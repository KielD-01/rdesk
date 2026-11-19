// Package gui is the rdesk desktop window: this computer's ID and password,
// who is connected, and a field to connect to another computer.
package gui

import (
	"context"
	"fmt"
	"image/color"
	"log/slog"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"rdesk/internal/config"
	"rdesk/internal/host"
)

var icon = fyne.NewStaticResource("rdesk.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">`+
	`<rect x="3" y="5" width="26" height="17" rx="3" fill="#4f8cff"/><rect x="11" y="24" width="10" height="3" rx="1.5" fill="#4f8cff"/></svg>`))

var iconWhite = fyne.NewStaticResource("rdesk-white.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">`+
	`<rect x="3" y="5" width="26" height="17" rx="3" fill="#ffffff"/><rect x="11" y="24" width="10" height="3" rx="1.5" fill="#ffffff"/></svg>`))

// HostOptions builds the host options for a config; the GUI adds its callbacks.
type HostOptions func(config.Config) host.Options

type ui struct {
	app  fyne.App
	win  fyne.Window
	log  *slog.Logger
	opts HostOptions
	p    palette

	mu      sync.Mutex
	cfg     config.Config
	h       *host.Host
	cancel  context.CancelFunc
	id, pw  string
	seen    map[string]bool // sessions already announced
	online  bool
	lastErr error

	idText, pwText *canvas.Text
	pwHint         *canvas.Text
	newPwBtn       *widget.Button
	status         *pill
	sessions       *fyne.Container
	sessionColor   color.Color // text colour of session rows
}

// Run shows the window until the user quits.
func Run(cfg config.Config, opts HostOptions, log *slog.Logger) {
	p := darkPalette

	a := app.NewWithID("dev.romanovich.rdesk")
	a.SetIcon(icon)
	a.Settings().SetTheme(rdeskTheme{p})
	u := &ui{app: a, log: log, opts: opts, cfg: cfg, seen: map[string]bool{}, p: p}
	u.win = a.NewWindow("rdesk")
	content := u.build()
	u.win.SetContent(content)
	// As tall as the content: the window grows when sessions appear.
	u.win.Resize(fyne.NewSize(windowWidth, content.MinSize().Height))

	// Closing the window keeps rdesk reachable from the tray where trays are
	// dependable; on Linux (tray support varies by desktop) it asks to quit.
	if desk, ok := a.(desktop.App); ok {
		desk.SetSystemTrayIcon(icon)
		desk.SetSystemTrayMenu(fyne.NewMenu("rdesk", fyne.NewMenuItem("Показати rdesk", u.win.Show)))
		if runtime.GOOS != "linux" {
			u.win.SetCloseIntercept(u.win.Hide)
		}
	}
	if runtime.GOOS == "linux" {
		u.win.SetCloseIntercept(u.confirmQuit)
	}

	u.start()
	go u.tick()
	u.win.ShowAndRun()
	u.stop()
}

// ── building blocks ─────────────────────────────────────────────────────

// credentials builds the ID/password block in the given text colours.
func (u *ui) credentials(fg, label, pwColor color.Color) fyne.CanvasObject {
	u.idText = text("— — —", 36, fg, true, true)
	u.pwText = text("…", 24, pwColor, true, true)
	u.pwHint = text("", 12, label, false, false)

	copyID := u.iconButton(theme.ContentCopyIcon(), func() {
		u.copy(strings.ReplaceAll(u.currentID(), " ", ""), "ID скопійовано")
	})
	copyPw := u.iconButton(theme.ContentCopyIcon(), func() {
		u.mu.Lock()
		pw := u.pw
		u.mu.Unlock()
		u.copy(pw, "Пароль скопійовано")
	})
	u.newPwBtn = u.iconButton(theme.ViewRefreshIcon(), func() {
		if h := u.host(); h != nil {
			h.NewOneTimeCode()
		}
	})
	return container.NewVBox(
		text("ВАШ ID", 11, label, true, false),
		container.NewHBox(u.idText, layout.NewSpacer(), container.NewCenter(copyID)),
		gap(0, 8),
		text("ПАРОЛЬ", 11, label, true, false),
		container.NewHBox(u.pwText, layout.NewSpacer(), container.NewCenter(container.NewHBox(u.newPwBtn, copyPw))),
		u.pwHint,
	)
}

