package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/peers"
)

// SetHostTimeoutForTest widens hostTimeout for the duration of one test (restore with the
// returned func, typically via t.Cleanup) - fleethosts.go, bc #214: configuredVoiceHosts'
// 2s probe deadline is a real UX/stall bound in production and must not grow just because
// go test -race is slower; this lets exactly one test ask for more room instead.
func SetHostTimeoutForTest(d time.Duration) (restore func()) {
	prev := hostTimeout
	hostTimeout = d
	return func() { hostTimeout = prev }
}

// SetJoinForTest lets a test's two daemons reach each other on loopback (machinejoin.go): an
// address check that accepts the test servers, and a client for them. Restore with the
// returned func.
func SetJoinForTest(url func(string) (string, error), c *http.Client) (restore func()) {
	prevURL, prevClient := joinPeerURL, joinClient
	joinPeerURL, joinClient = url, c
	return func() { joinPeerURL, joinClient = prevURL, prevClient }
}

// FailJoinTokenStoreForTest makes storing a joined machine's token fail (machinejoin.go), as a
// full disk would. Restore with the returned func.
func FailJoinTokenStoreForTest() (restore func()) {
	prev := storeJoinToken
	storeJoinToken = func(*peers.Tokens, string, string) (string, error) { return "", errors.New("disk full") }
	return func() { storeJoinToken = prev }
}
