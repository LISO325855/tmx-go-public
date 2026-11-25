package machinehealth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Unix(1794944883, 0).Add(3 * time.Minute)

// readerJSON is the reader's file as it stands on a real machine (two drives added: one that
// reports everything, one USB stick that reports nothing).
const readerJSON = `{"cpu_c":27.9,"disk_c":42,"at":1794944883,"source":"acpi,storage","interval_s":600,"disks":[` +
	`{"id":"0","name":"Samsung SSD 850 EVO 250GB","health":"Healthy","status":"OK","wear":0,"read_errors":0,"read_uncorrected":null,"write_errors":null,"write_uncorrected":null,"temp_c":0,"temp_max_c":0,"power_on_hours":23694},` +
	`{"id":"1","name":"Corsair MP400\u0007","health":"Warning","status":"OK,Predictive Failure","wear":31,"read_errors":2,"read_uncorrected":1,"write_errors":3,"write_uncorrected":0,"temp_c":37,"temp_max_c":70,"power_on_hours":24858},` +
	`{"id":"2","name":"","health":"Healthy","status":"OK","wear":250,"read_errors":-4,"temp_c":900,"power_on_hours":null}],` +
	`"smart":[{"instance":"x","data":"AQAF","predict":false}]}`

func service(t *testing.T, content string) *Service {
	t.Helper()
	path := filepath.Join(t.TempDir(), "temps.json")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &Service{ReaderFile: path, Now: func() time.Time { return now },
		SMI: func(context.Context) (string, error) { return "", errors.New("no nvidia-smi") }}
}

// The reader's file becomes temperatures and one row per drive; what a drive does not report
// stays absent - a 0 degree drive, a wear of 250 %, a negative error count are not readings.
func TestReadsTheReaderFile(t *testing.T) {
	v := service(t, readerJSON).View(context.Background())
	if v.TempsAt != 1794944883000 || v.TempsNote != "" || v.CPUTempC == nil || *v.CPUTempC != 27.9 || v.DiskTempC == nil || *v.DiskTempC != 42 {
		t.Fatalf("temperatures: %+v", v)
	}
	if len(v.Disks) != 3 || v.GPU != nil {
		t.Fatalf("drives %d, gpu %v", len(v.Disks), v.GPU)
	}
	a, b, c := v.Disks[0], v.Disks[1], v.Disks[2]
	if a.Name != "Samsung SSD 850 EVO 250GB" || a.Health != "Healthy" || a.TempC != nil || a.WearPct == nil || *a.WearPct != 0 ||
		a.PowerOnHours == nil || *a.PowerOnHours != 23694 || a.ReadErrors == nil || *a.ReadErrors != 0 || a.WriteErrors != nil || a.ReadUncorrected != nil {
		t.Fatalf("first drive: %+v", a)
	}
	if b.Name != "Corsair MP400" || b.Health != "Warning" || b.Status != "OK,Predictive Failure" || *b.WearPct != 31 || *b.TempC != 37 ||
		*b.ReadErrors != 2 || *b.ReadUncorrected != 1 || *b.WriteErrors != 3 || *b.WriteUncorrected != 0 {
		t.Fatalf("second drive: %+v", b)
	}
	if c.Name != "Drive 3" || c.WearPct != nil || c.ReadErrors != nil || c.TempC != nil || c.PowerOnHours != nil {
		t.Fatalf("a drive reporting nonsense: %+v", c)
	}
}

// No reader, a stale file, a file from the future, damaged or oversized: no readings, and a
// plain sentence why - never the last numbers, never zeros.
func TestNoUsableReaderFile(t *testing.T) {
	stale := strings.Replace(readerJSON, `"at":1794944883`, fmt.Sprintf(`"at":%d`, now.Add(-26*time.Minute).Unix()), 1)
	future := strings.Replace(readerJSON, `"at":1794944883`, fmt.Sprintf(`"at":%d`, now.Add(time.Hour).Unix()), 1)
	for name, c := range map[string]struct{ content, note string }{
		"missing":  {"", "need the reader"},
		"stale":    {stale, "too old"},
		"future":   {future, "too old"},
		"damaged":  {`{"cpu_c":`, "could not be understood"},
		"no time":  {`{"cpu_c":30,"disks":[]}`, "could not be understood"},
		"too big":  {strings.Repeat(" ", maxReaderFile+1), "need the reader"},
		"with BOM": {"\xef\xbb\xbf" + readerJSON, ""},
	} {
		v := service(t, c.content).View(context.Background())
		if c.note == "" {
			if v.TempsAt == 0 || v.CPUTempC == nil {
				t.Fatalf("%s: not read: %+v", name, v)
			}
			continue
		}
		if v.TempsAt != 0 || v.CPUTempC != nil || v.DiskTempC != nil || len(v.Disks) != 0 || !strings.Contains(v.TempsNote, c.note) {
			t.Fatalf("%s: %+v", name, v)
		}
		if v.Disks == nil {
			t.Fatalf("%s: disks must be an empty list, not null", name)
		}
	}
}