func (u *ui) iconButton(res fyne.Resource, fn func()) *widget.Button {
	b := widget.NewButtonWithIcon("", res, fn)
	b.Importance = widget.LowImportance
	return b
}

func (u *ui) connectBlock(label color.Color) fyne.CanvasObject {
	partner := widget.NewEntry()
	partner.SetPlaceHolder("ID іншого комп'ютера")
	connect := func() {
		id := digits(partner.Text)
		if len(id) != 9 {
			dialog.ShowInformation("rdesk", "ID складається з 9 цифр.", u.win)
			return
		}
		link, _ := url.Parse(u.serverURL() + "/#" + id)
		if err := u.app.OpenURL(link); err != nil {
			dialog.ShowError(err, u.win)
		}
	}
	partner.OnSubmitted = func(string) { connect() }
	btn := widget.NewButtonWithIcon("Підключитися", theme.NavigateNextIcon(), connect)
	btn.Importance = widget.HighImportance
	return container.NewVBox(
		text("ПІДКЛЮЧИТИСЯ ДО ІНШОГО", 11, label, true, false),
		partner,
		btn,
	)
}

func (u *ui) sessionsBlock(label, fg color.Color) fyne.CanvasObject {
	u.sessions = container.NewVBox()
	u.sessionColor = fg
	return container.NewVBox(text("ХТО ПІДКЛЮЧЕНИЙ", 11, label, true, false), u.sessions)
}

func (u *ui) footer() fyne.CanvasObject {
	settings := widget.NewButtonWithIcon("Налаштування", theme.SettingsIcon(), u.showSettings)
	settings.Importance = widget.LowImportance
	quit := widget.NewButtonWithIcon("Вийти", theme.LogoutIcon(), func() { u.app.Quit() })
	quit.Importance = widget.LowImportance
	return container.NewHBox(settings, layout.NewSpacer(), quit)
}

func brand(res fyne.Resource, c color.Color) fyne.CanvasObject {
	img := canvas.NewImageFromResource(res)
	img.SetMinSize(fyne.NewSize(26, 26))
	return container.NewHBox(img, container.NewCenter(text("rdesk", 20, c, true, false)))
}

// ── layout ──────────────────────────────────────────────────────────────

const windowWidth = 760

func (u *ui) build() fyne.CanvasObject {
	root := u.buildSplit()
	u.renderSessions()
	return root
}

// buildSplit: two columns, an accent panel with this computer's credentials on the
// left and everything else on the right (AnyDesk-like).
func (u *ui) buildSplit() fyne.CanvasObject {
	p := u.p
	white := color.White
	soft := color.NRGBA{0xff, 0xff, 0xff, 0xb8}
	grad := canvas.NewLinearGradient(rgb(0x4f8cff), rgb(0x2b5fd9), 135)
	u.status = newPill(p, color.NRGBA{0, 0, 0, 0x30}, white)
	left := container.NewStack(grad, inset(24, 22, container.NewBorder(
		container.NewVBox(brand(iconWhite, white), gap(0, 22)),
		container.NewVBox(gap(0, 22), container.NewHBox(u.status.obj, layout.NewSpacer())),
		nil, nil,
		container.NewCenter(container.NewVBox(
			text("Цей комп'ютер", 16, white, true, false),
			gap(0, 10),
			u.credentials(white, soft, white),
		)),
	)))
	right := inset(22, 20, container.NewBorder(nil, u.footer(), nil, nil, container.NewVBox(
		u.connectBlock(p.muted),
		gap(0, 10),
		widget.NewSeparator(),
		gap(0, 6),
		u.sessionsBlock(p.muted, p.text),
	)))
	return container.NewGridWithColumns(2, left, right)
}

