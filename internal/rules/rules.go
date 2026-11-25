// Package rules is the fleet's one source of standing rules and app guides (DESIGN.md "Fleet
// rules"): files in a git repository, read by every node, put into each agent's instructions
// whenever its process starts and readable at any time with the rules_read tool.
//
// A node keeps a bare copy of the source, looks at it every few minutes and takes a new commit
// only when it passes (accept): signed by a pinned key when keys are pinned, a descendant of
// the commit in force, and well-formed. Otherwise the commit in force stays and the head is
// told. Agents are told where their rules come from, at which commit and whether the signature
// was checked, so a rule that was slipped in can be traced and refused.
package rules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/peertext"
)

// Options configures the Service.
type Options struct {
	// Dir holds the node's copy and state (<home>/rules).
	Dir string
	// Settings returns the rules configuration in force (config.Config.EffectiveRules); a
	// Source of "" means this node reads no rules. It is asked at New and at every look at
	// the source; in between, agents' starts go by what it last said.
	Settings func() config.RulesConfig
	// Token returns the node's GitHub token or ""; it is sent to https://github.com only.
	Token func() string
	// Notify is told when a commit was taken or refused (nil = nobody). Called without locks.
	Notify func(Event)
	// Git is the git program; "" = git on PATH.
	Git    string
	Now    func() time.Time
	Logger *slog.Logger
}

// Event is one decision about a commit of the source.
type Event struct {
	Accepted bool `json:"accepted"`
	// First: nothing was in force before.
	First bool   `json:"first,omitempty"`
	Old   string `json:"old,omitempty"`
	New   string `json:"new"`
	Head  Change `json:"head"`
	// Signature is the pinned signer's id; "" = not checked.
	Signature string `json:"signature,omitempty"`
	// Files that differ from the commit in force before (accepted only).
	Files []string `json:"files,omitempty"`
	// Reason the commit was refused.
	Reason string `json:"reason,omitempty"`
	// Dropped: the commit that was in force (Old) is no longer: keys are pinned and it is not
	// signed by one of them. Nothing is in force until a signed commit is taken.
	Dropped bool `json:"dropped,omitempty"`
}

