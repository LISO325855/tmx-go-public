// Package machinehealth reads what the Watchdog page shows about the machine's hardware:
// CPU and disk temperature, each drive's health, and the GPU. It is read on demand and cached
// briefly: nothing runs while nobody looks at the page.
//
// Temperatures and drive health need administrator rights on Windows, which the daemon never
// has. They come from the file the owner-installed reader writes (the Python tmx's
// `tmx setup --temps`: a fixed read-only script run by a SYSTEM task every 10 minutes into
// %ProgramData%\TerminalMatrix\temps.json). This package only reads that file - it starts no
// PowerShell, ever: each start made Windows log megabytes of script blocks to the event log.
// Without the reader, or with a stale file, the readings are absent, never guessed. On Linux
// the temperatures come from /sys. The GPU comes from nvidia-smi when it is installed.
package machinehealth

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
)

const (
	// readerMaxAge: the reader runs every 10 minutes; a reading may miss one run before it
	// counts as stale (the Python daemon's rule).
	readerMaxAge  = 25 * time.Minute
	maxReaderFile = 1 << 20
	maxDisks      = 32
	cacheFor      = 30 * time.Second
	smiTimeout    = 5 * time.Second
)

// Disk is one drive's health, as the reader reported it. A field the drive does not report is
// absent (nil), never zero.
type Disk struct {
	Name             string   `json:"name"`
	Health           string   `json:"health,omitempty"` // Healthy, Warning, Unhealthy, Unknown
	Status           string   `json:"status,omitempty"` // OK, Degraded, Predictive Failure, …
	WearPct          *int     `json:"wear_pct,omitempty"`
	TempC            *float64 `json:"temp_c,omitempty"`
	PowerOnHours     *int64   `json:"power_on_hours,omitempty"`
	ReadErrors       *int64   `json:"read_errors,omitempty"`
	WriteErrors      *int64   `json:"write_errors,omitempty"`
	ReadUncorrected  *int64   `json:"read_uncorrected,omitempty"`
	WriteUncorrected *int64   `json:"write_uncorrected,omitempty"`
}

// GPU is the first NVIDIA card, as nvidia-smi reports it.
type GPU struct {
	Name    string   `json:"name"`
	TempC   *float64 `json:"temp_c,omitempty"`
	UsedGB  *float64 `json:"used_gb,omitempty"`
	TotalGB *float64 `json:"total_gb,omitempty"`
	UtilPct *float64 `json:"util_pct,omitempty"`
	PowerW  *float64 `json:"power_w,omitempty"`
}

// View is GET /api/machine/health.
type View struct {
	At int64 `json:"at"` // Unix ms: when this view was read
	// TempsAt is when the temperatures and drive health were read (Unix ms); 0 = there is no
	// usable reading, and TempsNote says why in plain words.
	TempsAt   int64    `json:"temps_at"`
	TempsNote string   `json:"temps_note,omitempty"`
	CPUTempC  *float64 `json:"cpu_temp_c,omitempty"`
	DiskTempC *float64 `json:"disk_temp_c,omitempty"`
	Disks     []Disk   `json:"disks"`
	GPU       *GPU     `json:"gpu,omitempty"` // nil = no NVIDIA card, or no nvidia-smi
}

// Service reads and caches the view.
type Service struct {
	// ReaderFile is the reader's file; "" = this system has none (temperatures from /sys on Linux).
	ReaderFile string
	// SMI runs nvidia-smi and returns its output; nil = the real one.
	SMI func(ctx context.Context) (string, error)
	Now func() time.Time

	mu   sync.Mutex
	last View
}

// New is the Service for this machine.
func New() *Service {
	s := &Service{Now: time.Now}
	if runtime.GOOS == "windows" {
		dir := os.Getenv("ProgramData")
		if dir == "" {
			dir = `C:\ProgramData`
		}
		s.ReaderFile = filepath.Join(dir, "TerminalMatrix", "temps.json")
	}
	return s
}

