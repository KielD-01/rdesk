package host

import (
	"context"
	"log/slog"
	"sync"

	"golang.design/x/clipboard"
)

// clipboardSync mirrors the host's text clipboard to viewers and applies
// text the viewers copy.
type clipboardSync struct {
	log       *slog.Logger
	broadcast func(string)

	mu      sync.Mutex
	started bool
	ok      bool
	last    string
}

func newClipboardSync(log *slog.Logger, broadcast func(string)) *clipboardSync {
	return &clipboardSync{log: log, broadcast: broadcast}
}

func (c *clipboardSync) start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return
	}
	c.started = true
	if err := clipboard.Init(); err != nil {
		c.log.Warn("clipboard sync disabled", "err", err)
		return
	}
	c.ok = true
	if b, err := clipboard.Read(context.Background(), clipboard.FmtText); err == nil {
		c.last = string(b)
	}
	ch := clipboard.Watch(context.Background(), clipboard.FmtText)
	go func() {
		for d := range ch {
			if d.Format != clipboard.FmtText || d.Sensitive {
				continue
			}
			t := string(d.Bytes)
			c.mu.Lock()
			changed := t != c.last && len(t) <= maxClip
			if changed {
				c.last = t
			}
			c.mu.Unlock()
			if changed {
				c.broadcast(t)
			}
		}
	}()
}

// set writes text from a viewer into the host clipboard.
func (c *clipboardSync) set(text string) {
	c.mu.Lock()
	if !c.ok || text == c.last {
		c.mu.Unlock()
		return
	}
	c.last = text
	c.mu.Unlock()
	clipboard.Write(context.Background(), clipboard.FmtText, []byte(text))
}
