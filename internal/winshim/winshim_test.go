package winshim

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The shim npm writes for a package whose program is a node script.
const npmNodeShim = "@ECHO off\r\nGOTO start\r\n:find_dp0\r\nSET dp0=%~dp0\r\nEXIT /b\r\n:start\r\nSETLOCAL\r\nCALL :find_dp0\r\n\r\n" +
	"IF EXIST \"%dp0%\\node.exe\" (\r\n  SET \"_prog=%dp0%\\node.exe\"\r\n) ELSE (\r\n  SET \"_prog=node\"\r\n  SET PATHEXT=%PATHEXT:;.JS;=;%\r\n)\r\n\r\n" +
	"endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & \"%_prog%\"  \"%dp0%\\node_modules\\@anthropic-ai\\claude-code\\cli.js\" %*\r\n"

// The shim npm writes for a package that ships its own program.
const npmExeShim = "@ECHO off\r\nGOTO start\r\n:find_dp0\r\nSET dp0=%~dp0\r\nEXIT /b\r\n:start\r\nSETLOCAL\r\nCALL :find_dp0\r\n" +
	"\"%dp0%\\node_modules\\@anthropic-ai\\claude-code\\bin\\claude.exe\"   %*\r\n"

func TestTarget(t *testing.T) {
	dir := filepath.Join("C:", "tools", "node")
	script := filepath.Join(dir, "node_modules", "@anthropic-ai", "claude-code", "cli.js")
	own := filepath.Join(dir, "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe")
	beside := filepath.Join(dir, "node.exe")
	onPath := filepath.Join("C:", "Program Files", "nodejs", "node.exe")
	look := func(string) (string, error) { return onPath, nil }
	noNode := func(string) (string, error) { return "", errors.New("not found") }
	has := func(files ...string) func(string) bool {
		return func(p string) bool {
			for _, f := range files {
				if f == p {
					return true
				}
			}
			return false
		}
	}
	for _, tc := range []struct {
		name, text string
		exists     func(string) bool
		look       func(string) (string, error)
		prog       string
		pre        []string
		ok         bool
	}{
		{"node beside the shim", npmNodeShim, has(script, beside), look, beside, []string{script}, true},
		{"node on PATH", npmNodeShim, has(script), look, onPath, []string{script}, true},
		{"no node anywhere", npmNodeShim, has(script), noNode, "", nil, false},
		{"the script is not there", npmNodeShim, has(beside), look, "", nil, false},
		{"a program of its own", npmExeShim, has(own), look, own, nil, true},
		{"its program is not there", npmExeShim, has(), look, "", nil, false},
		{"the older form", "@node \"%~dp0\\node_modules\\@anthropic-ai\\claude-code\\cli.js\" %*\r\n", has(script), look, onPath, []string{script}, true},
		{"_prog is not node", strings.ReplaceAll(strings.ReplaceAll(npmNodeShim, `_prog=%dp0%\node.exe`, `_prog=%dp0%\bun.exe`), "_prog=node", "_prog=bun"), has(script, beside), look, "", nil, false},
		{"_prog is never set", "@\"%_prog%\" \"%dp0%\\node_modules\\@anthropic-ai\\claude-code\\cli.js\" %*\r\n", has(script, beside), look, "", nil, false},
		{"an unknown variable", "@\"%TOOLS%\\claude.exe\" %*\r\n", has(), look, "", nil, false},
		{"no line with all arguments", "@echo off\r\necho hello\r\n", has(), look, "", nil, false},
		{"two different lines", "@a.exe %*\r\n@b.exe %*\r\n", has("a.exe", "b.exe"), look, "", nil, false},
		{"not an exe", "@\"%dp0%\\run.ps1\" %*\r\n", has(filepath.Join(dir, "run.ps1")), look, "", nil, false},
	} {
		prog, pre, ok := Target(tc.text, dir, tc.exists, tc.look)
		if ok != tc.ok || prog != tc.prog || !reflect.DeepEqual(pre, tc.pre) {
			t.Errorf("%s: %q %q %v, want %q %q %v", tc.name, prog, pre, ok, tc.prog, tc.pre, tc.ok)
		}
	}
}

// Resolve on real files: a shim becomes its program with the script in front and the caller's
// arguments after it, line breaks and the argument after them included; anything that is not
// a shim is left as it is.
func TestResolve(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "node_modules", "@anthropic-ai", "claude-code")
	if err := os.MkdirAll(pkg, 0o700); err != nil {
		t.Fatal(err)
	}
	script, node, shim := filepath.Join(pkg, "cli.js"), filepath.Join(dir, "node.exe"), filepath.Join(dir, "claude.cmd")
	for p, body := range map[string]string{script: "// cli", node: "node", shim: npmNodeShim} {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prompt := "You are w1, a worker.\nYour lead is head.\nRule three."
	args := []string{"-p", "--append-system-prompt", prompt, "--session-id", "1234"}
	exe, out, ok := Resolve(shim, args)
	if !ok || exe != node {
		t.Fatalf("resolve: %q %v", exe, ok)
	}
	if want := append([]string{script}, args...); !reflect.DeepEqual(out, want) {
		t.Fatalf("arguments %q, want %q", out, want)
	}
	if out[3] != prompt || !strings.Contains(out[3], "\nRule three.") || out[len(out)-2] != "--session-id" {
		t.Fatalf("the prompt or what follows it changed: %q", out)
	}
	for _, p := range []string{node, filepath.Join(dir, "missing.cmd"), "claude"} {
		if exe, out, ok := Resolve(p, args); ok || exe != p || !reflect.DeepEqual(out, args) {
			t.Errorf("%s: changed to %q %q %v", p, exe, out, ok)
		}
	}
	if err := os.WriteFile(shim, []byte("@echo off\r\necho nothing to run\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if exe, _, ok := Resolve(shim, args); ok || exe != shim {
		t.Errorf("an unknown shim was resolved: %q %v", exe, ok)
	}
}
