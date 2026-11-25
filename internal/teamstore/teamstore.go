// Package teamstore keeps team status (DESIGN.md "Team status") on disk: for each group, this
// install's settings, the latest report of each computer and the recorded state changes. It
// holds JSON as the chat layer hands it over and never interprets a report.
//
// It is a folder of small files under the state folder, not database tables: the database's
// migrations are numbered by their position in one list, so a table added on one branch and
// another on a second branch would collide on a machine that already ran either. A report is
// its own file (a few kB, rewritten when that computer reports), so one computer's report
// never rewrites the others'.
package teamstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

// TeamReport is the latest report of one computer in a group.
type TeamReport struct {
	ChatID     string `json:"chat_id"`
	PeerID     string `json:"peer_id"`
	Report     string `json:"report"`   // the report's JSON (sanitized)
	Envelope   string `json:"envelope"` // the signed chat.status envelope that carried it (JSON)
	SignedAt   int64  `json:"signed_at"`
	ReceivedAt int64  `json:"received_at"`
	Via        string `json:"via"` // "" = from that computer itself; "admin" = passed on by the group's admin
}

// TeamChange is one recorded state change.
type TeamChange struct {
	ChatID string `json:"chat_id"`
	PeerID string `json:"peer_id"`
	At     int64  `json:"at"`
	Field  string `json:"field"`
	Old    string `json:"old"`
	New    string `json:"new"`
}

// Share values of a group (TeamGroup.Share).
const (
	ShareUnasked = 0 // the owner has not decided: the opt-in is offered
	ShareOn      = 1
	ShareOff     = 2
)

// TeamGroup is this install's team settings for one group.
type TeamGroup struct {
	ChatID    string `json:"chat_id"`
	Share     int    `json:"share"`     // ShareUnasked | ShareOn | ShareOff
	Collector string `json:"collector"` // the admin (peer ID) whose tmx said it takes reports; "" = none
	Open      bool   `json:"open"`      // every member sees the team (the admin's setting, or as it announced)
	UpdatedAt int64  `json:"updated_at"`
}

const (
	// maxChanges bounds a group's recorded changes (the newest are kept): the admin keeps 30
	// days, and a state change is rare.
	maxChanges = 2000
	// maxFile bounds any file read back: a report with its envelope is a few kB.
	maxFile = 1 << 20
)

// Store is the folder. Its methods are safe for concurrent use.
type Store struct {
	dir string

	mu     sync.Mutex
	groups map[string]*group // by chat ID, loaded on first use
}

type group struct {
	settings TeamGroup
	reports  map[string]TeamReport // by peer ID
	changes  []TeamChange          // oldest first
}

// New opens the store under p (nothing is read until a group is asked for).
func New(p config.Paths) *Store {
	return &Store{dir: filepath.Join(p.Home, "team"), groups: map[string]*group{}}
}

// name is a file-system-safe name for an ID from the network: never the ID itself.
func name(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:16])
}

func (s *Store) groupDir(chatID string) string { return filepath.Join(s.dir, name(chatID)) }

func readJSON(path string, v any) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Size() > maxFile {
		return errors.New("file too large")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return config.WriteFileAtomic(path, b, 0o600)
}

