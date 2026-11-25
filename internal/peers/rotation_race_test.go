package peers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

func TestOutgoingRotationCannotOverwriteRemovedOrRepairedPeer(t *testing.T) {
	for _, name := range []string{"removed", "repaired", "reimported-same-token"} {
		repair := name != "removed"
		t.Run(name, func(t *testing.T) {
			p := config.Peer{Name: "beta", URL: "https://beta.example.ts.net"}
			paths := config.Paths{Home: t.TempDir()}
			c := New(config.Config{Profile: config.ProfileProduction, Machine: "alpha", Peers: []config.Peer{p}}, paths, nil)
			original := NewPairToken()
			if _, err := c.Tokens().Store("beta", original, "test"); err != nil {
				t.Fatal(err)
			}
			entered, resume := make(chan struct{}), make(chan struct{})
			c.SetTransport(liveTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get(HeaderToken) != original {
					t.Error("rotation did not use snapshotted credential")
				}
				var req struct {
					Token string `json:"token"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					return nil, err
				}
				close(entered)
				<-resume
				raw, _ := json.Marshal(map[string]string{"fingerprint": Fingerprint(req.Token)})
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw))), Header: make(http.Header)}, nil
			}))
			done := make(chan error, 1)
			go func() { _, err := c.RotateWith(context.Background(), "beta"); done <- err }()
			select {
			case <-entered:
			case err := <-done:
				t.Fatalf("rotation ended before exchange pause: %v", err)
			case <-time.After(5 * time.Second):
				close(resume)
				t.Fatal("rotation did not reach exchange")
			}
			edit := MutationMutex(paths.PeersFile())
			edit.Lock()
			err := c.Tokens().RemoveLocked("beta")
			c.ReplacePeersLocked(nil)
			expected := ""
			if repair && err == nil {
				expected = NewPairToken()
				if name == "reimported-same-token" {
					expected = original
				}
				_, err = c.Tokens().StoreLocked("beta", expected, "new-pair")
				c.ReplacePeersLocked([]config.Peer{p}) // same name AND URL, but a different relationship
			}
			edit.Unlock()
			close(resume)
			result := <-done
			if err != nil {
				t.Fatal(err)
			}
			if !errors.Is(result, ErrStalePair) {
				t.Fatalf("stale result: %v", result)
			}
			if got := c.Tokens().For("beta"); got != expected {
				t.Fatal("stale response restored or replaced pair")
			}
		})
	}
}
