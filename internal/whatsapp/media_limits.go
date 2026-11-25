package whatsapp

import (
	"context"
	"io"
	"os"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/sysinfo"
)

const mediaFreeFloor uint64 = 5 << 30

func (s *Service) cancelMediaDownloads(chat string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, cancel := range s.downloadCancels {
		if chat == "" || strings.HasPrefix(key, chat+"\x00") {
			cancel()
		}
	}
}

func (s *Service) markMedia(name string, active bool) {
	if name == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if active {
		s.activeFiles[name] = true
	} else {
		delete(s.activeFiles, name)
	}
}
func (s *Service) mediaFree() (uint64, error) {
	if s.o.DiskFree != nil {
		return s.o.DiskFree(s.o.MediaDir)
	}
	free, _, err := sysinfo.DiskSpace(s.o.MediaDir)
	return free, err
}
func (s *Service) mediaBudget(automatic bool) (int64, error) {
	var used int64
	err := s.scanMedia(func(info os.FileInfo) { used += info.Size() })
	if err != nil {
		return 0, err
	}
	budget := s.o.Config().MediaQuota() - used
	if automatic {
		free, err := s.mediaFree()
		if err != nil {
			return 0, err
		}
		if free <= mediaFreeFloor {
			return 0, errorf(507, "WhatsApp auto-download paused: less than 5 GB free")
		}
		budget = min(budget, int64(min(free-mediaFreeFloor, uint64(1<<62))))
	}
	if budget <= 0 {
		return 0, errorf(507, "WhatsApp media cache quota reached")
	}
	return budget, nil
}

// Deliberately does not embed os.File: io.Copy must not bypass Write via ReadFrom.
// Seeking and in-place decryption remain available, but no operation can grow
// the file beyond the limit. Cancel also prevents retries on another CDN host.
type limitedMediaFile struct {
	file   *os.File
	limit  int64
	cancel context.CancelFunc
	free   func() (uint64, error)
	err    error
}

func (f *limitedMediaFile) check(end int64) error {
	if f.err != nil {
		return f.err
	}
	if end < 0 || end > f.limit {
		f.err = errorf(413, "attachment exceeds the media size or cache quota limit")
	}
	if f.err == nil && f.free != nil {
		st, err := f.file.Stat()
		if err != nil {
			f.err = err
		} else if end > st.Size() {
			free, err := f.free()
			if err != nil {
				f.err = err
			} else if free <= mediaFreeFloor || uint64(end-st.Size()) > free-mediaFreeFloor {
				f.err = errorf(507, "WhatsApp auto-download paused: 5 GB free-space floor")
			}
		}
	}
	if f.err != nil && f.cancel != nil {
		f.cancel()
	}
	return f.err
}
func (f *limitedMediaFile) Write(p []byte) (int, error) {
	pos, err := f.file.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	if err = f.check(pos + int64(len(p))); err != nil {
		return 0, err
	}
	return f.file.Write(p)
}
func (f *limitedMediaFile) WriteAt(p []byte, off int64) (int, error) {
	if err := f.check(off + int64(len(p))); err != nil {
		return 0, err
	}
	return f.file.WriteAt(p, off)
}
func (f *limitedMediaFile) Truncate(n int64) error {
	if err := f.check(n); err != nil {
		return err
	}
	return f.file.Truncate(n)
}
func (f *limitedMediaFile) Read(p []byte) (int, error)              { return f.file.Read(p) }
func (f *limitedMediaFile) ReadAt(p []byte, off int64) (int, error) { return f.file.ReadAt(p, off) }
func (f *limitedMediaFile) Seek(off int64, whence int) (int64, error) {
	return f.file.Seek(off, whence)
}
func (f *limitedMediaFile) Stat() (os.FileInfo, error) { return f.file.Stat() }
