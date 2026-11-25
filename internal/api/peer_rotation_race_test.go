package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/api"
	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/peers"
)

type pausedRotationBody struct {
	entered, resume chan struct{}
	once            sync.Once
	io.Reader
}

func (b *pausedRotationBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered); <-b.resume })
	return b.Reader.Read(p)
}
func (b *pausedRotationBody) Close() error { return nil }

func TestIncomingRotationCannotRestoreRevokedPair(t *testing.T) {
	for _, repair := range []bool{false, true} {
		name := "removed"
		if repair {
			name = "repaired"
		}
		t.Run(name, func(t *testing.T) {
			d := apitest.New(t)
			production(t, d, "beta")
			paths := config.Paths{Home: d.Home}
			cfg, err := config.Load(paths)
			if err != nil {
				t.Fatal(err)
			}
			peer := config.Peer{Name: "alpha", URL: "https://alpha.example.ts.net"}
			cfg.Peers = []config.Peer{peer}
			if err := config.Save(paths, cfg); err != nil {
				t.Fatal(err)
			}
			pc := peers.New(cfg, paths, nil)
			original, stale := peers.NewPairToken(), peers.NewPairToken()
			if _, err := pc.Tokens().Store("alpha", original, "test"); err != nil {
				t.Fatal(err)
			}
			h := api.New(api.Options{Fleet: d.Fleet, OwnerToken: d.OwnerToken, Peers: pc, TrustedHosts: []string{"beta.example.ts.net"}}).Handler()
			raw, _ := json.Marshal(map[string]string{"token": stale})
			body := &pausedRotationBody{entered: make(chan struct{}), resume: make(chan struct{}), Reader: bytes.NewReader(raw)}
			req := httptest.NewRequest(http.MethodPost, "http://localhost/api/peers/pair-token", body)
			req.Header.Set(peers.HeaderFrom, "alpha")
			req.Header.Set(peers.HeaderToken, original)
			out := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { h.ServeHTTP(out, req); close(done) }()
			select {
			case <-body.entered: // authentication succeeded; JSON decoding is paused
			case <-done:
				t.Fatalf("request ended before body pause: %d", out.Code)
			case <-time.After(5 * time.Second):
				close(body.resume)
				t.Fatal("rotation did not reach body decode")
			}
			ownerCall := func(method, path string, value any) *httptest.ResponseRecorder {
				raw, _ := json.Marshal(value)
				r := httptest.NewRequest(method, "http://localhost"+path, bytes.NewReader(raw))
				r.Header.Set("Authorization", "Bearer "+d.OwnerToken)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			removed := ownerCall(http.MethodDelete, "/api/peers/alpha", nil)
			if removed.Code != 200 {
				close(body.resume)
				<-done
				t.Fatalf("remove: %d", removed.Code)
			}
			expected := ""
			if repair {
				invite := ownerCall(http.MethodPost, "/api/peers/invite", peer)
				var code struct {
					Code string `json:"code"`
				}
				if invite.Code != 200 || json.Unmarshal(invite.Body.Bytes(), &code) != nil {
					close(body.resume)
					<-done
					t.Fatal("invite failed")
				}
				raw, _ := json.Marshal(map[string]string{"code": code.Code, "name": "alpha", "url": peer.URL})
				r := httptest.NewRequest(http.MethodPost, "https://beta.example.ts.net/api/machines/join/accept", bytes.NewReader(raw))
				r.RemoteAddr = "127.0.0.1:1234"
				r.Header.Set("X-Forwarded-Proto", "https")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != 200 {
					close(body.resume)
					<-done
					t.Fatalf("fresh pairing: %d", w.Code)
				}
				expected = pc.Tokens().For("alpha")
			}
			close(body.resume)
			<-done
			if out.Code < 400 {
				t.Fatal("stale authenticated rotation accepted")
			}
			if pc.Tokens().For("alpha") != expected {
				t.Fatal("stale rotation recreated or overwrote pair")
			}
			if _, ok := pc.Tokens().Identify(stale); ok {
				t.Fatal("revoked peer's proposed credential became valid")
			}
			if strings.Contains(out.Body.String(), stale) {
				t.Fatal("refusal exposed credential")
			}
		})
	}
}