// View answers the current readings, at most cacheFor old.
func (s *Service) View(ctx context.Context) View {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	if s.last.At != 0 && now.Sub(time.UnixMilli(s.last.At)) < cacheFor {
		return s.last
	}
	v := View{At: now.UnixMilli(), Disks: []Disk{}}
	switch {
	case s.ReaderFile != "":
		readReader(s.ReaderFile, now, &v)
	case runtime.GOOS == "linux":
		readSys("/sys", now, &v)
	default:
		v.TempsNote = "Temperatures and drive health are not read on this system."
	}
	smi := s.SMI
	if smi == nil {
		smi = runSMI
	}
	if out, err := smi(ctx); err == nil {
		v.GPU = ParseSMI(out)
	}
	s.last = v
	return v
}

// readerFile is the reader's JSON (temps.py's script). Numbers arrive as numbers or null.
type readerFile struct {
	CPU   *float64 `json:"cpu_c"`
	Disk  *float64 `json:"disk_c"`
	At    int64    `json:"at"` // Unix seconds
	Disks []struct {
		Name             string   `json:"name"`
		Health           string   `json:"health"`
		Status           string   `json:"status"`
		Wear             *float64 `json:"wear"`
		Temp             *float64 `json:"temp_c"`
		Hours            *float64 `json:"power_on_hours"`
		ReadErrors       *float64 `json:"read_errors"`
		WriteErrors      *float64 `json:"write_errors"`
		ReadUncorrected  *float64 `json:"read_uncorrected"`
		WriteUncorrected *float64 `json:"write_uncorrected"`
	} `json:"disks"`
}

func readReader(path string, now time.Time, v *View) {
	const missing = "Temperatures and drive health need the reader the owner installs once per machine (tmx setup --temps)."
	st, err := os.Stat(path)
	if err != nil || st.Size() > maxReaderFile {
		v.TempsNote = missing
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		v.TempsNote = missing
		return
	}
	var f readerFile
	if err := json.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), &f); err != nil || f.At <= 0 {
		v.TempsNote = "The temperature reader's file could not be understood."
		return
	}
	at := time.Unix(f.At, 0)
	if age := now.Sub(at); age > readerMaxAge || age < -5*time.Minute {
		v.TempsNote = "The temperature reader has not written for a while: its last reading is too old to show."
		return
	}
	v.TempsAt = at.UnixMilli()
	v.CPUTempC, v.DiskTempC = temp(f.CPU), temp(f.Disk)
	for i, d := range f.Disks {
		if i == maxDisks {
			break
		}
		row := Disk{Name: clean(d.Name, 80), Health: clean(d.Health, 24), Status: clean(d.Status, 60), TempC: temp(d.Temp),
			PowerOnHours: count(d.Hours), ReadErrors: count(d.ReadErrors), WriteErrors: count(d.WriteErrors),
			ReadUncorrected: count(d.ReadUncorrected), WriteUncorrected: count(d.WriteUncorrected)}
		if w := d.Wear; w != nil && *w >= 0 && *w <= 100 {
			n := int(*w)
			row.WearPct = &n
		}
		if row.Name == "" {
			row.Name = "Drive " + strconv.Itoa(i+1)
		}
		v.Disks = append(v.Disks, row)
	}
}

// readSys reads Linux's thermal zones (the hottest is the CPU reading) and the drive
// temperatures the kernel exposes (drivetemp, nvme). There is no drive health here.
func readSys(root string, now time.Time, v *View) {
	hottest := func(paths []string, keep func(string) bool) *float64 {
		var best *float64
		for _, p := range paths {
			if keep != nil && !keep(p) {
				continue
			}
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			milli, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
			if err != nil {
				continue
			}
			c := milli / 1000
			if t := temp(&c); t != nil && (best == nil || *t > *best) {
				best = t
			}
		}
		return best
	}
	zones, _ := filepath.Glob(filepath.Join(root, "class", "thermal", "thermal_zone*", "temp"))
	v.CPUTempC = hottest(zones, nil)
	drives, _ := filepath.Glob(filepath.Join(root, "class", "hwmon", "hwmon*", "temp1_input"))
	v.DiskTempC = hottest(drives, func(p string) bool {
		name, err := os.ReadFile(filepath.Join(filepath.Dir(p), "name"))
		n := strings.TrimSpace(string(name))
		return err == nil && (n == "drivetemp" || n == "nvme")
	})
	if v.CPUTempC == nil && v.DiskTempC == nil {
		v.TempsNote = "This machine exposes no temperature sensors."
		return
	}
	v.TempsAt = now.UnixMilli()
}

