package rules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/blackcode-ch/tmx-go/internal/peertext"
)

// The layout of a rules source (DESIGN.md "Fleet rules"):
//
//	manifest.json   which files each profile gets
//	rules/*.md      standing rules
//	apps/*.md       one per connected app: front matter (name, summary, state, ...) and its guide
//	pages/*.md      longer notes (an operations page, ...)
//
// Any *.md file is a document, named by its path without ".md". The manifest only says which
// documents are put into an agent's instructions and which are listed there to read on demand.

// ManifestFile is the manifest's path in the source.
const ManifestFile = "manifest.json"

// Limits of one accepted commit. A commit beyond them is not taken at all: a rule set is
// never applied in part.
const (
	MaxDocBytes   = 64 << 10
	MaxDocs       = 300
	MaxTotalBytes = 4 << 20
	maxManifest   = 256 << 10
)

// ManifestVersion is the manifest version this tmx reads.
const ManifestVersion = 1

// Manifest is manifest.json. Unknown fields are ignored, so a newer manifest that only adds
// some still loads here.
type Manifest struct {
	Version int `json:"version"`
	// InlineChars is how many characters of rules go into the instructions (0 = DefaultInline,
	// at most MaxInline). What does not fit is listed to read on demand.
	InlineChars int `json:"inline_chars,omitempty"`
	// All applies to every agent; then the entry of the agent's profile (by slug) or, when the
	// profile has none, of its level (head, lead, worker); then the entry of the machine.
	All      Set            `json:"all"`
	Profiles map[string]Set `json:"profiles,omitempty"`
	Levels   map[string]Set `json:"levels,omitempty"`
	Machines map[string]Set `json:"machines,omitempty"`
}

// Set is one group of documents.
type Set struct {
	// Inline documents are part of the instructions, in this order.
	Inline []string `json:"inline,omitempty"`
	// More documents are listed in the instructions by name and summary and read with
	// rules_read. An entry is a file ("pages/operations.md") or every file of a folder ("apps/*").
	More []string `json:"more,omitempty"`
}

// Change is one commit as git describes it. Author is what the commit says, which anyone who
// can write to the source can set: only a checked signature says who made a commit.
type Change struct {
	Commit  string    `json:"commit"`
	Author  string    `json:"author"`
	Time    time.Time `json:"time"`
	Subject string    `json:"subject"`
}

// Doc is one document of the source at the accepted commit.
type Doc struct {
	Name    string            `json:"name"` // the path without ".md"
	Path    string            `json:"path"`
	Title   string            `json:"title"`
	Summary string            `json:"summary,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"` // the front matter
	Body    string            `json:"-"`
	Size    int               `json:"size"`
	Last    Change            `json:"last_change"`
}

// Snapshot is the source at one accepted commit. It is never changed after it is built.
type Snapshot struct {
	Source    string
	Branch    string
	Head      Change
	Signature string // the id of the pinned signer; "" = not checked
	Manifest  Manifest
	Docs      map[string]*Doc // by name
	names     []string        // sorted
}

