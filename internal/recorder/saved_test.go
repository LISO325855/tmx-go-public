package recorder

import (
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// While a recording is live, the node says how much recorded time it has stored ("saved so
// far"): the sound's length where there is sound, else the time the window sent with its
// chunks. A 2-hour run on 17 Nov showed 0 from the first second to the last.
func TestLiveRecordingReportsWhatIsSaved(t *testing.T) {
	r := newRig(t)
	rec := begin(t, r.s, model.RecordingScreen, true)
	if a := r.s.Active(); a == nil || a.SavedMS != 0 {
		t.Fatalf("nothing sent yet: %+v", a)
	}
	// media only: the window's count that came with the chunk
	if _, err := r.s.Append(rec.ID, 1, TrackMedia, 0, 4200, []byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	if a := r.s.Active(); a == nil || a.SavedMS != 4200 {
		t.Fatalf("after a media chunk with dur=4200: %+v", a)
	}
	// with sound stored, its length is what counts
	if _, err := r.s.Append(rec.ID, 1, TrackPCM, 0, 0, pcm(3000)); err != nil {
		t.Fatal(err)
	}
	if a := r.s.Active(); a == nil || a.SavedMS != 3000 || a.DurationMS != 0 {
		t.Fatalf("after 3 s of sound: saved %d, duration %d", a.SavedMS, a.DurationMS)
	}
}
