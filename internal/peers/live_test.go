package peers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

type liveTransport func(*http.Request) (*http.Response, error)

func (f liveTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLivePeersDoNotResurrectAfterPoll(t *testing.T) {
	p := config.Peer{Name: "beta", URL: "https://beta.example.ts.net"}
	c := New(config.Config{Profile: config.ProfileProduction, Machine: "alpha", Peers: []config.Peer{p}}, config.Paths{Home: t.TempDir()}, nil)
	if _, err := c.Tokens().Store("beta", NewPairToken(), "test"); err != nil {
		t.Fatal(err)
	}
	started, finish := make(chan struct{}), make(chan struct{})
	c.SetTransport(liveTransport(func(*http.Request) (*http.Response, error) {
		close(started)
		<-finish
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"host":"beta","sessions":[]}`)), Header: make(http.Header)}, nil
	}))
	done := make(chan struct{})
	go func() { c.PollAll(context.Background()); close(done) }()
	<-started
	c.ReplacePeers(nil)
	close(finish)
	<-done
	if c.Known("beta") || len(c.Statuses()) != 0 {
		t.Fatal("late poll restored removed peer")
	}
	// Exercise all map readers during updates; also run this with -race on the build host.
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				c.Known("beta")
				c.Names()
				c.Statuses()
				c.peerSnapshot()
			}
		}()
	}
	for range 100 {
		c.ReplacePeers([]config.Peer{p})
		c.ReplacePeers(nil)
	}
	wg.Wait()
}
