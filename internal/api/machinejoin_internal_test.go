package api

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/peers"
)

// The join code: one at a time, ten minutes, five wrong tries, used once; typed with or without
// its dashes, in either case.
func TestJoinCodeLifetime(t *testing.T) {
	var j joinState
	now := time.Unix(1794940000, 0)
	if j.check("ABCD-EFGH-JKMN", now) || j.consume("ABCD-EFGH-JKMN") {
		t.Fatal("a code was accepted before any was issued")
	}
	code, exp, err := j.issue(now)
	if err != nil || len(code) != joinCodeChars+2 || !exp.Equal(now.Add(joinCodeTTL)) {
		t.Fatalf("issue: %q %v %v", code, exp, err)
	}
	for _, r := range strings.ReplaceAll(code, "-", "") {
		if !strings.ContainsRune(joinAlphabet, r) {
			t.Fatalf("code %q has %q, which is not in the alphabet", code, r)
		}
	}
	typed := " " + strings.ToLower(strings.ReplaceAll(code, "-", " ")) + " "
	if !j.check(code, now) || !j.check(typed, now.Add(joinCodeTTL-time.Second)) {
		t.Fatal("the right code was refused")
	}
	if !j.check(code, now) { // checking does not use it up
		t.Fatal("a check consumed the code")
	}
	if !j.consume(typed) || j.consume(code) || j.check(code, now) {
		t.Fatal("the code must be used exactly once")
	}

	// A new code replaces the old one; five wrong tries end it, even for the right code after.
	old := code
	code, _, _ = j.issue(now)
	if j.check(old, now) && old != code {
		t.Fatal("the replaced code still works")
	}
	j.issue(now)
	code, _, _ = j.issue(now)
	for i := range joinMaxTries {
		if j.check("WRNG-WRNG-WRNG", now) {
			t.Fatalf("wrong code accepted on try %d", i)
		}
	}
	if j.check(code, now) {
		t.Fatal("the code survived five wrong tries")
	}

	// Ten minutes, not a second more.
	code, _, _ = j.issue(now)
	if j.check(code, now.Add(joinCodeTTL)) || j.check(code, now) {
		t.Fatal("an expired code was accepted (or came back to life)")
	}
	// Every letter of the alphabet turns up, and about equally often: no letter is favoured.
	seen := map[rune]int{}
	for range 2000 {
		c, _, _ := j.issue(now)
		for _, r := range strings.ReplaceAll(c, "-", "") {
			seen[r]++
		}
	}
	mean := 2000 * joinCodeChars / len(joinAlphabet)
	for _, r := range joinAlphabet {
		if n := seen[r]; n < mean*8/10 || n > mean*12/10 {
			t.Fatalf("letter %q drawn %d times, expected about %d", r, n, mean)
		}
	}
	// Two codes are never the same.
	a, _, _ := j.issue(now)
	b, _, _ := j.issue(now)
	if a == b {
		t.Fatal("two join codes were equal")
	}
}

func TestJoinAttemptLimit(t *testing.T) {
	var j joinState
	now := time.Now()
	for range 30 {
		if !j.allowAttempt(now) {
			t.Fatal("limited too early")
		}
	}
	if j.allowAttempt(now) {
		t.Fatal("missing rate limit")
	}
	j.issue(now) // a replacement code cannot reset the exchange rate limit
	if j.allowAttempt(now) {
		t.Fatal("issuing a code reset the rate limit")
	}
	if !j.allowAttempt(now.Add(time.Minute)) {
		t.Fatal("window did not reset")
	}
}

// A join only adds: a name that is listed (at any address, in any case) or that holds a pair
// token is already in the fleet, and so is a listed address under any name.
func TestInFleet(t *testing.T) {
	c := config.Config{Peers: []config.Peer{{Name: "beta", URL: "https://beta.example.ts.net"}}}
	tokens := peers.NewTokens(filepath.Join(t.TempDir(), "peers.json"))
	if _, err := tokens.Store("gamma", peers.NewPairToken(), "test"); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"beta": true, "BETA": true, "gamma": true, "delta": false} {
		if got := inFleet(c, tokens, name); got != want {
			t.Fatalf("inFleet(%q) = %v, want %v", name, got, want)
		}
	}
	if !addressInFleet(c, "https://BETA.example.ts.net") || addressInFleet(c, "https://delta.example.ts.net") {
		t.Fatal("addressInFleet")
	}
}
