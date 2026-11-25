package harness

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the same driver the store uses; opened read-only here
)

// OpenCodeDBPath is opencode's session database (`opencode db path`): $XDG_DATA_HOME/opencode
// or ~/.local/share/opencode, on every OS.
func OpenCodeDBPath(home string) string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "opencode", "opencode.db")
}

// OpenCodeFindNew returns the id of the one top-level opencode session in cwd created at or
// after since; "" if there is none yet, or several (they cannot be told apart, and binding the
// wrong one would resume someone else's conversation). opencode 1.19 does not accept an id of
// tmx's choosing (`-s` fails with "Session not found" unless the session exists), so a new
// conversation starts without `-s` and its id is read back from this database.
func OpenCodeFindNew(dbPath, cwd string, since time.Time) string {
	if _, err := os.Stat(dbPath); err != nil {
		return ""
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return ""
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id, directory FROM session
		WHERE parent_id IS NULL AND time_created >= ? ORDER BY time_created`, since.UnixMilli())
	if err != nil {
		return ""
	}
	defer rows.Close()
	want := sameDir(cwd)
	var found []string
	for rows.Next() {
		var id, dir string
		if rows.Scan(&id, &dir) == nil && sameDir(dir) == want {
			found = append(found, id)
		}
	}
	if len(found) != 1 {
		return ""
	}
	return found[0]
}

func sameDir(p string) string {
	// opencode stores forward slashes; a Windows cwd has backslashes (ToSlash converts them only on Windows).
	p = strings.TrimRight(strings.ReplaceAll(p, `\`, "/"), "/")
	if u, err := url.PathUnescape(p); err == nil {
		p = u
	}
	return strings.ToLower(p)
}
