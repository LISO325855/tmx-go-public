package rules

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
)

// The node's copy of the source is a bare repository: there is no working tree, files are read
// as blobs of one commit. So nothing is checked out (no line-ending conversion, no half-written
// folder) and no hook of the source ever runs. git itself is the program on PATH; the user's
// own git settings are not read (no rewritten addresses), and nothing may prompt.

var (
	commitRx = regexp.MustCompile(`^[0-9a-f]{40}$`)
	branchRx = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$`)
)

const (
	gitTimeout   = 20 * time.Second
	fetchTimeout = 60 * time.Second
	// tipRef is where a fetch leaves the source's branch tip.
	tipRef = "refs/tmx/tip"
)

var errNoGit = errors.New("git is not installed on this machine")

// CheckSource says why src cannot be a rules source, or nil. An address never carries a
// password or token (they would be shown to every agent in the header), and never a transport
// that runs a program.
func CheckSource(src string) error {
	switch {
	case src == "":
		return errors.New("the source is empty")
	case strings.HasPrefix(src, "-") || strings.ContainsAny(src, "\r\n\x00"):
		return errors.New("that is not a repository address")
	case strings.Contains(src, "::"):
		return errors.New("that kind of address is not allowed")
	}
	if strings.Contains(src, "://") {
		u, err := url.Parse(src)
		if err != nil {
			return errors.New("that is not a repository address")
		}
		if u.Scheme != "https" && u.Scheme != "ssh" {
			return errors.New("the source must be an https or ssh address, or a folder on this machine")
		}
		if _, has := u.User.Password(); has || (u.Scheme == "https" && u.User != nil) {
			return errors.New("the address must not contain a name or password; the node uses its own GitHub sign-in")
		}
		return nil
	}
	if filepath.IsAbs(src) || regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[A-Za-z0-9._/~-]+$`).MatchString(src) {
		return nil // a folder, or git's short ssh form (git@host:owner/name)
	}
	return errors.New("the source must be an https or ssh address, or a full folder path")
}

// IsCommit reports whether s is a full commit id (40 lowercase hex digits).
func IsCommit(s string) bool { return commitRx.MatchString(s) }

// CheckBranch says whether b can be a branch name here.
func CheckBranch(b string) error {
	if !branchRx.MatchString(b) || strings.Contains(b, "..") || strings.HasSuffix(b, "/") || strings.HasSuffix(b, ".lock") {
		return errors.New("that is not a branch name")
	}
	return nil
}

// githubHTTPS reports whether src is an https address of github.com: only there the node's
// GitHub token is sent.
func githubHTTPS(src string) bool {
	u, err := url.Parse(src)
	return err == nil && u.Scheme == "https" && strings.EqualFold(u.Host, "github.com")
}

type repo struct {
	dir string
	exe string // "" = git on PATH
}

func (g *repo) git() (string, error) {
	if g.exe != "" {
		return g.exe, nil
	}
	p, err := exec.LookPath("git")
	if err != nil {
		return "", errNoGit
	}
	return p, nil
}

// run runs git in the bare repository and returns its standard output. token, when set, is
// sent to github.com only, and travels in the environment, never on the command line.
func (g *repo) run(ctx context.Context, timeout time.Duration, token string, args ...string) ([]byte, error) {
	exe, err := g.git()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := hiddenexec.CommandContext(ctx, exe, append([]string{"--git-dir", g.dir}, args...)...)
	env := append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_ASKPASS=", "SSH_ASKPASS=",
		// The user's settings are left out; the system's stay, since Git for Windows keeps its
		// certificate store setting there and https would fail without it.
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"LC_ALL=C", "GIT_OPTIONAL_LOCKS=0")
	keys := [][2]string{
		{"protocol.ext.allow", "never"},
		// No hook ever runs, whatever is in the copy's hooks folder: a fetch carries the node's token.
		{"core.hooksPath", os.DevNull},
		{"credential.interactive", "false"},
		{"core.quotePath", "true"},
		{"gc.auto", "0"},
	}
	if token != "" {
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
		keys = append(keys, [2]string{"http.https://github.com/.extraheader", "Authorization: Basic " + basic})
	}
	env = append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(len(keys)))
	for i, kv := range keys {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, kv[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, kv[1]))
	}
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("git took too long")
		}
		return out, &gitError{err: err, stderr: firstLine(stderr.String())}
	}
	return out, nil
}

// gitError keeps git's exit code apart from what it printed: the text can name the address,
// so it goes to the log and the owner's page, never into an agent's instructions.
type gitError struct {
	err    error
	stderr string
}

func (e *gitError) Error() string {
	if e.stderr != "" {
		return "git: " + e.stderr
	}
	return "git: " + e.err.Error()
}

