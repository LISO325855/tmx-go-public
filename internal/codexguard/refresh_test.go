package codexguard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRefreshOnlyVerifiedSystemHashes(t *testing.T) {
	for _, tc := range []string{"system-update", "unsigned", "codex", "guard", "path", "added", "non-system", "invalid"} {
		t.Run(tc, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "cmd.exe")
			if err := os.WriteFile(file, []byte("updated fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			pin, err := OpenPin(file)
			if err != nil {
				t.Fatal(err)
			}
			target := Target{Path: file, SHA256: pin.SHA256}
			pin.Close()
			old := Manifest{Shell: Target{Path: file, SHA256: "old"}, Codex: Target{Path: "codex", SHA256: "same"}, Targets: map[string]Target{"bcdedit": {Path: file, SHA256: "old"}}}
			next := Manifest{Shell: target, Codex: old.Codex, Targets: map[string]Target{"bcdedit": target}}
			switch tc {
			case "codex":
				next.Codex.SHA256 = "changed"
			case "guard":
				next.Guard.Own = "changed"
			case "path":
				old.Shell.Path = "different"
			case "added":
				old.Shell = target
				old.Targets["bcdedit"] = target
				next.Targets["new"] = target
			}
			body, _ := json.Marshal(old)
			if tc == "invalid" {
				body = append(body, []byte(`{}`)...)
			}
			path := filepath.Join(dir, "manifest.json")
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			verified := 0
			changed, err := refreshSystemManifest(path, next, func(p string) error {
				verified++
				if tc == "unsigned" || tc == "non-system" {
					return fmt.Errorf("untrusted fixture")
				}
				return nil
			})
			want := tc == "system-update" || tc == "added"
			if changed != want || (want && err != nil) {
				t.Fatalf("changed=%v err=%v", changed, err)
			}
			got, _ := os.ReadFile(path)
			if !want && string(got) != string(body) {
				t.Fatal("rewrote refused manifest")
			}
			if want {
				wantVerified := 0
				if tc == "system-update" {
					wantVerified = 2
				}
				if verified != wantVerified {
					t.Fatalf("verified %d targets", verified)
				}
				expected, _ := json.Marshal(next)
				if string(got) != string(expected) {
					t.Fatal("wrong refreshed manifest")
				}
				again, err := refreshSystemManifest(path, next, func(string) error { t.Fatal("unchanged binary reverified"); return nil })
				if again || err != nil {
					t.Fatalf("unchanged refresh: %v %v", again, err)
				}
			}
		})
	}
}
