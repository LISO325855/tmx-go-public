package store

import (
	"strings"
	"testing"
)

func TestWaMediaCacheIndex(t *testing.T) {
	s := openTest(t)
	for _, q := range []string{
		`SELECT DISTINCT media_file FROM wa_messages WHERE media_file != ''`,
		`SELECT EXISTS(SELECT 1 FROM wa_messages WHERE media_file = 'fixture.ogg')`,
		`UPDATE wa_messages SET media = json_set(media, '$.expired', json('true')) WHERE media_file = 'fixture.ogg' AND media != ''`,
	} {
		rows, err := s.db.Query("EXPLAIN QUERY PLAN " + q)
		if err != nil {
			t.Fatal(err)
		}
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(details, "\n"), "wa_messages_media_file") {
			t.Fatalf("cache lookup missed index: %s: %v", q, details)
		}
	}
}