func exitCode(err error) int {
	var ge *gitError
	var ee *exec.ExitError
	if errors.As(err, &ge) && errors.As(ge.err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// ensure creates the bare repository when it is not there yet.
func (g *repo) ensure(ctx context.Context) error {
	if st, err := os.Stat(filepath.Join(g.dir, "HEAD")); err == nil && !st.IsDir() {
		return nil
	}
	if err := os.MkdirAll(g.dir, 0o700); err != nil {
		return err
	}
	_, err := g.run(ctx, gitTimeout, "", "init", "--quiet", "--bare")
	return err
}

// fetch brings the tip of the source's branch into tipRef and returns it.
func (g *repo) fetch(ctx context.Context, source, branch, token string) (string, error) {
	if err := g.ensure(ctx); err != nil {
		return "", err
	}
	if !githubHTTPS(source) {
		token = ""
	}
	if _, err := g.run(ctx, fetchTimeout, token, "fetch", "--quiet", "--no-tags", "--force", "--no-write-fetch-head", "--no-recurse-submodules",
		"--end-of-options", source, "+refs/heads/"+branch+":"+tipRef); err != nil {
		return "", err
	}
	out, err := g.run(ctx, gitTimeout, "", "rev-parse", "--verify", "--quiet", tipRef+"^{commit}")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(out))
	if !commitRx.MatchString(sha) {
		return "", errors.New("the source's branch does not name a commit")
	}
	return sha, nil
}

// has reports whether the repository holds commit sha.
func (g *repo) has(ctx context.Context, sha string) bool {
	if !commitRx.MatchString(sha) {
		return false
	}
	_, err := g.run(ctx, gitTimeout, "", "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// isAncestor reports whether old is new or one of its ancestors.
func (g *repo) isAncestor(ctx context.Context, old, new string) (bool, error) {
	_, err := g.run(ctx, gitTimeout, "", "merge-base", "--is-ancestor", old, new)
	if err == nil {
		return true, nil
	}
	if exitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

// rawCommit is the commit object as stored: what a signature covers.
func (g *repo) rawCommit(ctx context.Context, sha string) ([]byte, error) {
	return g.run(ctx, gitTimeout, "", "cat-file", "commit", sha)
}

// treeEntry is one file of a commit.
type treeEntry struct {
	mode, oid, path string
	size            int64
}

// tree lists every file of the commit with its size.
func (g *repo) tree(ctx context.Context, sha string) ([]treeEntry, error) {
	out, err := g.run(ctx, gitTimeout, "", "ls-tree", "-r", "-l", "-z", sha)
	if err != nil {
		return nil, err
	}
	var es []treeEntry
	for _, rec := range strings.Split(string(out), "\x00") {
		if rec == "" {
			continue
		}
		meta, path, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 4 {
			return nil, errors.New("git listed the commit's files in a form tmx does not know")
		}
		e := treeEntry{mode: f[0], oid: f[2], path: path}
		if f[1] == "blob" {
			if e.size, err = strconv.ParseInt(f[3], 10, 64); err != nil {
				return nil, errors.New("git listed the commit's files in a form tmx does not know")
			}
		}
		es = append(es, e)
	}
	return es, nil
}

func (g *repo) blob(ctx context.Context, oid string) ([]byte, error) {
	return g.run(ctx, gitTimeout, "", "cat-file", "blob", oid)
}

const logFormat = "%H%x1f%an%x1f%cI%x1f%s"

func parseChange(line string) (Change, bool) {
	f := strings.Split(strings.TrimSpace(line), "\x1f")
	if len(f) != 4 || !commitRx.MatchString(f[0]) {
		return Change{}, false
	}
	t, _ := time.Parse(time.RFC3339, f[2])
	return Change{Commit: f[0], Author: f[1], Time: t, Subject: f[3]}, true
}

// commit describes one commit.
func (g *repo) commit(ctx context.Context, sha string) (Change, error) {
	out, err := g.run(ctx, gitTimeout, "", "log", "-1", "--format="+logFormat, sha, "--")
	if err != nil {
		return Change{}, err
	}
	c, ok := parseChange(string(out))
	if !ok {
		return Change{}, errors.New("git described the commit in a form tmx does not know")
	}
	return c, nil
}

// maxHistory is how far back the last change of each file is looked for.
const maxHistory = 5000

// lastChanges maps each file to the newest commit that changed it, as of sha.
func (g *repo) lastChanges(ctx context.Context, sha string) (map[string]Change, error) {
	out, err := g.run(ctx, gitTimeout, "", "log", "-n", strconv.Itoa(maxHistory), "--format=%x1e"+logFormat, "--name-only", sha, "--")
	if err != nil {
		return nil, err
	}
	last := map[string]Change{}
	for _, rec := range strings.Split(string(out), "\x1e") {
		lines := strings.Split(rec, "\n")
		c, ok := parseChange(lines[0])
		if !ok {
			continue
		}
		for _, p := range lines[1:] {
			if p = strings.TrimSpace(p); p != "" {
				if _, seen := last[p]; !seen {
					last[p] = c
				}
			}
		}
	}
	return last, nil
}

// fileLog is the newest n commits that changed path, as of sha.
func (g *repo) fileLog(ctx context.Context, sha, path string, n int) ([]Change, error) {
	out, err := g.run(ctx, gitTimeout, "", "log", "-n", strconv.Itoa(n), "--format="+logFormat, sha, "--", path)
	if err != nil {
		return nil, err
	}
	var cs []Change
	for _, line := range strings.Split(string(out), "\n") {
		if c, ok := parseChange(line); ok {
			cs = append(cs, c)
		}
	}
	return cs, nil
}

// changed lists the files that differ between two commits.
func (g *repo) changed(ctx context.Context, old, new string) ([]string, error) {
	out, err := g.run(ctx, gitTimeout, "", "diff", "--name-only", "--no-renames", old, new, "--")
	if err != nil {
		return nil, err
	}
	var ps []string
	for _, p := range strings.Split(string(out), "\n") {
		if p = strings.TrimSpace(p); p != "" {
			ps = append(ps, p)
		}
	}
	return ps, nil
}
