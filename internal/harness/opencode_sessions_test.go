package harness_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/harness"
	_ "modernc.org/sqlite"
)

func openCodeDB(t *testing.T, rows [][4]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY, parent_id TEXT, directory TEXT, time_created INTEGER)`); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO session VALUES (?,?,?,?)`, r[0], r[1], r[2], r[3]); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestOpenCodeFindNew(t *testing.T) {
	since := time.UnixMilli(1_000_000)
	cwd := `C:\Work\Proj`
	// opencode stores forward slashes; compare case-insensitively.
	db := openCodeDB(t, [][4]any{
		{"ses_old", nil, "C:/Work/Proj", int64(900_000)},       // before the launch
		{"ses_other", nil, "C:/Work/Other", int64(1_000_500)},  // another folder
		{"ses_sub", "ses_x", "C:/Work/Proj", int64(1_000_600)}, // a sub-agent's session
		{"ses_new", nil, "c:/work/proj/", int64(1_001_000)},
	})
	if got := harness.OpenCodeFindNew(db, cwd, since); got != "ses_new" {
		t.Fatalf("got %q, want ses_new", got)
	}
	// Nothing yet: "" so the watcher keeps polling.
	if got := harness.OpenCodeFindNew(db, cwd, time.UnixMilli(2_000_000)); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	// Two candidates cannot be told apart: never bind either.
	db2 := openCodeDB(t, [][4]any{
		{"ses_a", nil, "C:/Work/Proj", int64(1_001_000)},
		{"ses_b", nil, "C:/Work/Proj", int64(1_002_000)},
	})
	if got := harness.OpenCodeFindNew(db2, cwd, since); got != "" {
		t.Fatalf("ambiguous: got %q, want empty", got)
	}
	if got := harness.OpenCodeFindNew(filepath.Join(t.TempDir(), "missing.db"), cwd, since); got != "" {
		t.Fatalf("missing db: got %q", got)
	}
}
