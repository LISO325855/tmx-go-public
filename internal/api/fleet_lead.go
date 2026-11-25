package api

import (
	"net/http"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

func (s *Server) fleetLeadRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/config/fleet-lead", s.ownerOnly(s.getFleetLead))
	mux.HandleFunc("PUT /api/config/fleet-lead", s.ownerOnly(s.putFleetLead))
}

func (s *Server) getFleetLead(w http.ResponseWriter, r *http.Request) {
	c, err := config.Load(s.f.Paths())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"fleet_lead": c.FleetLead})
}

func (s *Server) putFleetLead(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FleetLead string `json:"fleet_lead"`
	}
	if !decodeMax(w, r, &req, 4096) {
		return
	}
	address, err := config.FleetLeadAddress(req.FleetLead)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.RequireOwner(w, r, "set this node's verified fleet lead", address) {
		return
	}
	s.peerEditMu.Lock()
	defer s.peerEditMu.Unlock()
	c, err := config.Load(s.f.Paths())
	if err != nil {
		writeErr(w, err)
		return
	}
	if address != "" {
		_, host, _ := strings.Cut(address, "@")
		if !c.Production() || s.peers == nil || !s.peers.Paired(host) {
			writeError(w, http.StatusConflict, "fleet lead must be an agent on a paired node")
			return
		}
	}
	c.FleetLead = address
	if err := config.Save(s.f.Paths(), c); err != nil {
		writeErr(w, err)
		return
	}
	s.f.SetFleetLead(address)
	s.log.Info("fleet lead setting updated", "fleet_lead", address)
	writeJSON(w, http.StatusOK, map[string]string{"fleet_lead": address})
}
