package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// A harness may have several named accounts (two subscriptions of one provider). Each account
// other than "default" has its own private home under tmx's state, which the harness is pointed
// at by its own variable (CLAUDE_CONFIG_DIR, CODEX_HOME, GEMINI_CLI_HOME; agy has none, so
// HOME/USERPROFILE):
// its sign-in, credentials and sessions stay there, never in a project or in git, and tmx never
// reads them. "default" is the account tmx used before there were several: the harness's usual
// home (the daemon's CLAUDE_CONFIG_DIR, CODEX_HOME or ~/.codex, ~/.gemini), so nothing moves.

// DefaultName is the account every harness has, which needs no folder of its own.
const DefaultName = "default"

// MaxPerHarness bounds the accounts of one harness.
const MaxPerHarness = 8

var nameRx = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// Errors of the account list.
var (
	ErrBadName   = errors.New("an account name is 1-32 lower-case letters, digits or '-', starting with a letter or digit")
	ErrExists    = errors.New("that account already exists")
	ErrNoAccount = errors.New("no such account")
	ErrTooMany   = fmt.Errorf("at most %d accounts per harness", MaxPerHarness)
	// ErrIsDefault is kept for callers that still name it; the default account is hidden now
	// (Remove), not refused.
	ErrIsDefault = errors.New("the default account cannot be removed")
)

// ValidName reports whether name may name an account (it is also a folder name).
func ValidName(name string) bool { return nameRx.MatchString(name) }

// profileFile is profiles.json.
type profileFile struct {
	// Names lists each harness's accounts other than "default", in the order added.
	Names map[model.Harness][]string `json:"names"`
	// Defaults is each harness's account for an agent launched without one ("" = "default").
	Defaults map[model.Harness]string `json:"defaults"`
	// Hidden lists the harnesses whose "default" account the owner took off the list (Remove
	// on its card). Only the list changes: the harness's own sign-in stays, and agents that
	// use the default account keep using it. Adding "default" again shows it.
	Hidden []model.Harness `json:"hidden,omitempty"`
}

// Profiles is the list of accounts per harness, kept in <root>/profiles.json; each account's
// home is <root>/homes/<harness>/<name>.
type Profiles struct {
	root        string
	defaultHome string
	mu          sync.Mutex
	f           profileFile
}

// OpenProfiles reads root's list; a missing file is the single "default" account per harness.
func OpenProfiles(root string) (*Profiles, error) {
	p := &Profiles{root: root, f: profileFile{Names: map[model.Harness][]string{}, Defaults: map[model.Harness]string{}}}
	b, err := os.ReadFile(p.file())
	switch {
	case errors.Is(err, os.ErrNotExist):
		return p, nil
	case err != nil:
		return nil, err
	}
	var f profileFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("accounts: %s: %w", p.file(), err)
	}
	for h, names := range f.Names {
		if !Known(string(h)) {
			continue
		}
		for _, n := range names {
			if ValidName(n) && n != DefaultName && !slices.Contains(p.f.Names[h], n) && len(p.f.Names[h]) < MaxPerHarness-1 {
				p.f.Names[h] = append(p.f.Names[h], n)
			}
		}
	}
	for h, n := range f.Defaults {
		if Known(string(h)) && slices.Contains(p.f.Names[h], n) {
			p.f.Defaults[h] = n
		}
	}
	for _, h := range f.Hidden {
		if Known(string(h)) && !slices.Contains(p.f.Hidden, h) {
			p.f.Hidden = append(p.f.Hidden, h)
		}
	}
	return p, nil
}

func (p *Profiles) file() string { return filepath.Join(p.root, "profiles.json") }

// Names is h's listed accounts: "default" first (unless the owner hid it), then the others in
// the order added.
func (p *Profiles) Names(h model.Harness) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if slices.Contains(p.f.Hidden, h) {
		return slices.Clone(p.f.Names[h])
	}
	return append([]string{DefaultName}, p.f.Names[h]...)
}

// HiddenDefaults are the harnesses whose default account is off the list.
func (p *Profiles) HiddenDefaults() []model.Harness {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.f.Hidden)
}

// setHiddenLocked hides or shows h's default account on the list and saves.
func (p *Profiles) setHiddenLocked(h model.Harness, hidden bool) error {
	old := slices.Clone(p.f.Hidden)
	if hidden {
		p.f.Hidden = append(p.f.Hidden, h)
	} else {
		p.f.Hidden = slices.DeleteFunc(p.f.Hidden, func(x model.Harness) bool { return x == h })
	}
	if err := p.saveLocked(); err != nil {
		p.f.Hidden = old
		return err
	}
	return nil
}

// Has reports whether h has the account name ("" = the default).
func (p *Profiles) Has(h model.Harness, name string) bool {
	if name == "" || name == DefaultName {
		return Known(string(h))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Contains(p.f.Names[h], name)
}

// Default is the account an agent of h uses when its launch names none.
func (p *Profiles) Default(h model.Harness) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n := p.f.Defaults[h]; n != "" {
		return n
	}
	return DefaultName
}

// Resolve is the account an agent of h asking for name uses: name, or h's default for "".
func (p *Profiles) Resolve(h model.Harness, name string) (string, error) {
	if name == "" {
		return p.Default(h), nil
	}
	if !p.Has(h, name) {
		return "", fmt.Errorf("%s account %q: %w", h, name, ErrNoAccount)
	}
	return name, nil
}

