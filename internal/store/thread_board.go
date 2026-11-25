package store

import (
	"encoding/json"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// The Board view's fields of a thread (model.Thread Progress, ProgressWhy, Blocker) live in a
// side table of their own, one JSON row per thread that has any. It is deliberately NOT a
// numbered migration: the fleet line shares the migration list with upstream, and every number
// we add there is a conflict at the merge. The table is created if missing each time the
// database opens, the schema version does not change, and an older build simply never looks at
// it. If upstream takes the Board, this folds into three columns and one numbered migration.
const threadBoardTable = `CREATE TABLE IF NOT EXISTS thread_board (thread_id TEXT PRIMARY KEY, data TEXT NOT NULL)`

type threadBoard struct {
	Progress    *int   `json:"progress,omitempty"`
	ProgressWhy string `json:"progress_why,omitempty"`
	Blocker     string `json:"blocker,omitempty"`
}

func (s *Store) ensureThreadBoard() error {
	_, err := s.db.Exec(threadBoardTable)
	return err
}

// putThreadBoard keeps a thread's Board fields; a thread with none has no row.
func (s *Store) putThreadBoard(t model.Thread) error {
	if t.Progress == nil && t.ProgressWhy == "" && t.Blocker == "" {
		_, err := s.db.Exec(`DELETE FROM thread_board WHERE thread_id = ?`, t.ID)
		return err
	}
	b, err := json.Marshal(threadBoard{Progress: t.Progress, ProgressWhy: t.ProgressWhy, Blocker: t.Blocker})
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO thread_board (thread_id, data) VALUES (?, ?)
		ON CONFLICT(thread_id) DO UPDATE SET data = excluded.data`, t.ID, string(b))
	return err
}

// threadBoards reads every thread's Board fields.
func (s *Store) threadBoards() (map[string]threadBoard, error) {
	rows, err := s.db.Query(`SELECT thread_id, data FROM thread_board`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]threadBoard{}
	for rows.Next() {
		var id, data string
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		var b threadBoard
		if json.Unmarshal([]byte(data), &b) == nil { // an unreadable row is a thread that reported nothing
			out[id] = b
		}
	}
	return out, rows.Err()
}

// fillThreadBoard puts the Board fields on threads just read from the threads table.
func (s *Store) fillThreadBoard(ts []model.Thread) error {
	if len(ts) == 0 {
		return nil
	}
	all, err := s.threadBoards()
	if err != nil {
		return err
	}
	for i := range ts {
		if b, ok := all[ts[i].ID]; ok {
			ts[i].Progress, ts[i].ProgressWhy, ts[i].Blocker = clampProgress(b.Progress), b.ProgressWhy, b.Blocker
		}
	}
	return nil
}

// clampProgress keeps a stored value inside 0-100 whatever wrote it.
func clampProgress(p *int) *int {
	if p == nil {
		return nil
	}
	n := min(max(*p, 0), 100)
	return &n
}
