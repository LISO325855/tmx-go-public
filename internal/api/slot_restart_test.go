package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/slots"
)

func TestSlotProofSignedWithRawOwnerTokenIsRefused(t *testing.T) {
	paths := config.Paths{Home: t.TempDir()}
	token, err := config.OwnerToken(paths)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{owner: token, slots: slots.New(slots.Options{Sizes: map[string]int{"heavy": 2}}), slotBootID: newSlotBootID()}
	raw, err := json.Marshal(slotRecoveryGrant{
		Lease: slots.Lease{ID: "previous-lease", Kind: "heavy", State: slots.Held, PID: os.Getpid()},
		Start: 1, Boot: "earlier-boot",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("tmx-slot-grant-v2:" + payload))
	proof := payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	body, err := json.Marshal(map[string]string{"proof": proof})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/slots/recovery", bytes.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	out := httptest.NewRecorder()
	s.recoverSlot(out, r)
	if out.Code != http.StatusForbidden || len(s.slots.View().Leases) != 0 {
		t.Fatalf("raw-token signature must be refused: HTTP %d", out.Code)
	}
}

func TestSlotProofSurvivesDaemonRestart(t *testing.T) {
	paths := config.Paths{Home: t.TempDir()}
	start := func() *Server {
		key, err := config.OwnerToken(paths)
		if err != nil {
			t.Fatal(err)
		}
		return &Server{owner: key, slots: slots.New(slots.Options{Sizes: map[string]int{"heavy": 2}}), slotBootID: newSlotBootID()}
	}
	first := start()
	lease, err := first.slots.Acquire("heavy", slots.Holder{}, "surviving command", os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	local := func(method string, body []byte) *http.Request {
		r := httptest.NewRequest(method, "http://127.0.0.1/api/slots/recovery", bytes.NewReader(body))
		r.RemoteAddr = "127.0.0.1:12345"
		return r
	}
	granted := httptest.NewRecorder()
	first.writeSlotGrant(granted, local(http.MethodPost, nil), http.StatusCreated, lease)
	var grant slotGrantResponse
	if err := json.Unmarshal(granted.Body.Bytes(), &grant); err != nil || grant.Proof == "" {
		t.Fatalf("no proof issued: %v", err)
	}
	first.slots.Close()

	restarted := start()
	if restarted.slotBootID == first.slotBootID {
		t.Fatal("both boots used the same ID")
	}
	body, err := json.Marshal(map[string]string{"proof": grant.Proof})
	if err != nil {
		t.Fatal(err)
	}
	out := httptest.NewRecorder()
	restarted.recoverSlot(out, local(http.MethodPost, body))
	got := restarted.slots.View().Leases
	if out.Code != http.StatusOK || len(got) != 1 || got[0].ID != lease.ID || got[0].State != slots.Held {
		t.Fatalf("restart failed to verify the original proof: HTTP %d, leases %+v", out.Code, got)
	}
}
