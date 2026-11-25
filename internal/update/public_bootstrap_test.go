package update

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

func TestCompiledFleetReleaseKey(t *testing.T) {
	u := &Updater{}
	ks, err := u.trustedKeys()
	if err != nil || len(ks.keys) != 1 {
		t.Fatalf("bootstrap: %+v %v", ks, err)
	}
	k := ks.keys[0]
	if k.id != "fleet-release-v1" || k.typ != config.KeyEd25519 || fmt.Sprintf("%x", sha256.Sum256(k.ed))[:12] != "3cf7e2aa36fa" {
		t.Fatalf("wrong bootstrap identity: %s %s", k.id, k.typ)
	}
	priv, pub := newKey(t)
	sums := []byte(strings.Repeat("0", 64) + "  fixture\n")
	sig := []byte(SignReleaseLine(FleetKeyID, priv, config.PublicUpdateRepo, "fleet-v1.0.3-304-gabcd", sums))
	if verifySums(FleetSignatureContext, config.PublicUpdateRepo, "fleet-v1.0.3-304-gabcd", sums, sig, ks) == nil {
		t.Fatal("bootstrap accepted an untrusted signer")
	}
	u.o.Settings = func() config.UpdateConfig { return config.UpdateConfig{FleetPublicKey: pub} }
	ks, err = u.trustedKeys()
	if err != nil || len(ks.keys) != 1 || verifySums(FleetSignatureContext, config.PublicUpdateRepo, "fleet-v1.0.3-304-gabcd", sums, sig, ks) != nil {
		t.Fatal("explicit key did not replace bootstrap")
	}
	for _, cfg := range []config.UpdateConfig{{FleetKeys: []config.FleetKey{}}, {KeysSerial: 1}, {Require: []string{}}} {
		u.o.Settings = func() config.UpdateConfig { return cfg }
		ks, err = u.trustedKeys()
		if err != nil || !ks.empty() {
			t.Fatalf("explicit empty policy/rotation restored bootstrap: %+v %v", cfg, err)
		}
	}
	var malformed config.UpdateConfig
	if err := json.Unmarshal([]byte(`{"fleet_keys":{}}`), &malformed); err != nil {
		t.Fatal(err)
	}
	u.o.Settings = func() config.UpdateConfig { return malformed }
	if _, err := u.trustedKeys(); err == nil {
		t.Fatal("malformed configuration fell back")
	}
}

func TestPublicNumberedPrereleaseDiscovery(t *testing.T) {
	const current = "fleet-v1.0.3-302-g1fe312ef"
	const next = "fleet-v1.0.3-303-gabcd1234"
	cur, ok := ParseVersion(current)
	if !ok {
		t.Fatal("current stamp rejected")
	}
	if _, ok := ParseVersion("fleet-v1.0.3-302-g"); ok {
		t.Fatal("missing hash accepted")
	}
	u := New(Options{Paths: config.Paths{Home: t.TempDir()}, Version: current, Executable: "tmxg.exe", GOOS: "windows", GOARCH: "amd64",
		Settings: func() config.UpdateConfig { return config.UpdateConfig{Repo: config.PublicUpdateRepo} },
		APIBase:  "https://fixture.invalid", HTTP: &http.Client{Transport: releaseVectorTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/repos/"+config.PublicUpdateRepo+"/releases" {
				t.Fatalf("wrong endpoint: %s", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "" {
				t.Fatal("public lookup sent credentials")
			}
			body, _ := json.Marshal([]release{{TagName: current, Prerelease: true}, {TagName: next, Prerelease: true}, {TagName: "fleet-v99.0.0-rc1", Prerelease: true}, {TagName: "fleet-v99.0.0-999-gabcd", Draft: true}, {TagName: "v99.0.0"}})
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
		})}})
	for _, channel := range []string{"stable", "latest"} {
		r, err := u.latestRelease(context.Background(), config.PublicUpdateRepo, channel)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := ParseVersion(r.TagName)
		if !cur.Less(v) || (channel == "stable" && r.TagName != next) {
			t.Fatalf("%s: %s", channel, r.TagName)
		}
	}
	if st := u.Check(context.Background()); st.State != StateAvailable {
		t.Fatalf("fresh outside install: %+v", st)
	}
}