var docPathRx = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9][A-Za-z0-9._-]*)*\.md$`)

// DocName is the name a document is asked for by: its path without ".md", whatever the
// caller wrote ("rules/core", "rules/core.md", "/rules/core").
func DocName(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\\", "/"))
	return strings.TrimSuffix(strings.TrimPrefix(s, "/"), ".md")
}

// Names lists the documents, sorted.
func (s *Snapshot) Names() []string { return append([]string(nil), s.names...) }

// load builds the snapshot of commit sha, or says why that commit cannot be taken.
func load(ctx context.Context, g *repo, sha string) (*Snapshot, error) {
	head, err := g.commit(ctx, sha)
	if err != nil {
		return nil, err
	}
	entries, err := g.tree(ctx, sha)
	if err != nil {
		return nil, err
	}
	last, err := g.lastChanges(ctx, sha)
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{Head: head, Docs: map[string]*Doc{}}
	var manifest *treeEntry
	var total int64
	for i, e := range entries {
		switch {
		case e.path == ManifestFile:
			manifest = &entries[i]
		case !strings.HasSuffix(e.path, ".md"):
			continue // a README image, a licence: not a document
		case e.mode != "100644" && e.mode != "100755":
			return nil, fmt.Errorf("%s is not a plain file", peertext.Field(e.path))
		case !docPathRx.MatchString(e.path):
			return nil, fmt.Errorf("the file name %s has characters a rule file may not have", peertext.Field(e.path))
		case e.size > MaxDocBytes:
			return nil, fmt.Errorf("%s is larger than %d KB", e.path, MaxDocBytes>>10)
		default:
			if total += e.size; total > MaxTotalBytes || len(snap.Docs) >= MaxDocs {
				return nil, fmt.Errorf("the source holds more than %d documents or %d MB", MaxDocs, MaxTotalBytes>>20)
			}
			b, err := g.blob(ctx, e.oid)
			if err != nil {
				return nil, err
			}
			if !utf8.Valid(b) {
				return nil, fmt.Errorf("%s is not UTF-8 text", e.path)
			}
			d := parseDoc(e.path, string(b))
			d.Last = last[e.path]
			snap.Docs[d.Name] = d
			snap.names = append(snap.names, d.Name)
		}
	}
	sort.Strings(snap.names)
	if manifest == nil {
		return nil, errors.New("the source has no " + ManifestFile)
	}
	if manifest.mode != "100644" && manifest.mode != "100755" || manifest.size > maxManifest {
		return nil, errors.New(ManifestFile + " is not a plain file of reasonable size")
	}
	b, err := g.blob(ctx, manifest.oid)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &snap.Manifest); err != nil {
		return nil, errors.New(ManifestFile + " is not valid JSON")
	}
	if err := snap.checkManifest(); err != nil {
		return nil, err
	}
	return snap, nil
}

func (s *Snapshot) checkManifest() error {
	m := s.Manifest
	if m.Version != ManifestVersion {
		return fmt.Errorf("%s has version %d; this tmx reads version %d", ManifestFile, m.Version, ManifestVersion)
	}
	if m.InlineChars < 0 || m.InlineChars > MaxInline {
		return fmt.Errorf("%s: inline_chars must be between 0 and %d", ManifestFile, MaxInline)
	}
	check := func(where string, set Set) error {
		for _, p := range set.Inline {
			if _, ok := s.Docs[DocName(p)]; !ok {
				return fmt.Errorf("%s: %s names %s, which is not in the source", ManifestFile, where, peertext.Field(p))
			}
		}
		for _, p := range set.More {
			if strings.HasSuffix(p, "/*") {
				continue // a folder; empty is fine
			}
			if _, ok := s.Docs[DocName(p)]; !ok {
				return fmt.Errorf("%s: %s names %s, which is not in the source", ManifestFile, where, peertext.Field(p))
			}
		}
		return nil
	}
	if err := check("all", m.All); err != nil {
		return err
	}
	for _, group := range []struct {
		name string
		sets map[string]Set
	}{{"profiles", m.Profiles}, {"levels", m.Levels}, {"machines", m.Machines}} {
		for k, set := range group.sets {
			if err := check(group.name+"."+peertext.Field(k), set); err != nil {
				return err
			}
		}
	}
	return nil
}

// HasProfile reports whether the manifest has an entry for the profile slug itself (not only
// for its level): then the source, not the node, says what that profile is.
func (s *Snapshot) HasProfile(slug string) bool {
	_, ok := s.Manifest.Profiles[slug]
	return ok
}

// Select returns the documents of an agent: those that are part of its instructions, in the
// manifest's order, and those listed to read on demand, sorted. A document is in one list only.
func (s *Snapshot) Select(profile, level, machine string) (inline, more []*Doc) {
	sets := []Set{s.Manifest.All}
	if set, ok := s.Manifest.Profiles[profile]; ok {
		sets = append(sets, set)
	} else if set, ok := s.Manifest.Levels[level]; ok {
		sets = append(sets, set)
	}
	for k, set := range s.Manifest.Machines {
		if strings.EqualFold(k, machine) {
			sets = append(sets, set)
		}
	}
	seen := map[string]bool{}
	for _, set := range sets {
		for _, p := range set.Inline {
			if d := s.Docs[DocName(p)]; d != nil && !seen[d.Name] {
				seen[d.Name] = true
				inline = append(inline, d)
			}
		}
	}
	for _, set := range sets {
		for _, p := range set.More {
			if dir, ok := strings.CutSuffix(p, "/*"); ok {
				dir = DocName(dir) + "/"
				for _, n := range s.names {
					if strings.HasPrefix(n, dir) && !seen[n] {
						seen[n] = true
						more = append(more, s.Docs[n])
					}
				}
			} else if d := s.Docs[DocName(p)]; d != nil && !seen[d.Name] {
				seen[d.Name] = true
				more = append(more, d)
			}
		}
	}
	sort.Slice(more, func(i, j int) bool { return more[i].Name < more[j].Name })
	return inline, more
}

var (
	metaLineRx = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_-]{0,39}):\s*(.*)$`)
	headingRx  = regexp.MustCompile(`(?m)^#\s+(.+)$`)
)

// parseDoc reads a document: optional front matter between two "---" lines ("key: value" per
// line, nothing nested), then the text.
func parseDoc(path, text string) *Doc {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	text = strings.TrimPrefix(text, string(rune(0xFEFF)))
	d := &Doc{Name: DocName(path), Path: path, Size: len(text)}
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		if fm, body, ok := strings.Cut(rest, "\n---\n"); ok {
			d.Meta = map[string]string{}
			for _, line := range strings.Split(fm, "\n") {
				if m := metaLineRx.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
					v := strings.TrimSpace(m[2])
					if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
						v = v[1 : len(v)-1]
					}
					d.Meta[strings.ToLower(m[1])] = peertext.Inline(v, 400)
				}
			}
			text = body
		}
	}
	d.Body = strings.TrimSpace(Clean(text))
	d.Title = d.Meta["title"]
	if d.Title == "" {
		d.Title = d.Meta["name"]
	}
	if d.Title == "" {
		if m := headingRx.FindStringSubmatch(d.Body); m != nil {
			d.Title = peertext.Inline(m[1], 120)
		}
	}
	if d.Title == "" {
		d.Title = d.Name
	}
	d.Summary = peertext.Inline(d.Meta["summary"], 200)
	return d
}
