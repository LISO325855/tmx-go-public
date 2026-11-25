package team

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Check results.
const (
	Pass       = "pass"
	Fail       = "fail"
	NotChecked = "not_checked"
)

// Check is one clause of the contract (v1 has ten, C1 … C10) for one computer.
type Check struct {
	ID     string `json:"id"`     // "C1" … "C10"
	Title  string `json:"title"`  // plain words
	Result string `json:"result"` // pass | fail | not_checked
	Detail string `json:"detail,omitempty"`
}

// Input is what the checker knows about one computer: its latest report, whether the
// signature of the envelope that carried it holds, who signed it, and the peer ID the group
// lists for that computer.
type Input struct {
	Report    Report
	HasReport bool
	SignedBy  string // the envelope's From
	SigOK     bool   // the envelope's signature verified (Node.VerifyEnvelope)
	Member    string // the peer ID the group lists for this computer
	At        int64  // when it was signed (Envelope.At, Unix ms)
	Now       int64
}

// Checks runs contract v1 on one computer. Only the clauses a status report can show are
// checked: C2 same peer ID, C3 a fresh, signed, complete report, C4 the three channels, C6 the
// upstream release; the others need more than a report and are "not checked".
func Checks(in Input) []Check {
	out := make([]Check, 0, 10)
	for i := 1; i <= 10; i++ {
		out = append(out, Check{ID: fmt.Sprintf("C%d", i), Title: "Needs more than a status report", Result: NotChecked})
	}
	set := func(i int, title string, ok bool, detail string) {
		c := Check{ID: out[i-1].ID, Title: title, Result: Fail, Detail: detail}
		if ok {
			c.Result, c.Detail = Pass, ""
		}
		out[i-1] = c
	}
	const (
		t2 = "Same peer ID"
		t3 = "Recent, signed, complete report"
		t4 = "Phone, e-mail and WhatsApp connected"
		t6 = "Based on " + Floor + " or newer"
	)
	if !in.HasReport {
		for i, t := range map[int]string{2: t2, 3: t3, 4: t4, 6: t6} {
			set(i, t, false, "no report yet")
		}
		return out
	}
	r := in.Report
	switch {
	case in.SignedBy != in.Member:
		set(2, t2, false, "signed by another peer ID than the group lists")
	case !in.SigOK:
		set(2, t2, false, "the signature does not hold")
	default:
		set(2, t2, true, "")
	}
	var why []string
	if age := time.Duration(in.Now-in.At) * time.Millisecond; age >= Fresh {
		why = append(why, "older than 45 minutes")
	}
	if !in.SigOK {
		why = append(why, "signature not valid")
	}
	if p := Problems(r); len(p) > 0 {
		why = append(why, p...)
	}
	set(3, t3, len(why) == 0, strings.Join(why, "; "))
	var missing []string
	if r.Phone.State != Connected {
		missing = append(missing, "phone")
	}
	if r.Email.State != Connected {
		missing = append(missing, "e-mail")
	}
	if r.WhatsApp.State != Connected {
		missing = append(missing, "WhatsApp")
	}
	set(4, t4, len(missing) == 0, "not connected: "+strings.Join(missing, ", "))
	floor, _ := ParseBuild(Floor)
	b, ok := ParseBuild(r.Node.BasedOn)
	switch {
	case r.Node.BasedOn == FleetLine:
		// The fleet build has its own release line: its versions do not compare with the
		// upstream floor, and it is newer than that floor by construction.
		set(6, "A fleet build (its own release line)", true, "")
	case !ok:
		set(6, t6, false, fmt.Sprintf("%q is not a release", r.Node.BasedOn))
	case b.Less(floor):
		set(6, t6, false, "based on "+r.Node.BasedOn)
	default:
		set(6, t6, true, "")
	}
	return out
}

// Passed counts the passing checks.
func Passed(cs []Check) (passed, checked int) {
	for _, c := range cs {
		if c.Result != NotChecked {
			checked++
		}
		if c.Result == Pass {
			passed++
		}
	}
	return passed, checked
}

// Build is a parsed version: vMAJOR.MINOR.PATCH[-N-gSHA] (N = commits after the tag). A
// leading "v" is optional and anything after the patch other than -N-g… is ignored, so a
// team's own builds ("v0.1.18-team.2") compare by their release.
type Build struct{ Major, Minor, Patch, Ahead int }

var buildRx = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-(\d+)-g[0-9a-f]{4,40})?`)

func ParseBuild(s string) (Build, bool) {
	m := buildRx.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Build{}, false
	}
	n := func(i int) int { v, _ := strconv.Atoi(m[i]); return v }
	b := Build{Major: n(1), Minor: n(2), Patch: n(3)}
	if m[4] != "" {
		b.Ahead = n(4)
	}
	return b, true
}

func (b Build) Less(o Build) bool {
	x := [4]int{b.Major, b.Minor, b.Patch, b.Ahead}
	y := [4]int{o.Major, o.Minor, o.Patch, o.Ahead}
	for i := range x {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return false
}