// Add adds the account name to h and makes its private home.
func (p *Profiles) Add(h model.Harness, name string) error {
	if !Known(string(h)) {
		return ErrUnknown
	}
	if !ValidName(name) {
		return ErrBadName
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if name == DefaultName {
		if !slices.Contains(p.f.Hidden, h) {
			return ErrExists
		}
		return p.setHiddenLocked(h, false) // the default account is listed again; nothing else changed meanwhile
	}
	if slices.Contains(p.f.Names[h], name) {
		return ErrExists
	}
	if len(p.f.Names[h]) >= MaxPerHarness-1 {
		return ErrTooMany
	}
	// Private like tmx's secrets (an ACL on Windows, where 0700 means nothing): it will hold a
	// sign-in. A Claude account's folder also skips Claude's first-run questions.
	if err := config.MkdirSecret(p.root); err != nil {
		return err
	}
	mk := config.MkdirSecret
	if h == Claude {
		mk = func(dir string) error { _, err := config.EnsureClaudeDir(dir); return err }
	}
	if err := mk(p.home(h, name)); err != nil {
		return err
	}
	p.f.Names[h] = append(p.f.Names[h], name)
	if err := p.saveLocked(); err != nil {
		p.f.Names[h] = slices.DeleteFunc(p.f.Names[h], func(n string) bool { return n == name })
		return err
	}
	return nil
}

// Remove takes the account name off h's list. Its home is left in place (the owner signs out
// first; tmx deletes no credentials): adding the name again finds it signed in. The default
// account is only hidden: it is the harness's own sign-in, which tmx neither owns nor ends, and
// launches without an account keep using it.
func (p *Profiles) Remove(h model.Harness, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if name == DefaultName || name == "" {
		if !Known(string(h)) {
			return ErrUnknown
		}
		if slices.Contains(p.f.Hidden, h) {
			return ErrNoAccount
		}
		return p.setHiddenLocked(h, true)
	}
	i := slices.Index(p.f.Names[h], name)
	if i < 0 {
		return ErrNoAccount
	}
	old, oldDef := slices.Clone(p.f.Names[h]), p.f.Defaults[h]
	p.f.Names[h] = slices.Delete(p.f.Names[h], i, i+1)
	if oldDef == name {
		delete(p.f.Defaults, h)
	}
	if err := p.saveLocked(); err != nil {
		p.f.Names[h] = old
		if oldDef != "" {
			p.f.Defaults[h] = oldDef
		}
		return err
	}
	return nil
}

// SetDefault makes name h's account for launches that name none.
func (p *Profiles) SetDefault(h model.Harness, name string) error {
	if !p.Has(h, name) {
		return ErrNoAccount
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	old, had := p.f.Defaults[h]
	if name == DefaultName || name == "" {
		delete(p.f.Defaults, h)
	} else {
		p.f.Defaults[h] = name
	}
	if err := p.saveLocked(); err != nil {
		if had {
			p.f.Defaults[h] = old
		} else {
			delete(p.f.Defaults, h)
		}
		return err
	}
	return nil
}

// Home is the account's private home. The default uses the harness's usual home
// unless IsolateDefaults configured node-local storage.
func (p *Profiles) Home(h model.Harness, name string) string {
	if (name == "" || name == DefaultName) && p.defaultHome != "" {
		if h == Claude {
			return filepath.Join(p.defaultHome, "claude-config")
		}
		return p.home(h, DefaultName)
	}
	if name == "" || name == DefaultName || !ValidName(name) {
		return ""
	}
	return p.home(h, name)
}

func (p *Profiles) home(h model.Harness, name string) string {
	return filepath.Join(p.root, "homes", string(h), name)
}

// Env is what points h at its account name's home: nothing for "default".
func (p *Profiles) Env(h model.Harness, name string) []string {
	return HomeEnv(h, p.Home(h, name))
}

// HomeEnv is the environment that makes h keep its state in home ("" = nothing).
func HomeEnv(h model.Harness, home string) []string {
	if home == "" {
		return nil
	}
	switch h {
	case Claude:
		return []string{"CLAUDE_CONFIG_DIR=" + home}
	case Codex:
		return []string{"CODEX_HOME=" + home}
	case Antigravity:
		// agy has no variable of its own for ~/.gemini: its home folder is moved instead.
		return []string{"HOME=" + home, "USERPROFILE=" + home, "APPDATA=" + filepath.Join(home, "AppData", "Roaming"), "LOCALAPPDATA=" + filepath.Join(home, "AppData", "Local"), "XDG_CONFIG_HOME=" + filepath.Join(home, ".config")}
	case Gemini:
		// GEMINI_CLI_HOME moves ~/.gemini; the sign-in's token would still go to the OS
		// keychain under one name for every home, so it is kept in that folder's file (gemini.go).
		return []string{"GEMINI_CLI_HOME=" + home, "GEMINI_FORCE_FILE_STORAGE=true"}
	}
	return nil
}

func (p *Profiles) saveLocked() error {
	b, err := json.MarshalIndent(p.f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.root, 0o700); err != nil {
		return err
	}
	tmp := p.file() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.file())
}

// IsolateDefaults must be called before sharing Profiles with services or agents.
func (p *Profiles) IsolateDefaults(nodeHome string) { p.defaultHome = nodeHome }
