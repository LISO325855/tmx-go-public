package whatsapp

import (
	"context"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}
func (s *Service) wakeMedia() {
	if s.o.AutoMedia {
		signal(s.mediaWake)
		signal(s.asrWake)
	}
}
func (s *Service) mediaWorker() { s.runMediaQueue(false) }
func (s *Service) audioWorker() { s.runMediaQueue(true) }

// Independent, single-consumer queues bound work without holding downloads behind ASR.
func (s *Service) runMediaQueue(audio bool) {
	defer s.wg.Done()
	wake := s.mediaWake
	if audio {
		wake = s.asrWake
	}
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-wake:
		case <-tick.C:
		}
		for s.ctx.Err() == nil && s.ready(!audio) == nil {
			s.mu.Lock()
			since := s.mediaSince
			s.mu.Unlock()
			batch, err := s.o.Store.WaMediaBatch(since, s.o.Now().UnixMilli(), audio, 1)
			if err != nil || len(batch) == 0 {
				break
			}
			item := batch[0]
			if audio {
				err = s.processAudio(item.Chat, item.ID)
			} else {
				_, err = s.media(s.ctx, item.Chat, item.ID, true)
				if err == nil {
					signal(s.asrWake)
				}
			}
			if err != nil && !s.mediaRetry(item.Chat, item.ID, audio) {
				break
			}
		}
	}
}
func (s *Service) processAudio(chat, id string) error {
	row, err := s.o.Store.WaGetMessage(chat, id)
	if err != nil {
		return err
	}
	if row.Deleted || row.Media == nil {
		return nil
	}
	if !s.fileExists(row.MediaFile) {
		return s.o.Store.WaExpireMediaFile(row.MediaFile)
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Minute)
	defer cancel()
	text, err := s.o.Transcribe(ctx, s.mediaPath(row.MediaFile))
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return errorf(503, "no speech transcript available")
	}
	s.evMu.Lock()
	defer s.evMu.Unlock()
	cur, err := s.o.Store.WaGetMessage(chat, id)
	if err != nil {
		return err
	}
	if cur.Deleted || cur.Media == nil || cur.MediaRef != row.MediaRef || cur.Media.Expired || !s.fileExists(cur.MediaFile) {
		return nil
	}
	cur.Media.Transcript = strings.TrimSpace(text)
	cur.Media.TranscribeRetry = nil
	if err := s.o.Store.WaPutMessage(cur); err != nil {
		return err
	}
	s.publishMessage(chat, id)
	return nil
}
func (s *Service) mediaRetry(chat, id string, audio bool) bool {
	s.evMu.Lock()
	defer s.evMu.Unlock()
	row, err := s.o.Store.WaGetMessage(chat, id)
	if err != nil {
		return false
	}
	if row.Deleted || row.Media == nil || row.Media.Expired {
		return true
	}
	previous := row.Media.DownloadRetry
	if audio {
		previous = row.Media.TranscribeRetry
	} else if row.MediaFile != "" {
		return true
	}
	attempts := 1
	if previous != nil {
		attempts = min(previous.Attempts+1, 16)
	}
	delay := min(time.Minute*time.Duration(1<<min(attempts-1, 9)), 6*time.Hour)
	retry := &model.WaMediaRetry{At: s.o.Now().Add(delay).UnixMilli(), Attempts: attempts}
	if audio {
		row.Media.TranscribeRetry = retry
	} else {
		row.Media.DownloadRetry = retry
	}
	return s.o.Store.WaPutMessage(row) == nil
}