// ── host lifecycle ──────────────────────────────────────────────────────

func (u *ui) start() {
	u.mu.Lock()
	cfg := u.cfg
	u.mu.Unlock()

	o := u.opts(cfg)
	o.OnCredentials = func(id, pw string, oneTime bool, _ string) {
		fyne.Do(func() { u.setCredentials(id, pw, oneTime) })
	}
	o.OnStatus = func(online bool, err error) {
		fyne.Do(func() { u.setStatus(online, err) })
	}
	o.OnSessions = func() { fyne.Do(u.renderSessions) }

	h := host.New(o, u.log)
	ctx, cancel := context.WithCancel(context.Background())
	u.mu.Lock()
	u.h, u.cancel = h, cancel
	u.mu.Unlock()
	u.setStatus(false, nil)
	go h.Run(ctx)
}

func (u *ui) stop() {
	u.mu.Lock()
	h, cancel := u.h, u.cancel
	u.h, u.cancel = nil, nil
	u.mu.Unlock()
	if cancel != nil {
		cancel()
		h.Close()
	}
}

func (u *ui) restart() {
	u.stop()
	u.idText.Text, u.pwText.Text = "— — —", "…"
	u.idText.Refresh()
	u.pwText.Refresh()
	u.start()
}

func (u *ui) host() *host.Host {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.h
}

// ── view updates (UI goroutine) ─────────────────────────────────────────

func (u *ui) setCredentials(id, pw string, oneTime bool) {
	u.mu.Lock()
	u.id, u.pw = id, pw
	u.mu.Unlock()
	u.idText.Text = id[0:3] + " " + id[3:6] + " " + id[6:]
	u.idText.Refresh()
	if oneTime {
		u.pwText.Text = pw
		u.pwHint.Text = "Одноразовий — змінюється після кожної сесії"
		u.newPwBtn.Show()
	} else {
		u.pwText.Text = "••••••••"
		u.pwHint.Text = "Постійний пароль — змінити в налаштуваннях"
		u.newPwBtn.Hide()
	}
	u.pwText.Refresh()
	u.pwHint.Refresh()
}

func (u *ui) setStatus(online bool, err error) {
	u.online, u.lastErr = online, err
	switch {
	case online:
		u.status.set("Онлайн", u.p.ok)
	case err != nil:
		u.status.set("Немає зв'язку", u.p.danger)
	default:
		u.status.set("З'єднання…", u.p.warn)
	}
}

func (u *ui) renderSessions() {
	if u.sessions == nil {
		return
	}
	h := u.host()
	u.sessions.RemoveAll()
	var list []host.SessionInfo
	if h != nil {
		list = h.Sessions()
	}
	if len(list) == 0 {
		u.sessions.Add(text("Ніхто не підключений", 13, u.p.muted, false, false))
	}
	for _, s := range list {
		id := s.ID
		var line string
		c := u.p.warn
		switch {
		case s.Authed:
			c = u.p.ok
			path := "P2P"
			if strings.HasPrefix(s.Path, "relay") {
				path = "через relay"
			}
			line = fmt.Sprintf("Керує · %s · %s", path, since(s.Since))
			if !u.seen[id] {
				u.seen[id] = true
				u.app.SendNotification(fyne.NewNotification("rdesk", "До цього комп'ютера підключилися."))
				u.win.Show()
			}
		case s.Path != "":
			line = "Перевірка пароля…"
		default:
			line = "Хтось підключається…"
		}
		_, d := dot(c, 9)
		btn := widget.NewButtonWithIcon("Від'єднати", theme.CancelIcon(), func() {
			if h := u.host(); h != nil {
				h.Disconnect(id)
			}
		})
		btn.Importance = widget.DangerImportance
		u.sessions.Add(container.NewHBox(d, container.NewCenter(text(line, 13, u.sessionColor, false, false)), layout.NewSpacer(), btn))
	}
	u.sessions.Refresh()
}