// Linux: the hottest thermal zone and the hottest drive the kernel exposes.
func TestReadsSys(t *testing.T) {
	root := t.TempDir()
	write := func(rel, s string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var v View
	readSys(root, now, &v)
	if v.TempsAt != 0 || v.TempsNote == "" {
		t.Fatalf("no sensors: %+v", v)
	}
	write("class/thermal/thermal_zone0/temp", "41000\n")
	write("class/thermal/thermal_zone1/temp", "56500\n")
	write("class/thermal/thermal_zone2/temp", "garbage")
	write("class/hwmon/hwmon0/name", "nvme\n")
	write("class/hwmon/hwmon0/temp1_input", "38850\n")
	write("class/hwmon/hwmon1/name", "coretemp\n") // not a drive
	write("class/hwmon/hwmon1/temp1_input", "90000\n")
	v = View{}
	readSys(root, now, &v)
	if v.TempsAt != now.UnixMilli() || v.CPUTempC == nil || *v.CPUTempC != 56.5 || v.DiskTempC == nil || *v.DiskTempC != 38.9 {
		t.Fatalf("sys: cpu %v disk %v at %d", v.CPUTempC, v.DiskTempC, v.TempsAt)
	}
}

func TestParseSMI(t *testing.T) {
	g := ParseSMI("NVIDIA GeForce RTX 5060 Ti, 41, 5734, 16311, 1, 14.22\n")
	if g == nil || g.Name != "NVIDIA GeForce RTX 5060 Ti" || *g.TempC != 41 || *g.UsedGB != 5.6 || *g.TotalGB != 15.93 || *g.UtilPct != 1 || *g.PowerW != 14.22 {
		t.Fatalf("%+v", g)
	}
	// An old card reports no power draw; nonsense in a field is no reading.
	g = ParseSMI("\nNVIDIA GeForce GT 1030, 38, 512, 2048, [N/A], [N/A]\n")
	if g == nil || g.PowerW != nil || g.UtilPct != nil || *g.TotalGB != 2 || *g.TempC != 38 {
		t.Fatalf("%+v", g)
	}
	if g := ParseSMI("x, 900, -1, 0, 250, 1e9"); g == nil || g.TempC != nil || g.UsedGB != nil || g.TotalGB != nil || g.UtilPct != nil || g.PowerW != nil {
		t.Fatalf("out-of-range fields kept: %+v", g)
	}
	for _, s := range []string{"", "No devices were found", "a,b,c", ", 1, 2, 3, 4, 5"} {
		if g := ParseSMI(s); g != nil {
			t.Fatalf("%q: %+v", s, g)
		}
	}
}

// The view is cached for half a minute: looking at the page does not start nvidia-smi on
// every refresh, and nothing runs at all when nobody asks.
func TestViewIsCached(t *testing.T) {
	s := service(t, readerJSON)
	calls := 0
	s.SMI = func(context.Context) (string, error) {
		calls++
		return "NVIDIA GeForce RTX 5060 Ti, 41, 5734, 16311, 1, 14.22", nil
	}
	clock := now
	s.Now = func() time.Time { return clock }
	if calls != 0 {
		t.Fatal("something ran before anyone asked")
	}
	first := s.View(context.Background())
	clock = clock.Add(29 * time.Second)
	if again := s.View(context.Background()); calls != 1 || again.At != first.At || again.GPU == nil {
		t.Fatalf("within the cache: %d calls", calls)
	}
	clock = clock.Add(2 * time.Second)
	if later := s.View(context.Background()); calls != 2 || later.At == first.At {
		t.Fatalf("after the cache: %d calls", calls)
	}
}
