package cli

import (
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/rollback"
)

func TestDesktopRestartWaitsForUpdateSuccessor(t *testing.T) {
	paths := config.Paths{Home: t.TempDir()}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	token, err := config.OwnerToken(paths)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := filepath.Join(paths.Home, "rollback")
	now := time.Now()
	if err := rollback.BeginProbation(checkpoint, "old", "new", now); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/api/snapshot":
			if r.Header.Get("Authorization") != "Bearer "+token {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	e := &env{paths: paths, loaded: true, cfg: config.Config{Port: server.Listener.Addr().(*net.TCPAddr).Port}}
	if !e.recentUpdateHandover(now) || e.recentUpdateHandover(now.Add(time.Minute)) {
		t.Fatal("the probation marker should only delay recovery during the recent handover")
	}
	started := make(chan struct{})
	go func() {
		time.Sleep(250 * time.Millisecond)
		server.Start()
		close(started)
	}()
	defer func() { <-started; server.Close() }()
	if err := e.restartDesktopDaemon(); err != nil {
		t.Fatal(err)
	}
	if !e.ownedHealthy(e.baseURL()) {
		t.Fatal("restart returned before the owned successor was ready")
	}
}