// load returns the group's data, reading its folder the first time. A file that cannot be read
// or belongs to another group or computer is skipped: it costs that one record.
func (s *Store) load(chatID string) *group {
	if g, ok := s.groups[chatID]; ok {
		return g
	}
	g := &group{settings: TeamGroup{ChatID: chatID}, reports: map[string]TeamReport{}}
	dir := s.groupDir(chatID)
	var set TeamGroup
	if readJSON(filepath.Join(dir, "group.json"), &set) == nil && set.ChatID == chatID {
		g.settings = set
	}
	var changes []TeamChange
	if readJSON(filepath.Join(dir, "changes.json"), &changes) == nil {
		for _, c := range changes {
			if c.ChatID == chatID {
				g.changes = append(g.changes, c)
			}
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "r-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var r TeamReport
		if readJSON(filepath.Join(dir, e.Name()), &r) == nil && r.ChatID == chatID && r.PeerID != "" && e.Name() == "r-"+name(r.PeerID)+".json" {
			g.reports[r.PeerID] = r
		}
	}
	s.groups[chatID] = g
	return g
}

func (s *Store) reportFile(chatID, peerID string) string {
	return filepath.Join(s.groupDir(chatID), "r-"+name(peerID)+".json")
}

// PutTeamReport stores a computer's latest report and returns the one it replaced (ok false: none).
func (s *Store) PutTeamReport(r TeamReport) (prev TeamReport, ok bool, err error) {
	if r.ChatID == "" || r.PeerID == "" {
		return prev, false, errors.New("team report without a group or a computer")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.load(r.ChatID)
	prev, ok = g.reports[r.PeerID]
	if err := writeJSON(s.reportFile(r.ChatID, r.PeerID), r); err != nil {
		return prev, ok, err
	}
	g.reports[r.PeerID] = r
	return prev, ok, nil
}

// TeamReports lists a group's stored reports, by peer ID.
func (s *Store) TeamReports(chatID string) ([]TeamReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.load(chatID)
	out := make([]TeamReport, 0, len(g.reports))
	for _, r := range g.reports {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PeerID < out[j].PeerID })
	return out, nil
}

// DeleteTeamReports removes a group's reports passed on by its admin (via "admin"), or all of
// them (all), e.g. when the admin closes the team to members.
func (s *Store) DeleteTeamReports(chatID string, all bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.load(chatID)
	var first error
	for id, r := range g.reports {
		if !all && r.Via != "admin" {
			continue
		}
		if err := os.Remove(s.reportFile(chatID, id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			if first == nil {
				first = err
			}
			continue // still on disk: keep it in memory too, so both agree
		}
		delete(g.reports, id)
	}
	return first
}

// AddTeamChanges records state changes (of one group or several).
func (s *Store) AddTeamChanges(cs []TeamChange) error {
	if len(cs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	touched := map[string]bool{}
	for _, c := range cs {
		if c.ChatID == "" {
			continue
		}
		g := s.load(c.ChatID)
		g.changes = append(g.changes, c)
		if over := len(g.changes) - maxChanges; over > 0 {
			g.changes = append([]TeamChange(nil), g.changes[over:]...)
		}
		touched[c.ChatID] = true
	}
	var first error
	for chatID := range touched {
		if err := writeJSON(filepath.Join(s.groupDir(chatID), "changes.json"), s.groups[chatID].changes); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// TeamChanges lists a computer's latest state changes in a group, newest first (limit ≤ 0 = 50).
func (s *Store) TeamChanges(chatID, peerID string, limit int) ([]TeamChange, error) {
	if limit <= 0 {
		limit = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.load(chatID)
	var out []TeamChange
	for i := len(g.changes) - 1; i >= 0 && len(out) < limit; i-- {
		if g.changes[i].PeerID == peerID {
			out = append(out, g.changes[i])
		}
	}
	// Newest first by time; among equal times the later-recorded one first (as recorded).
	sort.SliceStable(out, func(i, j int) bool { return out[i].At > out[j].At })
	return out, nil
}

// PruneTeam removes state changes, and reports last signed, before the given time (Unix ms):
// the 30 days the admin keeps. It looks at every group on disk and returns how many records went.
func (s *Store) PruneTeam(before int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Groups not asked for since the start still have files: find them by their settings or
	// their records, which carry the chat ID.
	entries, err := os.ReadDir(s.dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if id := s.chatIDOf(filepath.Join(s.dir, e.Name())); id != "" && name(id) == e.Name() {
			s.load(id)
		}
	}
	var n int64
	var first error
	for chatID, g := range s.groups {
		kept := g.changes[:0:0]
		for _, c := range g.changes {
			if c.At >= before {
				kept = append(kept, c)
			}
		}
		if gone := len(g.changes) - len(kept); gone > 0 {
			if err := writeJSON(filepath.Join(s.groupDir(chatID), "changes.json"), kept); err != nil {
				if first == nil {
					first = err
				}
			} else {
				g.changes = kept
				n += int64(gone)
			}
		}
		for id, r := range g.reports {
			if r.SignedAt >= before {
				continue
			}
			if err := os.Remove(s.reportFile(chatID, id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				if first == nil {
					first = err
				}
				continue
			}
			delete(g.reports, id)
			n++
		}
	}
	return n, first
}

// chatIDOf finds the chat ID a group folder belongs to, from any record in it.
func (s *Store) chatIDOf(dir string) string {
	var set TeamGroup
	if readJSON(filepath.Join(dir, "group.json"), &set) == nil && set.ChatID != "" {
		return set.ChatID
	}
	var changes []TeamChange
	if readJSON(filepath.Join(dir, "changes.json"), &changes) == nil && len(changes) > 0 {
		return changes[0].ChatID
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		var r TeamReport
		if strings.HasPrefix(e.Name(), "r-") && readJSON(filepath.Join(dir, e.Name()), &r) == nil && r.ChatID != "" {
			return r.ChatID
		}
	}
	return ""
}

// TeamGroup returns a group's team settings (zero values when never set).
func (s *Store) TeamGroup(chatID string) (TeamGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(chatID).settings, nil
}

// PutTeamGroup saves a group's team settings.
func (s *Store) PutTeamGroup(g TeamGroup) error {
	if g.ChatID == "" {
		return errors.New("team settings without a group")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.load(g.ChatID)
	if err := writeJSON(filepath.Join(s.groupDir(g.ChatID), "group.json"), g); err != nil {
		return err
	}
	cur.settings = g
	return nil
}

// DeleteGroup removes everything kept for a group (its chat was deleted).
func (s *Store) DeleteGroup(chatID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.groups, chatID)
	return os.RemoveAll(s.groupDir(chatID))
}
