package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const claudeOffersURL = "https://api.anthropic.com/api/oauth/usage?cedar_ember=1&skip_spend=1"

// The reader, rather than an arbitrary HTTP client, is the test seam.
type claudeOfferReader interface {
	ReadOffers(context.Context, string, string, string, time.Time) *FreeResets
}
type liveClaudeOfferReader struct{}

func (liveClaudeOfferReader) ReadOffers(ctx context.Context, cfg, cred, email string, now time.Time) *FreeResets {
	if resetTestProcess() {
		return unknownResets("Real offer reads are disabled under go test.")
	}
	return readClaudeOffersWithReader(ctx, liveOfferResponseReader{}, cfg, cred, email, now)
}

// This private interface is implemented by the guarded production reader and
// test-only fixtures. No HTTP client or transport can be supplied in Options.
type offerResponseReader interface {
	readOffer(*http.Request) (*http.Response, error)
}
type liveOfferResponseReader struct{}

func (liveOfferResponseReader) readOffer(req *http.Request) (*http.Response, error) {
	if resetTestProcess() {
		return nil, errors.New("real reset HTTP disabled under go test")
	}
	return offerClient().Do(req)
}

func resetTestProcess() bool { return testing.Testing() }

var liveOffersHTTPTransport = &http.Transport{Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second}

type liveOfferTransport struct{ dryRun bool }

func (t liveOfferTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if resetTestProcess() || t.dryRun {
		return nil, errors.New("real reset HTTP client disabled under go test or dry run")
	}
	if r.Method != http.MethodGet || r.URL.String() != claudeOffersURL {
		return nil, errors.New("only the fixed offer GET is allowed")
	}
	return liveOffersHTTPTransport.RoundTrip(r)
}

// The only direct Claude request in this package is this fixed GET. Never follow
// redirects or accept an environment-controlled URL with an account credential.
type liveOfferClient struct{ dryRun bool }

func offerClient(dryRun ...bool) liveOfferClient {
	return liveOfferClient{dryRun: len(dryRun) > 0 && dryRun[0]}
}
func (c liveOfferClient) Do(req *http.Request) (*http.Response, error) {
	if resetTestProcess() || c.dryRun {
		return nil, errors.New("real offer HTTP disabled for tests and dry runs")
	}
	client := &http.Client{Transport: liveOfferTransport{dryRun: c.dryRun}, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client.Do(req)
}

func readBoundedFile(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if int64(len(b)) > max {
		return nil, errors.New("file too large")
	}
	return b, err
}

// claudeFreeResets runs at the existing usage cadence, after the CLI has had the
// chance to refresh its own login. No token is persisted, logged, or refreshed by
// Go. File-backed Claude logins are supported; unavailable keychain logins remain
// unknown rather than borrowing another account's credential.
func (s *scope) claudeFreeResets(ctx context.Context, email string) *FreeResets {
	if s.o.ResetDryRun || s.resetDryRun {
		return unknownResets("Dry run: no live offer read.")
	}
	var dir string
	for _, v := range s.env() {
		k, value, ok := strings.Cut(v, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(k, "CLAUDE_CONFIG_DIR") {
			dir = value
		}
		// These can make auth status refer to a different credential or authority.
		for _, override := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_CUSTOM_OAUTH_URL", "CLAUDE_LOCAL_OAUTH_API_BASE"} {
			if strings.EqualFold(k, override) && value != "" {
				return unknownResets("Unknown: reset offers cannot use this login override.")
			}
		}
	}
	var configPath, credentialPath string
	if dir != "" {
		configPath = filepath.Join(dir, ".claude.json")
		credentialPath = filepath.Join(dir, ".credentials.json")
	} else {
		if !s.o.AllowUserLogins {
			return unknownResets("Unknown: reset offers have not been read yet.")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return unknownResets("Unknown: reset offers have not been read yet.")
		}
		configPath = filepath.Join(home, ".claude.json")
		credentialPath = filepath.Join(home, ".claude", ".credentials.json")
	}
	return s.offerReader.ReadOffers(ctx, configPath, credentialPath, email, s.o.Now())
}

func readClaudeOffersWithReader(ctx context.Context, reader offerResponseReader, configPath, credentialPath, email string, now time.Time) *FreeResets {
	identity := func() string {
		b, err := readBoundedFile(configPath, 16<<20)
		if err != nil {
			return ""
		}
		var cfg struct {
			Account struct {
				UUID  string `json:"accountUuid"`
				Email string `json:"emailAddress"`
			} `json:"oauthAccount"`
		}
		if json.Unmarshal(b, &cfg) != nil || email == "" || !strings.EqualFold(cfg.Account.Email, email) {
			return ""
		}
		return cfg.Account.UUID
	}
	id := identity()
	if id == "" {
		return unknownResets("Unknown: reset offers have not been read for this account.")
	}
	b, err := readBoundedFile(credentialPath, 1<<20)
	if err != nil {
		return unknownResets("Unknown: this Claude login is not available for the offer read.")
	}
	var credentials struct {
		OAuth struct {
			Access  string `json:"accessToken"`
			Expires int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(b, &credentials) != nil || credentials.OAuth.Access == "" || credentials.OAuth.Expires <= now.UnixMilli() {
		return unknownResets("Unknown: Claude needs to refresh its login before offers can be read.")
	}
	if identity() != id {
		return unknownResets("Unknown: the signed-in account changed.")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, claudeOffersURL, nil)
	if err != nil {
		return unknownResets("Unknown: could not read reset offers.")
	}
	req.Header.Set("Authorization", "Bearer "+credentials.OAuth.Access)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Accept", "application/json")
	resp, err := reader.readOffer(req)
	if err != nil {
		return unknownResets("Unknown: reset offer read failed; check again later.")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return unknownResets("Unknown: Claude did not confirm the reset offers; check again later.")
	}
	b, err = io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(b) > 1<<20 {
		return unknownResets("Unknown: could not read reset offers.")
	}
	if identity() != id {
		return unknownResets("Unknown: the signed-in account changed.")
	}
	offers := parseClaudeResetOffers(b, now)
	offers.accountID = id
	return offers
}
