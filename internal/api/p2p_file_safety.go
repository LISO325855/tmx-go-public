package api

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Python tmx/servedcheck.py BAD_FILE/BAD_DIR is the older, wider list; this one is narrower on purpose.
// A file an agent sends to a chat, or the folder it works in, is refused when a part of its path
// names a place where credentials or an application's state live. Exact names and the file types
// of keys only: until 17 Nov this also refused every dot-folder, every script and program
// extension (.py, .sh, .exe, .db ...) and ordinary words (default, venv, node_modules, tmx,
// "signature", "Profile 12"), which stopped agents sending ordinary project files. Programs are
// still refused by what they are (chat/filepolicy.go), not by a word in their path.
var secretFileName = regexp.MustCompile(`(?i)\.(key|pem|p12|pfx|kdbx|npmrc|pypirc)$`)

// secretFolders: dot-folders and files that hold credentials or a tool's sign-in.
var secretFolders = map[string]bool{
	".ssh": true, ".gnupg": true, ".aws": true, ".azure": true, ".kube": true, ".docker": true, ".config": true,
	".git": true, ".git-credentials": true, ".netrc": true, ".npmrc": true, ".pypirc": true,
	".claude": true, ".codex": true, ".gemini": true,
	// application state and browser profiles
	"appdata": true, "tokens": true, "claude-config": true, "ai-accounts": true, "terminalmatrix2": true, "tmxg-trial": true,
	"tmx-go": true, "hosts.yml": true, "cookies": true, "login data": true, "local state": true, "web data": true, "user data": true,
}

func sensitiveFilePart(part string) bool {
	p := strings.ToLower(strings.TrimRight(part, " ."))
	if strings.ContainsAny(p, ":\x00") || secretFileName.MatchString(p) || secretFolders[p] || strings.HasPrefix(p, ".env") {
		return true
	}
	for _, x := range []string{"credentials", "secret"} {
		if strings.Contains(p, x) {
			return true
		}
	}
	return strings.HasPrefix(p, "id_rsa") || strings.HasPrefix(p, "id_ed25519")
}

func fileRule(rule string) error {
	return fmt.Errorf("%s: refused; use a regular, non-sensitive document in a project working folder outside system, profile-root and application-state folders", rule)
}

// Check both lexical and resolved cwd so aliases cannot hide a protected root.
func p2pWorkingFolderRule(base, state string) error {
	base = filepath.Clean(base)
	if filepath.Dir(base) == base {
		return fileRule("cwd-drive-root")
	}
	if home, err := os.UserHomeDir(); err != nil {
		return fileRule("cwd-profile-unknown")
	} else {
		for _, h := range []string{home, resolvedFolder(home)} {
			if insidePath(base, h) {
				return fileRule("cwd-profile-root")
			}
		}
	}
	// Also refuse other users' profile roots and profile container folders.
	p := strings.ToLower(filepath.ToSlash(base))
	// Only the temp prefix is exempt from application-state name checks.
	// Descendants still undergo the normal sensitive-path checks.
	tempRoot := ""
	for _, tmp := range []string{os.TempDir(), resolvedFolder(os.TempDir())} {
		if insidePath(tmp, base) {
			tempRoot = tmp
			break
		}
	}
	for _, container := range []string{"users", "home"} {
		if strings.EqualFold(filepath.Base(filepath.Dir(base)), container) || strings.EqualFold(filepath.Base(base), container) {
			return fileRule("cwd-profile-root")
		}
	}
	protected := []string{state}
	if exe, err := os.Executable(); err == nil {
		protected = append(protected, filepath.Dir(exe))
	}
	for _, env := range []string{"SystemRoot", "WINDIR", "ProgramFiles", "ProgramFiles(x86)", "ProgramData", "LOCALAPPDATA", "APPDATA", "TMX_HOME", "TMXG_HOME"} {
		if dir := os.Getenv(env); dir != "" {
			if (env == "LOCALAPPDATA" || env == "APPDATA") && tempRoot != "" && insidePath(dir, tempRoot) && !strings.EqualFold(filepath.Clean(dir), filepath.Clean(tempRoot)) {
				continue
			}
			protected = append(protected, dir)
		}
	}
	if runtime.GOOS == "windows" {
		vol := filepath.VolumeName(base) + string(filepath.Separator)
		for _, dir := range []string{"Windows", "Program Files", "Program Files (x86)", "ProgramData", "Recovery", "System Volume Information", "$Recycle.Bin"} {
			protected = append(protected, filepath.Join(vol, dir))
		}
	} else {
		protected = append(protected, "/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/dev", "/proc", "/sys", "/System", "/Library", "/Applications", "/root", "/private/etc")
		if !p2pMacTempFolder(base) {
			protected = append(protected, "/var", "/private/var")
		}
	}
	for _, dir := range protected {
		if dir != "" && (insidePath(dir, base) || insidePath(resolvedFolder(dir), base)) {
			return fileRule("cwd-system-state-install")
		}
	}
	if tempRoot != "" {
		prefix := strings.TrimPrefix(strings.ToLower(filepath.ToSlash(tempRoot)), strings.ToLower(filepath.ToSlash(filepath.VolumeName(tempRoot))))
		for _, part := range strings.Split(prefix, "/") {
			if part != "" && part != "appdata" && sensitiveFilePart(part) {
				return fileRule("cwd-sensitive-path")
			}
		}
		rel, err := filepath.Rel(tempRoot, base)
		if err == nil {
			p = strings.ToLower(filepath.ToSlash(rel))
			if p == "." {
				p = ""
			}
		}
	}
	for _, part := range strings.Split(strings.TrimPrefix(p, strings.ToLower(filepath.ToSlash(filepath.VolumeName(base)))), "/") {
		if part != "" && sensitiveFilePart(part) {
			return fileRule("cwd-sensitive-path")
		}
	}
	return nil
}

// Use slash-based cleaning so the macOS classifier is testable on every OS.
// /var is a symlink to /private/var on macOS; only the per-user T directory
// is a working area, never its parents or the rest of /private/var.
func p2pMacTempFolder(dir string) bool {
	p := path.Clean(strings.ReplaceAll(dir, `\`, "/"))
	for _, root := range []string{"/var/folders/", "/private/var/folders/"} {
		if rest, ok := strings.CutPrefix(p, root); ok {
			parts := strings.Split(rest, "/")
			return len(parts) >= 3 && parts[2] == "T"
		}
	}
	return false
}

func resolvedFolder(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}
