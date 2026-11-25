// Package winshim starts what a Windows batch shim starts, without cmd.exe.
//
// npm installs its command-line programs on Windows as .cmd shims (claude.cmd, codex.cmd).
// A .cmd file can only run through cmd.exe, and cmd.exe ends a /c command at the first line
// break. tmx passes an agent's instructions as one argument of many lines
// (--append-system-prompt), so through a shim the program got the first line only, and every
// argument after it was lost, --session-id among them (found on hosta-go on 17 Nov by
// hosta-go-move-f: cmd.exe's command line had 7,129 characters, claude.exe's 777).
//
// Resolve reads the shim and returns the program it would start with the arguments it puts in
// front, so the caller can start that directly: CreateProcess passes line breaks unchanged.
package winshim

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// maxShim is the most that is read of a shim; npm's are under 1 KB.
const maxShim = 64 << 10

// Resolve returns what to start instead of path with args. For anything but a .cmd or .bat
// file, or a shim it cannot read with certainty, it returns path and args unchanged and
// ok false; then the caller starts path as before.
func Resolve(path string, args []string) (exe string, out []string, ok bool) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".cmd" && ext != ".bat" {
		return path, args, false
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) > maxShim {
		return path, args, false
	}
	prog, pre, found := Target(string(b), filepath.Dir(path), fileExists, exec.LookPath)
	if !found {
		return path, args, false
	}
	return prog, append(pre, args...), true
}

// Apply is Resolve for a caller that starts whatever comes back: the shim's program with its
// arguments, or path and args as they were.
func Apply(path string, args []string) (string, []string) {
	exe, out, _ := Resolve(path, args)
	return exe, out
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// progIsNode: the shim sets its _prog variable to node (npm writes `SET "_prog=%dp0%\node.exe"`
// and `SET "_prog=node"`), and to nothing else.
func progIsNode(text string) bool {
	sets := progRx.FindAllStringSubmatch(text, -1)
	if len(sets) == 0 {
		return false
	}
	for _, m := range sets {
		v := strings.ToLower(strings.TrimSpace(m[1]))
		if v != "node" && v != "node.exe" && !strings.HasSuffix(v, `\node.exe`) {
			return false
		}
	}
	return true
}

var (
	progRx  = regexp.MustCompile(`(?i)\b_prog=([^"\r\n]*)`)
	tokenRx = regexp.MustCompile(`"([^"]*)"|(\S+)`)
	varRx   = regexp.MustCompile(`%[^%\s"]*%|%~[a-zA-Z]*[0-9]`)
)

// Target reads a shim's text. dir is the shim's folder; exists and look are os.Stat and
// exec.LookPath (parameters for tests). It understands the line that runs the program with
// all arguments (`... %*`), as npm writes it:
//
//	"%_prog%"  "%dp0%\node_modules\pkg\cli.js" %*        node, found beside the shim or on PATH
//	"%dp0%\node_modules\pkg\bin\prog.exe"   %*            a program of its own
//	node "%~dp0\node_modules\pkg\cli.js" %*               the older form
//
// Anything else (a variable it does not know, a file that is not there, more than one such
// line that differ) is not found.
func Target(text, dir string, exists func(string) bool, look func(string) (string, error)) (prog string, pre []string, ok bool) {
	var line string
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasSuffix(l, "%*") {
			continue
		}
		// npm's shim ends with: endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & "%_prog%" "..." %*
		if i := strings.LastIndex(l, "& "); i >= 0 {
			l = strings.TrimSpace(l[i+2:])
		}
		l = strings.TrimSpace(strings.TrimPrefix(l, "@"))
		if line != "" && line != l {
			return "", nil, false
		}
		line = l
	}
	if line == "" {
		return "", nil, false
	}
	var toks []string
	for _, m := range tokenRx.FindAllStringSubmatch(strings.TrimSpace(strings.TrimSuffix(line, "%*")), -1) {
		t := m[1]
		if m[2] != "" {
			t = m[2]
		}
		toks = append(toks, t)
	}
	if len(toks) == 0 {
		return "", nil, false
	}
	expand := func(t string) (string, bool) {
		for _, v := range []string{`%~dp0\`, `%dp0%\`, `%~dp0`, `%dp0%`} {
			if strings.HasPrefix(t, v) {
				t = filepath.Join(dir, strings.ReplaceAll(t[len(v):], `\`, string(filepath.Separator)))
				break
			}
		}
		return t, !varRx.MatchString(t)
	}
	first := toks[0]
	switch low := strings.ToLower(first); {
	case low == "%_prog%" && !progIsNode(text):
		return "", nil, false // a _prog this shim sets to something else, or never: start the shim as before
	case low == "%_prog%" || low == "node" || low == "node.exe":
		// npm: node.exe beside the shim if it is there, else node on PATH.
		if p := filepath.Join(dir, "node.exe"); exists(p) {
			prog = p
		} else if p, err := look("node"); err == nil {
			prog = p
		} else {
			return "", nil, false
		}
	default:
		p, clean := expand(first)
		if !clean || !exists(p) || strings.ToLower(filepath.Ext(p)) != ".exe" {
			return "", nil, false
		}
		prog = p
	}
	for _, t := range toks[1:] {
		p, clean := expand(t)
		if !clean {
			return "", nil, false
		}
		if p != t && !exists(p) { // a file of the package that is not there
			return "", nil, false
		}
		pre = append(pre, p)
	}
	return prog, pre, true
}
