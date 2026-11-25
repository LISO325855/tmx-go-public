package whatsapp

import (
	"io"
	"os"
	"sort"
	"time"
)

// Bounded directory reads; never recurse or follow symlinks.
func (s *Service) scanMedia(visit func(os.FileInfo)) error {
	dir, err := os.Open(s.o.MediaDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer dir.Close()
	for {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		entries, err := dir.ReadDir(64)
		for _, entry := range entries {
			info, e := entry.Info()
			if e == nil && info.Mode().IsRegular() {
				visit(info)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// Maintenance has its own goroutine and never holds evMu. The writer gate keeps
// files stable during directory/database I/O; connection events do not acquire
// this gate. Outbound files awaiting their row are snapshotted under mu and skipped.
func (s *Service) mediaMaintenance() {
	defer s.wg.Done()
	s.pruneMedia()
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
		case <-s.maintenanceWake:
		}
		s.pruneMedia()
	}
}

func (s *Service) pruneMedia() {
	if s.o.MediaDir == "" || s.ctx.Err() != nil {
		return
	}
	select {
	case s.mediaWrite <- struct{}{}:
	case <-s.ctx.Done():
		return
	}
	defer func() { <-s.mediaWrite }()
	s.mu.Lock()
	active := make(map[string]bool, len(s.activeFiles))
	for name := range s.activeFiles {
		active[name] = true
	}
	s.mu.Unlock()
	// One indexed bulk snapshot, instead of a query for every directory entry.
	referenced, err := s.o.Store.WaMediaFiles()
	if err != nil {
		s.log.Warn("whatsapp cache references", "err", err)
		return
	}
	cutoff := s.o.Now().Add(-s.o.Config().MediaRetention())
	quota := s.o.Config().MediaQuota()
	var used int64
	var candidates []os.FileInfo
	scan := s.scanMedia
	if s.maintenanceScan != nil {
		scan = s.maintenanceScan
	}
	if err := scan(func(info os.FileInfo) {
		used += info.Size()
		if !active[info.Name()] {
			candidates = append(candidates, info)
		}
	}); err != nil {
		s.log.Warn("whatsapp cache scan", "err", err)
		return
	}
	// Orphans/expired files first, then oldest retained files. Sort once and walk
	// once, even when a reduced quota requires evicting many attachments.
	mandatory := func(info os.FileInfo) bool { return !referenced[info.Name()] || info.ModTime().Before(cutoff) }
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if mandatory(a) != mandatory(b) {
			return mandatory(a)
		}
		if a.ModTime().Equal(b.ModTime()) {
			return a.Name() < b.Name()
		}
		return a.ModTime().Before(b.ModTime())
	})
	for _, info := range candidates {
		if s.ctx.Err() != nil {
			return
		}
		if !mandatory(info) && used <= quota {
			break
		}
		name := info.Name()
		if err := os.Remove(s.mediaPath(name)); err != nil && !os.IsNotExist(err) {
			continue
		}
		used -= info.Size()
		if referenced[name] {
			_ = s.o.Store.WaExpireMediaFile(name)
		}
	}
}
