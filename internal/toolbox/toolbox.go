package toolbox

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// The declared list. It is set once at start (the private build: seed_private.go; tests:
// toolboxtest) and is the same on every node of a fleet. A build without one has an empty
// list. Lines written by agents (resource_set) will sit on top of it in the database.
var (
	seedMu sync.RWMutex
	seed   []Resource
)

// SetSeed replaces the declared list with the records in b (a JSON array), after checking
// every one.
func SetSeed(b []byte) error {
	var list []Resource
	if err := json.Unmarshal(b, &list); err != nil {
		return fmt.Errorf("resources seed: %w", err)
	}
	if err := ValidateList(list); err != nil {
		return err
	}
	for i := range list {
		list[i].Source = SourceDeclared
		if list[i].Actions == nil {
			list[i].Actions = []Action{}
		}
	}
	seedMu.Lock()
	seed = list
	seedMu.Unlock()
	return nil
}

// Seed is a copy of the declared list (empty when the build has none).
func Seed() []Resource {
	seedMu.RLock()
	defer seedMu.RUnlock()
	out := make([]Resource, len(seed))
	for i, r := range seed {
		r.Actions = append([]Action{}, r.Actions...)
		r.Secrets = append([]Secret(nil), r.Secrets...)
		out[i] = r
	}
	return out
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,26}$`)

// Shapes that must never be in a record (the spec: "Always a label, never the number or
// address", "No secrets shown"). Deliberately broad: a false alarm costs a rewording.
var (
	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+\.[A-Za-z]{2,}`)
	phoneRe = regexp.MustCompile(`\+?\d[\d \-/().]{6,}\d`)
	// Dates and times (2026-11-17, 2026-11-17T18:20:25Z) are not numbers to hide.
	dateRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}(T\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:\d{2})?)?`)
	// Long unbroken runs of key-like characters, and the usual key prefixes.
	tokenRe  = regexp.MustCompile(`[A-Za-z0-9_\-]{28,}|\b(sk|pk|ghp|gho|ghs|xox[abp]|AKIA|AIza|ya29)[-_A-Za-z0-9]{6,}`)
	secretRe = regexp.MustCompile(`(?i)\b(password|passwort|token|api[ _-]?key|secret|pin)\s*[:=]\s*\S+`)
)

// SecretShape names the first forbidden shape in s ("" = none): a full mail address, a phone
// number, a token or a "password: x" pair.
func SecretShape(s string) string {
	switch {
	case emailRe.MatchString(s):
		return "a mail address"
	case secretRe.MatchString(s):
		return "a password or key"
	case tokenRe.MatchString(s):
		return "a token"
	}
	for _, m := range phoneRe.FindAllString(dateRe.ReplaceAllString(s, " "), -1) {
		if digits(m) >= 7 {
			return "a phone number"
		}
	}
	return ""
}

func digits(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

func oneOf(v string, set ...string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}

// texts is every human-written string of a record, for the secret check.
func (r Resource) texts() []string {
	out := []string{r.ID, r.Name, r.Summary, r.Identity.Label, r.Identity.Person, r.Machine, r.Holder, r.HolderNote, r.Node,
		r.Maintainer, r.Parent, r.How, r.Rule.Text, r.Rule.ID, r.StateNote, r.VerifiedAt, r.VerifiedBy, r.Page}
	for _, a := range r.Actions {
		out = append(out, a.Name, a.Note, a.Route.Tool, a.Route.To)
	}
	for _, s := range r.Secrets {
		out = append(out, s.Name)
	}
	if r.Meter != nil {
		out = append(out, r.Meter.Label, r.Meter.Text)
	}
	if r.Health != nil {
		out = append(out, r.Health.Reason)
	}
	return out
}

// Validate checks one record: required fields, known values, and no secret shape anywhere.
func Validate(r Resource) error {
	bad := func(f string, a ...any) error { return fmt.Errorf("resource %q: %s", r.ID, fmt.Sprintf(f, a...)) }
	switch {
	case !idRe.MatchString(r.ID):
		return bad("the id must be 2-27 lowercase letters, digits and dashes")
	case strings.TrimSpace(r.Name) == "":
		return bad("no name")
	case !oneOf(r.Kind, Kinds...):
		return bad("unknown kind %q", r.Kind)
	case strings.TrimSpace(r.Summary) == "":
		return bad("no summary")
	case !oneOf(r.Identity.Who, "owner", "fleet", "other", "none"):
		return bad("identity.who must be owner, fleet, other or none")
	case !oneOf(r.State, States...):
		return bad("unknown state %q", r.State)
	case strings.TrimSpace(r.Rule.Text) == "":
		return bad("no rule")
	case !oneOf(r.Live, "", "whatsapp", "accounts", "companion"):
		return bad("unknown live source %q", r.Live)
	case r.Page != "" && !strings.HasPrefix(r.Page, "/"):
		return bad("page must be a hash path such as /usage")
	}
	for _, a := range r.Actions {
		switch {
		case strings.TrimSpace(a.Name) == "":
			return bad("an action has no name")
		case !oneOf(a.Type, ActionRead, ActionSendFleet, ActionSendOwner, ActionAct):
			return bad("action %q: unknown type %q", a.Name, a.Type)
		case !oneOf(a.Route.Kind, RouteTool, RouteMail, RouteNone):
			return bad("action %q: unknown route %q", a.Name, a.Route.Kind)
		case a.Route.Kind == RouteTool && a.Route.Tool == "":
			return bad("action %q: a tool route names its tool", a.Name)
		case a.Route.Kind == RouteMail && a.Route.To == "":
			return bad("action %q: a mail route names the holder", a.Name)
		}
	}
	for _, s := range r.texts() {
		if shape := SecretShape(s); shape != "" {
			return bad("holds %s; a record names things by label only", shape)
		}
	}
	return nil
}

// ValidateList checks every record, unique ids and that each parent exists.
func ValidateList(list []Resource) error {
	ids := map[string]bool{}
	for _, r := range list {
		if err := Validate(r); err != nil {
			return err
		}
		if ids[r.ID] {
			return fmt.Errorf("resource %q: listed twice", r.ID)
		}
		ids[r.ID] = true
	}
	for _, r := range list {
		if r.Parent != "" && !ids[r.Parent] {
			return fmt.Errorf("resource %q: its parent %q is not in the list", r.ID, r.Parent)
		}
	}
	return nil
}

// NeedsAttention: the lines that float to the top of the page (down, or not verified).
func NeedsAttention(r Resource) bool {
	return r.State == StateNotVerified || (r.Health != nil && r.Health.Level == HealthDown)
}

func kindRank(k string) int {
	for i, x := range Kinds {
		if x == k {
			return i
		}
	}
	return len(Kinds)
}

// Sort orders a list for reading: by kind (page order), then the lines needing attention,
// then as declared.
func Sort(list []Resource) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if ka, kb := kindRank(a.Kind), kindRank(b.Kind); ka != kb {
			return ka < kb
		}
		return NeedsAttention(a) && !NeedsAttention(b)
	})
}

// Filter keeps the records of a kind and a state ("" = any). State also takes "down":
// the records whose health check fails.
func Filter(list []Resource, kind, state string) []Resource {
	out := []Resource{}
	for _, r := range list {
		if kind != "" && r.Kind != kind {
			continue
		}
		switch {
		case state == "":
		case state == HealthDown:
			if r.Health == nil || r.Health.Level != HealthDown {
				continue
			}
		case r.State != state:
			continue
		}
		out = append(out, r)
	}
	return out
}

// Find returns the record with that id.
func Find(list []Resource, id string) (Resource, bool) {
	for _, r := range list {
		if r.ID == id {
			return r, true
		}
	}
	return Resource{}, false
}

// CheckFilter refuses an unknown kind or state, so a typo is not answered with an empty list.
func CheckFilter(kind, state string) error {
	if kind != "" && !oneOf(kind, Kinds...) {
		return fmt.Errorf("unknown kind %q: use %s", kind, strings.Join(Kinds, ", "))
	}
	if state != "" && state != HealthDown && !oneOf(state, States...) {
		return fmt.Errorf("unknown state %q: use %s or down", state, strings.Join(States, ", "))
	}
	return nil
}
