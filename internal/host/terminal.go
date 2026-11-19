package host

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/aymanbagabas/go-pty"
	"github.com/pion/webrtc/v4"
)

// termMsg is a text message on a "term" data channel. The viewer opens the
// channel with {cols, rows}, then sends keystrokes as binary messages and
// resizes as {"t":"resize"}; the host answers with binary output and
// {"t":"exit"} when the shell ends.
type termMsg struct {
	T    string `json:"t,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
	Code int    `json:"code,omitempty"`
	Err  string `json:"error,omitempty"`
}

// shell picks the user's shell: PowerShell on Windows, $SHELL elsewhere.
func shell() (string, []string) {
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("pwsh.exe"); err == nil {
			return p, []string{"-NoLogo"}
		}
		return "powershell.exe", []string{"-NoLogo"}
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh, []string{"-l"}
	}
	for _, sh := range []string{"/bin/bash", "/bin/zsh", "/bin/sh"} {
		if _, err := os.Stat(sh); err == nil {
			return sh, []string{"-l"}
		}
	}
	return "/bin/sh", nil
}

// serveTerminal runs a shell in a pseudo-terminal for one "term" channel.
func (s *session) serveTerminal(dc *webrtc.DataChannel) {
	var (
		mu      sync.Mutex
		p       pty.Pty
		cmd     *pty.Cmd
		started bool
		low     = make(chan struct{}, 1)
	)
	send := func(m termMsg) {
		b, _ := json.Marshal(m)
		dc.SendText(string(b))
	}
	dc.SetBufferedAmountLowThreshold(256 << 10)
	dc.OnBufferedAmountLow(func() {
		select {
		case low <- struct{}{}:
		default:
		}
	})

	start := func(cols, rows int) error {
		var err error
		if p, err = pty.New(); err != nil {
			return err
		}
		if cols > 0 && rows > 0 {
			p.Resize(cols, rows)
		}
		name, args := shell()
		cmd = p.Command(name, args...)
		cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
		if home, err := os.UserHomeDir(); err == nil {
			cmd.Dir = home
		}
		if err := cmd.Start(); err != nil {
			p.Close()
			return fmt.Errorf("start %s: %w", name, err)
		}
		s.h.log.Info("terminal opened", "sid", s.sid, "shell", name)

		// shell output → viewer
		go func() {
			buf := make([]byte, 32<<10)
			for {
				n, err := p.Read(buf)
				if n > 0 {
					for dc.BufferedAmount() > 1<<20 {
						select {
						case <-low:
						case <-s.done:
							return
						case <-time.After(2 * time.Second):
						}
					}
					if dc.Send(append([]byte(nil), buf[:n]...)) != nil {
						return
					}
				}
				if err != nil {
					return
				}
			}
		}()
		// shell exit → tell the viewer, close the channel
		go func() {
			err := cmd.Wait()
			code := 0
			if err != nil {
				code = 1
				if ee, ok := err.(*exec.ExitError); ok {
					code = ee.ExitCode()
				}
			}
			send(termMsg{T: "exit", Code: code})
			time.Sleep(200 * time.Millisecond)
			dc.Close()
		}()
		return nil
	}

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		mu.Lock()
		defer mu.Unlock()
		if !msg.IsString {
			if started {
				p.Write(msg.Data)
			}
			return
		}
		var m termMsg
		if json.Unmarshal(msg.Data, &m) != nil {
			return
		}
		switch {
		case !started:
			if err := start(m.Cols, m.Rows); err != nil {
				send(termMsg{T: "exit", Code: -1, Err: err.Error()})
				dc.Close()
				return
			}
			started = true
		case m.T == "resize" && m.Cols > 0 && m.Rows > 0:
			p.Resize(m.Cols, m.Rows)
		}
	})
	dc.OnClose(func() {
		mu.Lock()
		defer mu.Unlock()
		if !started {
			return
		}
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		p.Close()
		s.h.log.Info("terminal closed", "sid", s.sid)
	})
}