// temp keeps a temperature a sensor can report (above 0 and below 150 degrees C): the reader
// writes 0 for a drive that reports none.
func temp(c *float64) *float64 {
	if c == nil || !(*c > 0 && *c < 150) {
		return nil
	}
	t := float64(int(*c*10+0.5)) / 10
	return &t
}

func count(n *float64) *int64 {
	if n == nil || *n < 0 || *n > 1e15 {
		return nil
	}
	c := int64(*n)
	return &c
}

// clean clamps a device's string and drops control and format characters: it is display text.
func clean(s string, n int) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s))
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return s
}

func runSMI(ctx context.Context) (string, error) {
	exe, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, smiTimeout)
	defer cancel()
	out, err := hiddenexec.CommandContext(ctx, exe,
		"--query-gpu=name,temperature.gpu,memory.used,memory.total,utilization.gpu,power.draw",
		"--format=csv,noheader,nounits").Output()
	return string(out), err
}

// ParseSMI reads the first card from nvidia-smi's csv,noheader,nounits answer to
// name,temperature.gpu,memory.used,memory.total,utilization.gpu,power.draw. A field the card
// does not report ("[N/A]") is absent.
func ParseSMI(text string) *GPU {
	for _, line := range strings.Split(text, "\n") {
		parts := strings.Split(line, ",")
		if len(parts) < 6 || strings.TrimSpace(parts[0]) == "" {
			continue
		}
		num := func(i int, lo, hi float64) *float64 {
			v, err := strconv.ParseFloat(strings.TrimSpace(parts[i]), 64)
			if err != nil || v < lo || v > hi {
				return nil
			}
			return &v
		}
		gib := func(mib *float64) *float64 {
			if mib == nil {
				return nil
			}
			g := float64(int(*mib/1024*100+0.5)) / 100
			return &g
		}
		return &GPU{Name: clean(parts[0], 80), TempC: num(1, 1, 149), UsedGB: gib(num(2, 0, 1<<24)), TotalGB: gib(num(3, 1, 1<<24)),
			UtilPct: num(4, 0, 100), PowerW: num(5, 0, 5000)}
	}
	return nil
}

// Clean returns v as display data from another machine: every string clamped and stripped of
// control characters, every number outside what hardware can report dropped, the drive list
// bounded.
func (v View) Clean() View {
	out := View{At: v.At, TempsAt: v.TempsAt, TempsNote: clean(v.TempsNote, 200), CPUTempC: temp(v.CPUTempC), DiskTempC: temp(v.DiskTempC), Disks: []Disk{}}
	if v.At < 0 || v.TempsAt < 0 {
		out.At, out.TempsAt = 0, 0
	}
	nonNeg := func(n *int64) *int64 {
		if n == nil || *n < 0 {
			return nil
		}
		c := *n
		return &c
	}
	for i, d := range v.Disks {
		if i == maxDisks {
			break
		}
		row := Disk{Name: clean(d.Name, 80), Health: clean(d.Health, 24), Status: clean(d.Status, 60), TempC: temp(d.TempC),
			PowerOnHours: nonNeg(d.PowerOnHours), ReadErrors: nonNeg(d.ReadErrors), WriteErrors: nonNeg(d.WriteErrors),
			ReadUncorrected: nonNeg(d.ReadUncorrected), WriteUncorrected: nonNeg(d.WriteUncorrected)}
		if w := d.WearPct; w != nil && *w >= 0 && *w <= 100 {
			n := *w
			row.WearPct = &n
		}
		out.Disks = append(out.Disks, row)
	}
	if g := v.GPU; g != nil && clean(g.Name, 80) != "" {
		in := func(n *float64, lo, hi float64) *float64 {
			if n == nil || !(*n >= lo && *n <= hi) {
				return nil
			}
			c := *n
			return &c
		}
		out.GPU = &GPU{Name: clean(g.Name, 80), TempC: in(g.TempC, 1, 149), UsedGB: in(g.UsedGB, 0, 1<<14), TotalGB: in(g.TotalGB, 0, 1<<14),
			UtilPct: in(g.UtilPct, 0, 100), PowerW: in(g.PowerW, 0, 5000)}
	}
	return out
}