// Rejection is the last commit of the source this node refused.
type Rejection struct {
	Commit string    `json:"commit"`
	Author string    `json:"author,omitempty"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// state is rules/state.json.
type state struct {
	Source     string            `json:"source,omitempty"`
	Branch     string            `json:"branch,omitempty"`
	Accepted   string            `json:"accepted,omitempty"`
	AcceptedAt time.Time         `json:"accepted_at,omitzero"`
	FetchedAt  time.Time         `json:"fetched_at,omitzero"`
	Rejected   *Rejection        `json:"rejected,omitempty"`
	Starts     map[string]string `json:"starts,omitempty"` // agent id -> the commit its instructions were built from
}

// Service is a node's view of the rules source.
type Service struct {
	o    Options
	repo *repo
	log  *slog.Logger

	fetchMu sync.Mutex // one look at the source at a time

	mu      sync.Mutex
	cfg     config.RulesConfig // as of New or the last Refresh: what agents' starts go by
	st      state
	snap    *Snapshot // the commit in force; nil = none
	problem string    // why the last look at the source brought nothing ("" = it worked)
	triedAt time.Time
	kicking bool
	dropped *Event // a commit dropped at New, told at the first look (nobody listens before)

	ctx    context.Context // ends with Close: background looks stop
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New opens the node's copy and puts the commit accepted earlier back in force, without
// touching the network: agents that start with the daemon get their rules at once.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Settings == nil {
		o.Settings = func() config.RulesConfig { return config.RulesConfig{} }
	}
	s := &Service{o: o, repo: &repo{dir: filepath.Join(o.Dir, "repo.git"), exe: o.Git}, log: o.Logger}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	if b, err := os.ReadFile(s.stateFile()); err == nil {
		if json.Unmarshal(b, &s.st) != nil {
			s.st = state{}
		}
	}
	if s.st.Starts == nil {
		s.st.Starts = map[string]string{}
	}
	cfg := o.Settings()
	s.cfg = cfg
	if cfg.Source == "" || s.st.Accepted == "" || s.st.Source != cfg.Source || s.st.Branch != cfg.Branch {
		return s
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	// With keys pinned, a commit that does not verify is never in force, whatever state.json
	// says: the file is a note of this daemon's, not a proof.
	if len(cfg.Signers) > 0 && s.signedBy(ctx, s.st.Accepted, cfg) == "" {
		s.dropped = s.dropLocked()
		return s
	}
	snap, err := load(ctx, s.repo, s.st.Accepted)
	if err != nil {
		s.problem = "the rules taken earlier could not be read back: " + err.Error()
		s.log.Warn("fleet rules: reading back the accepted commit", "commit", short(s.st.Accepted), "err", err)
		return s
	}
	snap.Source, snap.Branch = cfg.Source, cfg.Branch
	snap.Signature = s.signedBy(ctx, s.st.Accepted, cfg)
	s.snap = snap
	return s
}

// notSigned is why a commit is dropped once keys are pinned.
const notSigned = "it is not signed by a key pinned on this node; no fleet rules are in force until a signed commit is taken"

// dropLocked takes the accepted commit out of force and forgets it, so the next signed
// commit of the source is judged on its own and not as a continuation of this one.
func (s *Service) dropLocked() *Event {
	old := s.st.Accepted
	s.snap = nil
	s.st.Accepted, s.st.AcceptedAt = "", time.Time{}
	s.problem = "commit " + short(old) + " was in force and is dropped: " + notSigned
	s.saveLocked()
	s.log.Warn("fleet rules: the commit in force is dropped", "commit", short(old), "reason", notSigned)
	return &Event{Dropped: true, Old: old, New: old, Head: Change{Commit: old}, Reason: notSigned}
}

func (s *Service) stateFile() string { return filepath.Join(s.o.Dir, "state.json") }

// saveLocked writes state.json (whole, then renamed into place).
func (s *Service) saveLocked() {
	b, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(s.o.Dir, 0o700); err != nil {
		s.log.Warn("fleet rules: saving state", "err", err)
		return
	}
	tmp := s.stateFile() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		s.log.Warn("fleet rules: saving state", "err", err)
		return
	}
	if err := os.Rename(tmp, s.stateFile()); err != nil {
		s.log.Warn("fleet rules: saving state", "err", err)
	}
}

// signedBy returns the id of the pinned signer of commit sha, or "" (no keys pinned, or the
// commit does not pass).
func (s *Service) signedBy(ctx context.Context, sha string, cfg config.RulesConfig) string {
	signers, err := ParseSigners(cfg.Signers)
	if err != nil || len(signers) == 0 {
		return ""
	}
	raw, err := s.repo.rawCommit(ctx, sha)
	if err != nil {
		return ""
	}
	id, err := verifyCommit(raw, signers)
	if err != nil {
		return ""
	}
	return id
}

// Run looks at the source now and then every rules.fetch_minutes until ctx ends.
func (s *Service) Run(ctx context.Context) {
	_ = s.Refresh(ctx)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.mu.Lock()
			due := s.o.Now().Sub(s.triedAt) >= time.Duration(max(s.cfg.FetchMinutes, 1))*time.Minute
			s.mu.Unlock()
			if due {
				_ = s.Refresh(ctx)
			}
		}
	}
}

// Close stops the looks at the source that agents' starts began and waits for them.
func (s *Service) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
}

// kickEvery is the least time between two looks started by agents' starts.
const kickEvery = time.Minute

// Kick looks at the source in the background unless that happened within the last minute. An
// agent's start calls it and never waits: the start uses the commit in force.
func (s *Service) Kick() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.kicking || s.ctx.Err() != nil || s.o.Now().Sub(s.triedAt) < kickEvery {
		s.mu.Unlock()
		return
	}
	s.kicking = true
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(s.ctx, 2*fetchTimeout)
		defer cancel()
		_ = s.Refresh(ctx)
		s.mu.Lock()
		s.kicking = false
		s.mu.Unlock()
	}()
}

// RefreshAfter is Refresh unless the source was looked at within the last gap; then it only
// reports what that look found.
func (s *Service) RefreshAfter(ctx context.Context, gap time.Duration) error {
	if s == nil {
		return ErrNotLoaded
	}
	s.mu.Lock()
	recent := s.o.Now().Sub(s.triedAt) < gap
	problem := s.problem
	s.mu.Unlock()
	if !recent {
		return s.Refresh(ctx)
	}
	if problem != "" {
		return errors.New(problem)
	}
	return nil
}

// Refresh looks at the source once and takes its commit when it passes. The error says why
// nothing new is in force; the commit in force before stays.
func (s *Service) Refresh(ctx context.Context) error {
	if s == nil {
		return ErrNotLoaded
	}
	s.fetchMu.Lock()
	defer s.fetchMu.Unlock()
	cfg := s.o.Settings()
	now := s.o.Now()
	s.mu.Lock()
	s.cfg = cfg
	s.triedAt = now
	if cfg.Source == "" {
		s.snap, s.problem = nil, ""
		s.mu.Unlock()
		return nil
	}
	if s.st.Source != cfg.Source || s.st.Branch != cfg.Branch {
		// Another source: its history starts fresh here. What was loaded from the old one is
		// dropped at once; an agent must not be told rules came from a source the owner left.
		s.st.Source, s.st.Branch, s.st.Accepted, s.st.Rejected = cfg.Source, cfg.Branch, "", nil
		s.st.AcceptedAt, s.st.FetchedAt = time.Time{}, time.Time{}
		s.snap = nil
		s.saveLocked()
	}
	accepted := s.st.Accepted
	loaded := s.snap != nil
	dropped := s.dropped
	s.dropped = nil
	s.mu.Unlock()
	// Keys are pinned (now, or since the last look): the commit in force must verify, before
	// anything else and also when the source cannot be reached or the settings are wrong.
	if dropped == nil && accepted != "" && len(cfg.Signers) > 0 && s.signedBy(ctx, accepted, cfg) == "" {
		s.mu.Lock()
		dropped = s.dropLocked()
		s.mu.Unlock()
	}
	if dropped != nil {
		accepted, loaded = "", false
		if s.o.Notify != nil {
			s.o.Notify(*dropped)
		}
	}

	fail := func(err error) error {
		s.mu.Lock()
		s.problem = err.Error()
		s.mu.Unlock()
		s.log.Warn("fleet rules: the source brought nothing new", "err", err)
		return err
	}
	if err := CheckSource(cfg.Source); err != nil {
		return fail(fmt.Errorf("rules.source: %w", err))
	}
	if err := CheckBranch(cfg.Branch); err != nil {
		return fail(fmt.Errorf("rules.branch: %w", err))
	}
	if cfg.Pin != "" && !commitRx.MatchString(cfg.Pin) {
		return fail(errors.New("rules.pin must be a full commit id (40 hex digits)"))
	}
	signers, err := ParseSigners(cfg.Signers)
	if err != nil {
		return fail(err)
	}
	token := ""
	if s.o.Token != nil && githubHTTPS(cfg.Source) {
		token = s.o.Token()
	}
	tip, err := s.repo.fetch(ctx, cfg.Source, cfg.Branch, token)
	if err != nil {
		if errors.Is(err, errNoGit) {
			return fail(err)
		}
		return fail(fmt.Errorf("the source was not reached (%w)", err))
	}
	s.mu.Lock()
	s.st.FetchedAt = now
	s.saveLocked()
	s.mu.Unlock()

	target := tip
	if cfg.Pin != "" {
		if !s.repo.has(ctx, cfg.Pin) {
			return fail(errors.New("the commit this node is held at (rules.pin) is not in the source"))
		}
		target = cfg.Pin
	}
	if target == accepted && loaded {
		s.mu.Lock()
		s.problem = ""
		// The keys may have changed since: say what holds now.
		sig := s.signedBy(ctx, target, cfg)
		if s.snap != nil && s.snap.Signature != sig {
			cp := *s.snap
			cp.Signature = sig
			s.snap = &cp
		}
		s.mu.Unlock()
		return nil
	}
	return s.consider(ctx, cfg, signers, accepted, target)
}

// consider decides about commit target: every check must pass, or the commit in force stays.
func (s *Service) consider(ctx context.Context, cfg config.RulesConfig, signers []signer, accepted, target string) error {
	head, err := s.repo.commit(ctx, target)
	if err != nil {
		return s.reject(target, Change{Commit: target}, "the commit could not be read: "+err.Error())
	}
	signature := ""
	if len(signers) > 0 {
		raw, err := s.repo.rawCommit(ctx, target)
		if err != nil {
			return s.reject(target, head, "the commit could not be read: "+err.Error())
		}
		if signature, err = verifyCommit(raw, signers); err != nil {
			return s.reject(target, head, err.Error())
		}
	}
	// A held commit may lie before the one in force (that is what holding is for); the branch
	// tip may not: a rewound or rewritten history is refused.
	if accepted != "" && cfg.Pin == "" && s.repo.has(ctx, accepted) {
		ok, err := s.repo.isAncestor(ctx, accepted, target)
		if err != nil {
			return s.reject(target, head, "its history could not be checked: "+err.Error())
		}
		if !ok {
			return s.reject(target, head, "it does not continue the commit in force ("+short(accepted)+"): the source's history was rewound or rewritten")
		}
	}
	snap, err := load(ctx, s.repo, target)
	if err != nil {
		return s.reject(target, head, err.Error())
	}
	snap.Source, snap.Branch, snap.Signature = cfg.Source, cfg.Branch, signature
	var files []string
	if accepted != "" && s.repo.has(ctx, accepted) {
		files, _ = s.repo.changed(ctx, accepted, target)
	}
	s.mu.Lock()
	s.snap, s.problem = snap, ""
	s.st.Accepted, s.st.AcceptedAt, s.st.Rejected = target, s.o.Now(), nil
	s.saveLocked()
	s.mu.Unlock()
	s.log.Info("fleet rules: commit taken", "commit", short(target), "signature", signature, "files", len(files))
	if s.o.Notify != nil {
		s.o.Notify(Event{Accepted: true, First: accepted == "", Old: accepted, New: target, Head: head, Signature: signature, Files: files})
	}
	return nil
}

// reject keeps the commit in force and remembers why target was refused; the head hears of
// each refused commit once.
func (s *Service) reject(target string, head Change, reason string) error {
	s.mu.Lock()
	again := s.st.Rejected != nil && s.st.Rejected.Commit == target && s.st.Rejected.Reason == reason
	s.st.Rejected = &Rejection{Commit: target, Author: head.Author, Reason: reason, At: s.o.Now()}
	s.problem = "commit " + short(target) + " of the source was not taken: " + reason
	old := s.st.Accepted
	s.saveLocked()
	s.mu.Unlock()
	s.log.Warn("fleet rules: commit refused", "commit", short(target), "reason", reason)
	if !again && s.o.Notify != nil {
		s.o.Notify(Event{Old: old, New: target, Head: head, Reason: reason})
	}
	return errors.New(reason)
}

// viewLocked is the snapshot in force with what the frame needs, or false.
func (s *Service) viewLocked() (view, bool) {
	if s.snap == nil {
		return view{}, false
	}
	cfg := s.cfg
	if cfg.Source == "" {
		return view{}, false
	}
	return view{snap: s.snap, fetched: s.st.FetchedAt, pinned: len(cfg.Signers) > 0, held: cfg.Pin != ""}, true
}

// Loaded reports whether rules are in force on this node.
func (s *Service) Loaded() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.viewLocked()
	return ok
}

// Commit is the commit in force, or "".
func (s *Service) Commit() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.viewLocked(); ok {
		return v.snap.Head.Commit
	}
	return ""
}

// Prompt builds the rules part of an agent's instructions from the commit in force; its Text
// is "" when no rules are loaded.
func (s *Service) Prompt(req PromptRequest) Prompt {
	if s == nil {
		return Prompt{}
	}
	s.mu.Lock()
	v, ok := s.viewLocked()
	s.mu.Unlock()
	if !ok {
		return Prompt{}
	}
	return v.prompt(req)
}

// Full returns every document of an agent in full, or "" when no rules are loaded.
func (s *Service) Full(req PromptRequest) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	v, ok := s.viewLocked()
	s.mu.Unlock()
	if !ok {
		return ""
	}
	return v.full(req)
}

// WriteFull writes every document of an agent, in full and framed, to path (for a harness
// without tmx tools). It reports whether rules were loaded and the file was written.
func (s *Service) WriteFull(path string, req PromptRequest) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	v, ok := s.viewLocked()
	s.mu.Unlock()
	if !ok {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false
	}
	return os.WriteFile(path, []byte(v.full(req)), 0o600) == nil
}

// NoteStart records the commit an agent's instructions were built from.
func (s *Service) NoteStart(agentID, commit string) {
	if s == nil || agentID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.Starts[agentID] == commit {
		return
	}
	if commit == "" {
		delete(s.st.Starts, agentID)
	} else {
		s.st.Starts[agentID] = commit
	}
	s.saveLocked()
}

// StartCommit is the commit an agent's instructions were built from, or "".
func (s *Service) StartCommit(agentID string) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Starts[agentID]
}

// Forget drops what is kept about an agent that is gone.
func (s *Service) Forget(agentID string) {
	s.NoteStart(agentID, "")
}

// ErrNotLoaded is returned when no rules are in force on this node.
var ErrNotLoaded = errors.New("no fleet rules are loaded on this node")

// logEntries is how many commits rules_read shows for a document.
const logEntries = 10

// Read answers one rules_read call.
func (s *Service) Read(ctx context.Context, req ReadRequest) (string, error) {
	if s == nil {
		return "", ErrNotLoaded
	}
	s.mu.Lock()
	v, ok := s.viewLocked()
	problem := s.problem
	s.mu.Unlock()
	if !ok {
		if problem != "" {
			return "", fmt.Errorf("%w: %s", ErrNotLoaded, problem)
		}
		return "", ErrNotLoaded
	}
	if req.Name == "" {
		var changed []string
		known := false
		if req.Start != "" && req.Start != v.snap.Head.Commit && s.repo.has(ctx, req.Start) {
			if files, err := s.repo.changed(ctx, req.Start, v.snap.Head.Commit); err == nil {
				changed, known = files, true
			}
		}
		out := v.overview(req, changed, known)
		if problem != "" {
			out += "\nNote from tmx: " + peertext.Inline(problem, 300)
		}
		return out, nil
	}
	d := v.snap.Docs[DocName(req.Name)]
	if d == nil {
		return "", fmt.Errorf("there is no document %q in the fleet rules; rules_read without a name lists them", peertext.Field(req.Name))
	}
	var log []Change
	if req.Log {
		log, _ = s.repo.fileLog(ctx, v.snap.Head.Commit, d.Path, logEntries)
	}
	return v.document(d, req, log), nil
}

// Changed lists the documents that differ between commit old and the commit in force; ok is
// false when that is not known (old is not in the node's copy).
func (s *Service) Changed(ctx context.Context, old string) (files []string, ok bool) {
	if s == nil {
		return nil, false
	}
	cur := s.Commit()
	if cur == "" || old == "" || !s.repo.has(ctx, old) {
		return nil, false
	}
	if old == cur {
		return nil, true
	}
	files, err := s.repo.changed(ctx, old, cur)
	return files, err == nil
}

// SignerView is a pinned signer as shown to the owner.
type SignerView struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
}

// Status is the node's view of the rules source, for the owner's page and the API.
type Status struct {
	// Enabled: this node has a rules source.
	Enabled bool         `json:"enabled"`
	Source  string       `json:"source,omitempty"`
	Branch  string       `json:"branch,omitempty"`
	Pin     string       `json:"pin,omitempty"`
	Signers []SignerView `json:"signers"`
	// Loaded: rules are in force.
	Loaded bool    `json:"loaded"`
	Head   *Change `json:"head,omitempty"`
	// Signature is the id of the pinned signer of the commit in force; "" = not checked.
	Signature  string     `json:"signature,omitempty"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
	FetchedAt  *time.Time `json:"fetched_at,omitempty"`
	// Problem is why the last look at the source brought nothing new ("" = it worked).
	Problem  string     `json:"problem,omitempty"`
	Rejected *Rejection `json:"rejected,omitempty"`
	Manifest *Manifest  `json:"manifest,omitempty"`
	Docs     []Doc      `json:"docs"`
}

