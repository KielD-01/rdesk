package host

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/pion/webrtc/v4"
)

const fileChunk = 16 << 10

type fileEntry struct {
	Name string `json:"name"`
	Dir  bool   `json:"dir,omitempty"`
	Size int64  `json:"size,omitempty"`
}

type fileMeta struct {
	T    string `json:"t,omitempty"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	Path string `json:"path,omitempty"`
	Err  string `json:"error,omitempty"`
}

// listDir answers a viewer's directory listing request.
func (s *session) listDir(path string) {
	if path == "" {
		path, _ = os.UserHomeDir()
	}
	path = filepath.Clean(path)
	ents, err := os.ReadDir(path)
	if err != nil {
		s.sendControl(ctrl{T: "ls", Path: path, Error: err.Error()})
		return
	}
	out := make([]fileEntry, 0, len(ents))
	for _, e := range ents {
		fe := fileEntry{Name: e.Name(), Dir: e.IsDir()}
		if info, err := e.Info(); err == nil && !e.IsDir() {
			fe.Size = info.Size()
		}
		if fe.Dir || (e.Type().IsRegular()) || e.Type()&os.ModeSymlink != 0 {
			out = append(out, fe)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	parent := filepath.Dir(path)
	if parent == path {
		parent = ""
	}
	s.sendControl(ctrl{T: "ls", Path: path, Parent: parent, Entries: out})
}

// sendFile streams a host file to the viewer on a new "download" channel.
func (s *session) sendFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		s.sendControl(ctrl{T: "get-error", Path: path, Error: err.Error()})
		return
	}
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		f.Close()
		s.sendControl(ctrl{T: "get-error", Path: path, Error: "not a regular file"})
		return
	}
	ordered := true
	dc, err := s.pc.CreateDataChannel("download", &webrtc.DataChannelInit{Ordered: &ordered})
	if err != nil {
		f.Close()
		s.sendControl(ctrl{T: "get-error", Path: path, Error: err.Error()})
		return
	}
	low := make(chan struct{}, 1)
	dc.SetBufferedAmountLowThreshold(1 << 20)
	dc.OnBufferedAmountLow(func() {
		select {
		case low <- struct{}{}:
		default:
		}
	})
	dc.OnOpen(func() {
		go func() {
			defer f.Close()
			meta, _ := json.Marshal(fileMeta{T: "meta", Name: filepath.Base(path), Size: info.Size()})
			if dc.SendText(string(meta)) != nil {
				return
			}
			buf := make([]byte, fileChunk)
			for {
				n, err := f.Read(buf)
				if n > 0 {
					for dc.BufferedAmount() > 4<<20 {
						select {
						case <-low:
						case <-s.done:
							return
						case <-time.After(5 * time.Second):
						}
					}
					if dc.Send(buf[:n]) != nil {
						return
					}
				}
				if err == io.EOF {
					break
				}
				if err != nil {
					b, _ := json.Marshal(fileMeta{T: "error", Err: err.Error()})
					dc.SendText(string(b))
					return
				}
			}
			b, _ := json.Marshal(fileMeta{T: "done"})
			dc.SendText(string(b))
			s.h.log.Info("file sent to viewer", "path", path, "bytes", info.Size())
		}()
	})
}

// receiveFile stores a file the viewer uploads on an "upload" channel:
// a JSON header {name,size}, then binary chunks.
func (s *session) receiveFile(dc *webrtc.DataChannel) {
	var (
		f    *os.File
		meta fileMeta
		got  int64
		done bool
	)
	reply := func(m fileMeta) {
		b, _ := json.Marshal(m)
		dc.SendText(string(b))
	}
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		if f == nil {
			if !msg.IsString || json.Unmarshal(msg.Data, &meta) != nil {
				dc.Close()
				return
			}
			var err error
			f, meta.Path, err = createUnique(s.h.opt.Download, meta.Name)
			if err != nil {
				reply(fileMeta{T: "error", Err: err.Error()})
				dc.Close()
				return
			}
			reply(fileMeta{T: "ready", Path: meta.Path})
			if meta.Size == 0 {
				f.Close()
				done = true
				reply(fileMeta{T: "done", Path: meta.Path})
			}
			return
		}
		if done || msg.IsString {
			return
		}
		if _, err := f.Write(msg.Data); err != nil {
			reply(fileMeta{T: "error", Err: err.Error()})
			f.Close()
			os.Remove(meta.Path)
			done = true
			return
		}
		got += int64(len(msg.Data))
		if got >= meta.Size {
			f.Close()
			done = true
			reply(fileMeta{T: "done", Path: meta.Path})
			s.h.log.Info("file received from viewer", "path", meta.Path, "bytes", got)
		}
	})
	dc.OnClose(func() {
		if f != nil && !done {
			f.Close()
			os.Remove(meta.Path)
			s.h.log.Warn("upload aborted", "path", meta.Path)
		}
	})
}

// createUnique creates dir/name, adding " (n)" when the name is taken.
func createUnique(dir, name string) (*os.File, string, error) {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if runtime.GOOS == "windows" {
		name = strings.Map(func(r rune) rune {
			if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
				return '_'
			}
			return r
		}, name)
	}
	if name == "" || name == "." || name == ".." || name == "/" {
		name = "upload"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", err
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 1000; i++ {
		n := name
		if i > 0 {
			n = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		p := filepath.Join(dir, n)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return f, p, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("too many files named %s", name)
}
