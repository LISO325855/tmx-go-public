package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/machinehealth"
	"github.com/blackcode-ch/tmx-go/internal/peers"
	"github.com/blackcode-ch/tmx-go/internal/reshistory"
	"github.com/blackcode-ch/tmx-go/internal/resources"
)

// startPlain serves h for the test and returns its address.
func startPlain(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func addHistory(d *apitest.Daemon, cpu float64) {
	now := time.Now()
	for i := range 3 {
		d.History.Add(resources.Sample{At: now.Add(time.Duration(i-3) * 5 * time.Second).UnixMilli(), Interval: 5,
			Machine: resources.MachineUsage{CPU: cpu, Cores: 8, MemTotal: 32 << 30, MemFree: 8 << 30, DiskWriteRate: 1 << 20, DiskQueue: -1}})
	}
}

// GET /api/resources/history: this machine's readings for one of four periods, to any
// authenticated caller (as GET /api/resources), nothing to an unauthenticated one.
func TestResourceHistory(t *testing.T) {
	d := apitest.New(t)
	addHistory(d, 40)
	owner := client{t: t, base: d.URL, token: d.OwnerToken}
	var v reshistory.View
	if got := owner.do("GET", "/api/resources/history", nil, &v); got != 200 || v.Period != "1h" || v.Step != 10 || len(v.Points) == 0 {
		t.Fatalf("default period: %d %+v", got, v)
	}
	if p := v.Points[0]; p.CPU != 40 || p.Write == nil || p.Queue != nil || v.MemTotal != 32<<30 || v.Cores != 8 {
		t.Fatalf("point %+v, view %+v", p, v)
	}
	for period, step := range map[string]int{"6h": 60, "24h": 60, "7d": 900} {
		if got := owner.do("GET", "/api/resources/history?period="+period, nil, &v); got != 200 || v.Period != period || v.Step != step {
			t.Fatalf("%s: %d step %d", period, got, v.Step)
		}
	}
	if got := owner.do("GET", "/api/resources/history?period=1y", nil, nil); got != http.StatusBadRequest {
		t.Fatalf("unknown period: %d", got)
	}
	worker := client{t: t, base: d.URL, token: d.AgentToken(t, mkWorker(t, d))}
	if got := worker.do("GET", "/api/resources/history", nil, nil); got != 200 {
		t.Fatalf("an agent reads the machine's readings like GET /api/resources: %d", got)
	}
	if got := (client{t: t, base: d.URL}).do("GET", "/api/resources/history", nil, nil); got != http.StatusUnauthorized {
		t.Fatalf("no token: %d", got)
	}
}

// GET /api/machines/{name}/history: the owner reads another Go machine's history through this
// daemon and the pair token the two share. Only the owner; only a configured machine; only one
// of the four periods; and a machine that does not answer or has no such route says so.
func TestMachineHistory(t *testing.T) {
	da, db := apitest.New(t), apitest.New(t)
	pair := peers.NewPairToken()
	cb := peers.New(config.Config{Profile: config.ProfileProduction, Machine: "beta", Peers: []config.Peer{{Name: "alpha", URL: "https://alpha.example.ts.net"}}}, config.Paths{Home: db.Home}, nil)
	if _, err := cb.Tokens().Store("alpha", pair, "test"); err != nil {
		t.Fatal(err)
	}
	betaURL := db.WithPeers(t, cb)
	py := &pyPeer{} // "gamma": a machine without the route (a Python daemon answers 404)
	ca := peers.New(config.Config{Profile: config.ProfileProduction, Machine: "alpha", Peers: []config.Peer{
		{Name: "beta", URL: betaURL}, {Name: "gamma", URL: startPlain(t, py)}, {Name: "delta", URL: "http://127.0.0.1:1"}}}, config.Paths{Home: da.Home}, nil)
	for _, name := range []string{"beta", "gamma", "delta"} {
		if _, err := ca.Tokens().Store(name, pair, "test"); err != nil {
			t.Fatal(err)
		}
	}
	alphaURL := da.WithPeers(t, ca)
	addHistory(da, 11) // alpha's own readings must never be answered for beta
	addHistory(db, 77)

	owner := client{t: t, base: alphaURL, token: da.OwnerToken}
	var v reshistory.View
	if got := owner.do("GET", "/api/machines/beta/history?period=1h", nil, &v); got != 200 || v.Period != "1h" || len(v.Points) == 0 || v.Points[0].CPU != 77 {
		t.Fatalf("beta's history through alpha: %d %+v", got, v)
	}
	if got := owner.do("GET", "/api/machines/BETA/history", nil, &v); got != 200 || v.Points[0].CPU != 77 {
		t.Fatalf("machine names compare without case: %d", got)
	}
	for path, want := range map[string]int{
		"/api/machines/beta/history?period=1y":         http.StatusBadRequest,
		"/api/machines/beta/history?period=1h%26x%3D1": http.StatusBadRequest, // never passed on as a query
		"/api/machines/nobody/history":                 http.StatusNotFound,
		"/api/machines/alpha/history":                  http.StatusNotFound, // this machine is not its own peer
		"/api/machines/gamma/history":                  http.StatusBadGateway,
		"/api/machines/delta/history":                  http.StatusBadGateway,
	} {
		if got := owner.do("GET", path, nil, nil); got != want {
			t.Fatalf("%s: %d, want %d", path, got, want)
		}
	}
	// Not the owner: neither alpha's head nor a worker reads another machine through this route.
	for _, token := range []string{da.AgentToken(t, "head"), da.AgentToken(t, mkWorker(t, da)), ""} {
		want := http.StatusForbidden
		if token == "" {
			want = http.StatusUnauthorized
		}
		if got := (client{t: t, base: alphaURL, token: token}).do("GET", "/api/machines/beta/history", nil, nil); got != want {
			t.Fatalf("not the owner: %d, want %d", got, want)
		}
	}
	// And beta answers its history to alpha's pair token only.
	if got := peerDo(t, betaURL, "GET", "/api/resources/history", "alpha", peers.NewPairToken(), nil, nil, nil); got != http.StatusUnauthorized {
		t.Fatalf("wrong pair token: %d", got)
	}
}

// GET /api/machine/health: this machine's temperatures, drive health and GPU to any
// authenticated caller. GET /api/machines/{name}/health: the owner reads another machine's
// through this daemon - and what that machine sends is cleaned before it reaches the window.
func TestMachineHealth(t *testing.T) {
	d := apitest.New(t)
	reading := fmt.Sprintf(`{"cpu_c":31.5,"disk_c":40,"at":%d,"disks":[{"name":"Corsair MP400","health":"Healthy","status":"OK","wear":31,"temp_c":40,"power_on_hours":24858}]}`, time.Now().Unix())
	if err := os.WriteFile(d.Health.ReaderFile, []byte(reading), 0o600); err != nil {
		t.Fatal(err)
	}
	owner := client{t: t, base: d.URL, token: d.OwnerToken}
	var v machinehealth.View
	if got := owner.do("GET", "/api/machine/health", nil, &v); got != 200 || v.CPUTempC == nil || *v.CPUTempC != 31.5 || len(v.Disks) != 1 || v.Disks[0].Name != "Corsair MP400" || v.GPU != nil {
		t.Fatalf("this machine: %d %+v", got, v)
	}
	if got := (client{t: t, base: d.URL, token: d.AgentToken(t, mkWorker(t, d))}).do("GET", "/api/machine/health", nil, nil); got != 200 {
		t.Fatalf("an agent reads the machine's readings like GET /api/resources: %d", got)
	}
	if got := (client{t: t, base: d.URL}).do("GET", "/api/machine/health", nil, nil); got != http.StatusUnauthorized {
		t.Fatalf("no token: %d", got)
	}

	// Another machine that answers hostile readings, and one without the route.
	hostile := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/machine/health" || r.Header.Get(peers.HeaderToken) == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"at":5,"temps_at":5,"cpu_temp_c":900,"disk_temp_c":41.5,"temps_note":"<b>hi</b>\u0007",` +
			`"disks":[{"name":"` + strings.Repeat("x", 300) + `â€®","health":"Healthy\u0000","wear_pct":400,"temp_c":-3,"read_errors":-1,"power_on_hours":7}],` +
			`"gpu":{"name":"RTX\u0007","temp_c":41,"used_gb":-2,"total_gb":16,"util_pct":900,"power_w":14},"extra":{"x":1}}`))
	})
	pair := peers.NewPairToken()
	pc := peers.New(config.Config{Profile: config.ProfileProduction, Machine: "alpha", Peers: []config.Peer{
		{Name: "beta", URL: startPlain(t, hostile)}, {Name: "gamma", URL: startPlain(t, &pyPeer{})}}}, config.Paths{Home: d.Home}, nil)
	for _, name := range []string{"beta", "gamma"} {
		if _, err := pc.Tokens().Store(name, pair, "test"); err != nil {
			t.Fatal(err)
		}
	}
	base := d.WithPeers(t, pc)
	owner = client{t: t, base: base, token: d.OwnerToken}
	v = machinehealth.View{}
	if got := owner.do("GET", "/api/machines/beta/health", nil, &v); got != 200 {
		t.Fatalf("another machine: %d", got)
	}
	disk := v.Disks[0]
	if v.CPUTempC != nil || v.DiskTempC == nil || *v.DiskTempC != 41.5 || v.TempsNote != "<b>hi</b>" || len([]rune(disk.Name)) != 80 || disk.Health != "Healthy" ||
		disk.WearPct != nil || disk.TempC != nil || disk.ReadErrors != nil || disk.PowerOnHours == nil || *disk.PowerOnHours != 7 {
		t.Fatalf("not cleaned: %+v disk %+v", v, disk)
	}
	if g := v.GPU; g == nil || g.Name != "RTX" || g.UsedGB != nil || g.UtilPct != nil || g.TempC == nil || *g.TempC != 41 || *g.TotalGB != 16 {
		t.Fatalf("gpu not cleaned: %+v", v.GPU)
	}
	for path, want := range map[string]int{
		"/api/machines/gamma/health":  http.StatusBadGateway, // a Python daemon: no such route
		"/api/machines/nobody/health": http.StatusNotFound,
	} {
		if got := owner.do("GET", path, nil, nil); got != want {
			t.Fatalf("%s: %d, want %d", path, got, want)
		}
	}
	if got := (client{t: t, base: base, token: d.AgentToken(t, "head")}).do("GET", "/api/machines/beta/health", nil, nil); got != http.StatusForbidden {
		t.Fatalf("the head reads another machine's hardware: %d", got)
	}
}

