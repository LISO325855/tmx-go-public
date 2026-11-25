// Package rulestest makes throwaway rules sources for tests: a git repository in a temporary
// folder, with plain or signed commits. Nothing here touches the network.
package rulestest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
	"github.com/blackcode-ch/tmx-go/internal/rules"
)

// Repo is a rules source in a temporary folder.
type Repo struct {
	Dir string
	t   testing.TB
	n   int
}

// Manifest is a small manifest: core for everyone, one file per level, apps and pages on demand.
const Manifest = `{
  "version": 1,
  "all": {"inline": ["rules/core.md"], "more": ["apps/*", "pages/operations.md"]},
  "profiles": {"head": {"inline": ["rules/head.md"]}},
  "levels": {"worker": {"inline": ["rules/worker.md"]}}
}`

// Files is a small, valid source.
func Files() map[string]string {
	return map[string]string{
		"manifest.json":       Manifest,
		"rules/core.md":       "# Core\n\nReport what you verified, not what you assume.\n",
		"rules/head.md":       "# Head\n\nYou are \"{{name}}\", the head. Keep the owner informed.\n",
		"rules/worker.md":     "# Worker\n\nStay inside your project.\n",
		"apps/tickets.md":     "---\nname: Ticket app\nsummary: Issues and tasks of the fleet.\nstate: works\n---\nOpen a ticket with the tickets tool.\n",
		"pages/operations.md": "# Operations\n\nHow the fleet runs day to day.\n",
	}
}

// New makes an empty source; it skips the test on a machine without git.
func New(t testing.TB) *Repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	r := &Repo{Dir: t.TempDir(), t: t}
	r.Git("init", "--quiet", "--initial-branch=main")
	return r
}

// NewWith makes a source with one commit holding files.
func NewWith(t testing.TB, files map[string]string) (*Repo, string) {
	r := New(t)
	return r, r.Commit("first rules", files)
}

func (r *Repo) env() []string {
	r.n++
	date := fmt.Sprintf("2026-01-%02dT10:00:00+00:00", 1+r.n%28)
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=rules-editor", "GIT_AUTHOR_EMAIL=editor@example.invalid",
		"GIT_COMMITTER_NAME=rules-editor", "GIT_COMMITTER_EMAIL=editor@example.invalid",
		"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
}

// Git runs git in the source and returns its trimmed output.
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	return r.gitIn(nil, args...)
}

func (r *Repo) gitIn(stdin []byte, args ...string) string {
	r.t.Helper()
	cmd := hiddenexec.Command("git", append([]string{"-c", "core.autocrlf=false", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir, cmd.Env = r.Dir, r.env()
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// write puts files into the working folder; a file with the text "" is removed.
func (r *Repo) write(files map[string]string) {
	r.t.Helper()
	for name, text := range files {
		p := filepath.Join(r.Dir, filepath.FromSlash(name))
		if text == "" {
			_ = os.Remove(p)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
	r.Git("add", "-A")
}

// Commit changes files and commits, unsigned; it returns the commit.
func (r *Repo) Commit(subject string, files map[string]string) string {
	r.t.Helper()
	r.write(files)
	r.Git("commit", "--quiet", "--allow-empty", "-m", subject)
	return r.Head()
}

// CommitSigned changes files and makes a commit signed with key, as `git commit -S` with an
// SSH key does; it returns the commit.
func (r *Repo) CommitSigned(key ed25519.PrivateKey, subject string, files map[string]string) string {
	r.t.Helper()
	r.write(files)
	tree := r.Git("write-tree")
	var raw bytes.Buffer
	fmt.Fprintf(&raw, "tree %s\n", tree)
	if out, err := r.tryGit("rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		fmt.Fprintf(&raw, "parent %s\n", out)
	}
	r.n++
	stamp := fmt.Sprintf("%d +0000", 1767261600+r.n*3600)
	fmt.Fprintf(&raw, "author rules-editor <editor@example.invalid> %s\ncommitter rules-editor <editor@example.invalid> %s\n\n%s\n", stamp, stamp, subject)
	signed, err := rules.SignCommit(raw.Bytes(), key)
	if err != nil {
		r.t.Fatal(err)
	}
	sha := r.gitIn(signed, "hash-object", "-t", "commit", "-w", "--stdin")
	r.Git("update-ref", "refs/heads/main", sha)
	return sha
}

func (r *Repo) tryGit(args ...string) (string, error) {
	cmd := hiddenexec.Command("git", args...)
	cmd.Dir, cmd.Env = r.Dir, r.env()
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// Head is the tip of main.
func (r *Repo) Head() string {
	r.t.Helper()
	return r.Git("rev-parse", "refs/heads/main")
}

// Rewind moves main back to commit sha (a rewritten history).
func (r *Repo) Rewind(sha string) {
	r.t.Helper()
	r.Git("update-ref", "refs/heads/main", sha)
	r.Git("reset", "--quiet", "--hard", sha)
}

// Key makes a signing key and the signer entry that pins it under id.
func Key(t testing.TB, id string) (ed25519.PrivateKey, config.RulesSigner) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv, config.RulesSigner{ID: id, Key: base64.StdEncoding.EncodeToString(pub)}
}

// Settings is a rules configuration that follows r's main branch.
func (r *Repo) Settings(signers ...config.RulesSigner) config.RulesConfig {
	return config.RulesConfig{Source: r.Dir, Branch: "main", FetchMinutes: 10, Signers: signers}
}