// tick refreshes session durations.
func (u *ui) tick() {
	for range time.Tick(30 * time.Second) {
		fyne.Do(u.renderSessions)
	}
}

// ── settings ────────────────────────────────────────────────────────────

func (u *ui) showSettings() {
	u.mu.Lock()
	cfg := u.cfg
	u.mu.Unlock()

	server := widget.NewEntry()
	server.SetText(cfg.Server)
	permanent := widget.NewCheck("Постійний пароль (доступ без людини біля комп'ютера)", nil)
	permanent.SetChecked(cfg.Password != "")
	password := widget.NewPasswordEntry()
	password.SetPlaceHolder("щонайменше 8 символів")
	password.SetText(cfg.Password)
	if !permanent.Checked {
		password.Disable()
	}
	permanent.OnChanged = func(on bool) {
		if on {
			password.Enable()
		} else {
			password.Disable()
		}
	}
	viewOnly := widget.NewCheck("Лише перегляд (ігнорувати мишу й клавіатуру)", nil)
	viewOnly.SetChecked(cfg.ViewOnly)
	audio := widget.NewCheck("Передавати звук", nil)
	audio.SetChecked(!cfg.NoAudio)

	items := []*widget.FormItem{
		widget.NewFormItem("Сервер", server),
		widget.NewFormItem("", permanent),
		widget.NewFormItem("Пароль", password),
		widget.NewFormItem("", viewOnly),
		widget.NewFormItem("", audio),
	}
	d := dialog.NewForm("Налаштування", "Зберегти", "Скасувати", items, func(ok bool) {
		if !ok {
			return
		}
		next := cfg
		next.Server = strings.TrimRight(strings.TrimSpace(server.Text), "/")
		if !strings.HasPrefix(next.Server, "https://") && !strings.HasPrefix(next.Server, "http://") {
			dialog.ShowInformation("rdesk", "Адреса сервера має починатися з https://", u.win)
			return
		}
		next.Password = ""
		if permanent.Checked {
			if len(password.Text) < 8 {
				dialog.ShowInformation("rdesk", "Постійний пароль — щонайменше 8 символів.", u.win)
				return
			}
			next.Password = password.Text
		}
		next.ViewOnly, next.NoAudio = viewOnly.Checked, !audio.Checked
		if err := config.Save(next); err != nil {
			dialog.ShowError(err, u.win)
			return
		}
		u.mu.Lock()
		u.cfg = next
		u.mu.Unlock()
		u.restart()
	}, u.win)
	d.Resize(fyne.NewSize(460, 0))
	d.Show()
}

// ── helpers ─────────────────────────────────────────────────────────────

func (u *ui) confirmQuit() {
	if h := u.host(); h != nil && len(h.Sessions()) > 0 {
		dialog.ShowConfirm("Вийти з rdesk?", "Активні підключення буде розірвано.",
			func(ok bool) {
				if ok {
					u.app.Quit()
				}
			}, u.win)
		return
	}
	u.app.Quit()
}

func (u *ui) copy(s, note string) {
	if s == "" {
		return
	}
	u.app.Clipboard().SetContent(s)
	u.status.set(note, u.p.accent)
	go func() {
		time.Sleep(2 * time.Second)
		fyne.Do(func() { u.setStatus(u.online, u.lastErr) })
	}()
}

func (u *ui) currentID() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.id
}

func (u *ui) serverURL() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return strings.TrimRight(u.cfg.Server, "/")
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func since(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "щойно"
	case d < time.Hour:
		return fmt.Sprintf("%d хв", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d год %d хв", int(d.Hours()), int(d.Minutes())%60)
	}
}
