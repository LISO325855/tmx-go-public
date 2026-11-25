package store

import (
	"path/filepath"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// The Board's fields ride on a thread through the store without a schema version of their own.
func TestThreadBoardFieldsAreKeptWithoutANewSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tmx.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := s.SchemaVersion(); err != nil || v != len(migrations) {
		t.Fatalf("schema version %d %v, want %d: the Board's table must not be a numbered migration", v, err, len(migrations))
	}
	pct := 0
	plain := model.Thread{ID: "t-plain", Title: "Plain", Kind: model.ThreadTopic, CreatedAt: 1, UpdatedAt: 1}
	board := model.Thread{ID: "t-board", Title: "Board", Kind: model.ThreadTopic, CreatedAt: 2, UpdatedAt: 2,
		Progress: &pct, ProgressWhy: "Nothing bought yet.", Blocker: "Raise the card limit."}
	for _, th := range []model.Thread{plain, board} {
		if err := s.PutThread(th); err != nil {
			t.Fatal(err)
		}
	}
	check := func(s *Store) {
		t.Helper()
		got, err := s.GetThread("t-board")
		if err != nil || got.Progress == nil || *got.Progress != 0 || got.ProgressWhy != "Nothing bought yet." || got.Blocker != "Raise the card limit." {
			t.Fatalf("one thread: %+v %v", got, err)
		}
		if got, err := s.GetThread("t-plain"); err != nil || got.Progress != nil || got.ProgressWhy != "" || got.Blocker != "" {
			t.Fatalf("a thread that reported nothing: %+v %v", got, err)
		}
		all, err := s.ListThreads()
		if err != nil {
			t.Fatal(err)
		}
		seen := 0
		for _, th := range all {
			switch th.ID {
			case "t-board":
				seen++
				if th.Progress == nil || *th.Progress != 0 || th.Blocker != "Raise the card limit." {
					t.Fatalf("the list: %+v", th)
				}
			case "t-plain":
				seen++
				if th.Progress != nil {
					t.Fatalf("the list: %+v", th)
				}
			}
		}
		if seen != 2 {
			t.Fatalf("the list has %d of the 2 threads", seen)
		}
	}
	check(s)
	// a restart keeps it, and opening again is harmless
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	check(s)

	// clearing the fields drops the row; deleting the thread does too
	board.Progress, board.ProgressWhy, board.Blocker = nil, "", ""
	if err := s.PutThread(board); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetThread("t-board"); got.Progress != nil || got.Blocker != "" {
		t.Fatalf("cleared: %+v", got)
	}
	full := 100
	plain.Progress = &full
	if err := s.PutThread(plain); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteThread("t-plain", 9); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM thread_board`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows left behind: %d %v", n, err)
	}
	// a value written by something else is kept inside 0-100
	if _, err := s.db.Exec(`INSERT INTO thread_board (thread_id, data) VALUES ('t-board', '{"progress": 250}')`); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetThread("t-board"); got.Progress == nil || *got.Progress != 100 {
		t.Fatalf("out of range: %+v", got)
	}
}