// Status describes the source and the commit in force.
func (s *Service) Status() Status {
	st := Status{Signers: []SignerView{}, Docs: []Doc{}}
	if s == nil {
		return st
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.cfg
	st.Enabled = cfg.Source != ""
	st.Source, st.Branch, st.Pin = cfg.Source, cfg.Branch, cfg.Pin
	for _, sg := range cfg.Signers {
		st.Signers = append(st.Signers, SignerView{ID: sg.ID, Fingerprint: SignerFingerprint(sg)})
	}
	st.Problem = s.problem
	if s.st.Rejected != nil {
		r := *s.st.Rejected
		st.Rejected = &r
	}
	if !s.st.FetchedAt.IsZero() {
		t := s.st.FetchedAt
		st.FetchedAt = &t
	}
	v, ok := s.viewLocked()
	if !ok {
		return st
	}
	st.Loaded = true
	head := v.snap.Head
	st.Head, st.Signature = &head, v.snap.Signature
	if !s.st.AcceptedAt.IsZero() {
		t := s.st.AcceptedAt
		st.AcceptedAt = &t
	}
	m := v.snap.Manifest
	st.Manifest = &m
	for _, n := range v.snap.names {
		st.Docs = append(st.Docs, *v.snap.Docs[n])
	}
	return st
}

// Doc returns one document of the commit in force with its text.
func (s *Service) Doc(name string) (Doc, string, bool) {
	if s == nil {
		return Doc{}, "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.viewLocked()
	if !ok {
		return Doc{}, "", false
	}
	d := v.snap.Docs[DocName(name)]
	if d == nil {
		return Doc{}, "", false
	}
	return *d, d.Body, true
}

// DocsUnder lists the documents of one folder ("apps") of the commit in force, sorted.
func (s *Service) DocsUnder(dir string) []Doc {
	var out []Doc
	if s == nil {
		return out
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.viewLocked()
	if !ok {
		return out
	}
	prefix := strings.Trim(dir, "/") + "/"
	for _, n := range v.snap.names {
		if strings.HasPrefix(n, prefix) {
			out = append(out, *v.snap.Docs[n])
		}
	}
	return out
}