// Another machine's per-disk series reach the window bounded: at most 16 disks, names cleaned,
// nameless ones dropped, and no more points per disk than a period can hold.
func TestMachineHistoryBoundsDisks(t *testing.T) {
	d := apitest.New(t)
	var b strings.Builder
	b.WriteString(`{"period":"1h","step":10,"from":1,"to":2,"points":[],"disks":[{"name":"","points":[{"t":1,"write":1}]},{"name":"bad\u0007` + strings.Repeat("x", 200) + `","points":[`)
	for i := range 2500 {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"t":%d,"write":1,"write_max":1}`, i+1)
	}
	b.WriteString(`]}`)
	for i := range 30 {
		fmt.Fprintf(&b, `,{"name":"disk%02d","points":[]}`, i)
	}
	b.WriteString(`]}`)
	body := b.String()
	peer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	pc := peers.New(config.Config{Profile: config.ProfileProduction, Machine: "alpha", Peers: []config.Peer{{Name: "beta", URL: startPlain(t, peer)}}}, config.Paths{Home: d.Home}, nil)
	if _, err := pc.Tokens().Store("beta", peers.NewPairToken(), "test"); err != nil {
		t.Fatal(err)
	}
	owner := client{t: t, base: d.WithPeers(t, pc), token: d.OwnerToken}
	var v reshistory.View
	if got := owner.do("GET", "/api/machines/beta/history", nil, &v); got != 200 {
		t.Fatalf("status %d", got)
	}
	if len(v.Disks) != 16 || len([]rune(v.Disks[0].Name)) > 65 || strings.ContainsRune(v.Disks[0].Name, 7) || !strings.HasPrefix(v.Disks[0].Name, "bad") {
		t.Fatalf("%d disks, first %q", len(v.Disks), v.Disks[0].Name)
	}
	if n := len(v.Disks[0].Points); n != 2000 || v.Disks[0].Points[n-1].T != 2500 {
		t.Fatalf("%d points kept, the newest must stay", n)
	}
}
