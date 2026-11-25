package teamstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

const day = int64(24 * 3600_000)

// Ported from tmx-go v0.1.20's store test: the same records, the same answers.
func TestTeamReportsChangesAndPruning(t *testing.T) {
	p := config.Paths{Home: t.TempDir()}
	s := New(p)
	now := 100 * day
	put := func(peer, report string, at int64, via string) (TeamReport, bool) {
		t.Helper()
		prev, ok, err := s.PutTeamReport(TeamReport{ChatID: "g1", PeerID: peer, Report: report, Envelope: "{}", SignedAt: at, ReceivedAt: at, Via: via})
		if err != nil {
			t.Fatal(err)
		}
		return prev, ok
	}
	if g, err := s.TeamGroup("g1"); err != nil || g.Share != ShareUnasked || g.Open || g.Collector != "" || g.ChatID != "g1" {
		t.Fatalf("a group's team settings before any: %+v %v", g, err)
	}
	if _, had := put("p-bo", `{"v":"a"}`, now-31*day, ""); had {
		t.Fatal("a first report replaced something")
	}
	if prev, had := put("p-bo", `{"v":"b"}`, now-2*day, ""); !had || prev.Report != `{"v":"a"}` {
		t.Fatalf("the replaced report: %+v %v", prev, had)
	}
	put("p-cleo", `{"v":"c"}`, now-31*day, "")
	put("p-dan", `{"v":"d"}`, now-day, "admin")
	if err := s.AddTeamChanges([]TeamChange{
		{ChatID: "g1", PeerID: "p-bo", At: now - 40*day, Field: "build", Old: "v0.1.16", New: "v0.1.17"},
		{ChatID: "g1", PeerID: "p-bo", At: now - 2*day, Field: "build", Old: "v0.1.17", New: "v0.1.18"},
		{ChatID: "g1", PeerID: "p-bo", At: now - day, Field: "whatsapp", Old: "connected", New: "error"},
		{ChatID: "g1", PeerID: "p-cleo", At: now - day, Field: "phone", Old: "connected", New: "not_set"},
	}); err != nil {
		t.Fatal(err)
	}
	if cs, _ := s.TeamChanges("g1", "p-bo", 0); len(cs) != 3 || cs[0].Field != "whatsapp" || cs[2].New != "v0.1.17" {
		t.Fatalf("changes, newest first: %+v", cs)
	}
	if cs, _ := s.TeamChanges("g1", "p-bo", 1); len(cs) != 1 || cs[0].Field != "whatsapp" {
		t.Fatalf("limit 1: %+v", cs)
	}

	// A restart reads the same back, and 30 days still prune a group nobody asked for yet.
	s = New(p)
	n, err := s.PruneTeam(now - 30*day)
	if err != nil || n != 2 {
		t.Fatalf("pruned %d %v", n, err)
	}
	rs, _ := s.TeamReports("g1")
	if len(rs) != 2 || rs[0].PeerID != "p-bo" || rs[0].Report != `{"v":"b"}` || rs[1].Via != "admin" {
		t.Fatalf("reports after pruning: %+v", rs)
	}
	if cs, _ := s.TeamChanges("g1", "p-bo", 0); len(cs) != 2 {
		t.Fatalf("changes after pruning: %+v", cs)
	}
	// Reports passed on by the admin go on their own.
	if err := s.DeleteTeamReports("g1", false); err != nil {
		t.Fatal(err)
	}
	if rs, _ := s.TeamReports("g1"); len(rs) != 1 || rs[0].PeerID != "p-bo" {
		t.Fatalf("after dropping the passed-on ones: %+v", rs)
	}
	if err := s.PutTeamGroup(TeamGroup{ChatID: "g1", Share: ShareOn, Collector: "p-alex", Open: true, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	s = New(p)
	if g, _ := s.TeamGroup("g1"); g.Share != ShareOn || g.Collector != "p-alex" || !g.Open {
		t.Fatalf("group settings after a restart: %+v", g)
	}
	if rs, _ := s.TeamReports("g1"); len(rs) != 1 {
		t.Fatalf("a deleted report came back after a restart: %+v", rs)
	}
	// Deleting the chat takes everything.
	if err := s.DeleteGroup("g1"); err != nil {
		t.Fatal(err)
	}
	for _, st := range []*Store{s, New(p)} {
		rs, _ := st.TeamReports("g1")
		cs, _ := st.TeamChanges("g1", "p-bo", 0)
		g, _ := st.TeamGroup("g1")
		if len(rs) != 0 || len(cs) != 0 || g.Share != ShareUnasked {
			t.Fatalf("left after deleting the chat: %d reports, %d changes, %+v", len(rs), len(cs), g)
		}
	}
}

// IDs come from the network: whatever they contain, a record lands in the store's own folder
// under a name the store chose, groups stay apart, and a file planted for another group or
// computer is not believed.
func TestIDsNeverBecomePaths(t *testing.T) {
	p := config.Paths{Home: t.TempDir()}
	s := New(p)
	evil := []string{"../../escape", `..\..\escape`, "a/b", "CON", strings.Repeat("x", 5000), "g1\x00", "."}
	for i, id := range evil {
		if _, _, err := s.PutTeamReport(TeamReport{ChatID: id, PeerID: id, Report: "{}", SignedAt: int64(i + 1)}); err != nil {
			t.Fatalf("%q: %v", id[:min(len(id), 20)], err)
		}
		if err := s.PutTeamGroup(TeamGroup{ChatID: id, Share: ShareOn}); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(p.Home, "team")
	var files int
	err := filepath.WalkDir(p.Home, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files++
			if rel, err := filepath.Rel(root, path); err != nil || strings.HasPrefix(rel, "..") {
				t.Fatalf("a file outside the store's folder: %s", path)
			}
		}
		return nil
	})
	if err != nil || files != 2*len(evil) {
		t.Fatalf("%d files, %v", files, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(p.Home), "escape")); err == nil {
		t.Fatal("an ID escaped the state folder")
	}
	s = New(p)
	for _, id := range evil {
		if rs, _ := s.TeamReports(id); len(rs) != 1 || rs[0].PeerID != id {
			t.Fatalf("%q: %d reports", id[:min(len(id), 20)], len(rs))
		}
	}
	if _, _, err := s.PutTeamReport(TeamReport{PeerID: "p"}); err == nil {
		t.Fatal("a report without a group was stored")
	}

	// A file copied into another group's folder, or renamed to another computer, is skipped.
	src, _ := os.ReadFile(s.reportFile("a/b", "a/b"))
	if err := os.WriteFile(filepath.Join(s.groupDir("CON"), "r-"+name("someone")+".json"), src, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.groupDir("CON"), "r-junk.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rs, _ := New(p).TeamReports("CON"); len(rs) != 1 || rs[0].PeerID != "CON" {
		t.Fatalf("a planted file was believed: %+v", rs)
	}
}

// A group's changes are bounded: the newest are kept.
func TestChangesAreBounded(t *testing.T) {
	s := New(config.Paths{Home: t.TempDir()})
	var cs []TeamChange
	for i := range maxChanges + 50 {
		cs = append(cs, TeamChange{ChatID: "g", PeerID: "p", At: int64(i), Field: "build"})
	}
	if err := s.AddTeamChanges(cs); err != nil {
		t.Fatal(err)
	}
	got, _ := s.TeamChanges("g", "p", maxChanges+100)
	if len(got) != maxChanges || got[0].At != int64(maxChanges+49) || got[len(got)-1].At != 50 {
		t.Fatalf("%d kept, newest %d, oldest %d", len(got), got[0].At, got[len(got)-1].At)
	}
	if err := s.AddTeamChanges(nil); err != nil {
		t.Fatal(err)
	}
}
