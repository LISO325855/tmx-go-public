package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/slots"
	"github.com/blackcode-ch/tmx-go/internal/sysinfo"
)

type slotRecoveryGrant struct {
	Lease slots.Lease
	Start int64
	Boot  string
}

type slotGrantResponse struct {
	slots.Lease
	Proof string `json:"recovery_proof,omitempty"`
}

func slotRecoveryRoute(r *http.Request) bool {
	return r.URL.Path == "/api/slots/recovery" &&
		(r.Method == http.MethodPost || r.Method == http.MethodDelete) && localRequest(r)
}

func (s *Server) signSlotGrant(payload string) []byte {
	derive := hmac.New(sha256.New, []byte(s.owner))
	derive.Write([]byte("tmx-slot-proof-v1"))
	mac := hmac.New(sha256.New, derive.Sum(nil))
	mac.Write([]byte("tmx-slot-grant-v2:" + payload))
	return mac.Sum(nil)
}

func (s *Server) writeSlotGrant(w http.ResponseWriter, r *http.Request, status int, l slots.Lease) {
	out := slotGrantResponse{Lease: l}
	if s.owner != "" && l.PID > 0 && localRequest(r) {
		out = s.slotGrant(l, sysinfo.ProcessStart(l.PID))
	}
	writeJSON(w, status, out)
}

func (s *Server) slotGrant(l slots.Lease, start int64) slotGrantResponse {
	out := slotGrantResponse{Lease: l}
	if start > 0 {
		raw, err := json.Marshal(slotRecoveryGrant{Lease: l, Start: start, Boot: s.slotBootID})
		if err == nil {
			payload := base64.RawURLEncoding.EncodeToString(raw)
			out.Proof = payload + "." + base64.RawURLEncoding.EncodeToString(s.signSlotGrant(payload))
		}
	}
	return out
}

func (s *Server) recoverSlot(w http.ResponseWriter, r *http.Request) {
	if !slotRecoveryRoute(r) || s.owner == "" {
		writeError(w, http.StatusForbidden, "slot recovery is local only")
		return
	}
	var req struct {
		Proof string `json:"proof"`
	}
	if !decode(w, r, &req) {
		return
	}
	payload, signature, _ := strings.Cut(req.Proof, ".")
	sig, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || !hmac.Equal(sig, s.signSlotGrant(payload)) {
		writeError(w, http.StatusForbidden, "invalid slot recovery proof")
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	var grant slotRecoveryGrant
	if err != nil || json.Unmarshal(raw, &grant) != nil || grant.Start <= 0 || grant.Boot == "" {
		writeError(w, http.StatusForbidden, "invalid slot recovery grant")
		return
	}
	l := grant.Lease
	if r.Method == http.MethodDelete {
		if err := s.slots.Release(l.ID, slots.Holder{AgentID: l.AgentID}); err != nil {
			writeSlotErr(w, err)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}
	earlier := grant.Boot != s.slotBootID
	if r.Context().Err() != nil || (earlier && sysinfo.ProcessStart(l.PID) != grant.Start) {
		writeSlotErr(w, slots.ErrNotFound)
		return
	}
	l, err = s.slots.Recover(l, earlier)
	if err == nil && l.State == slots.Waiting {
		l, err = s.slots.Wait(r.Context(), l.ID, slots.Holder{AgentID: l.AgentID}, 15*time.Second)
	}
	if err != nil {
		writeSlotErr(w, err)
		return
	}
	out := slotGrantResponse{Lease: l, Proof: req.Proof}
	if earlier || l.State != grant.Lease.State {
		out = s.slotGrant(l, grant.Start)
	}
	writeJSON(w, http.StatusOK, out)
}

func newSlotBootID() string { return model.NewID() }
