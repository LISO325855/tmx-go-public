package api

import (
	"net/http"

	"github.com/blackcode-ch/tmx-go/internal/fleet"
)

// POST /api/matrix/threads/{id}/owner {act, note}: the owner acts on a Board row
// (fleet/matrix_owner.go): a note, park, not_relevant, done or thanks. Owner only.
func (s *Server) matrixOwnerAct(w http.ResponseWriter, r *http.Request) {
	var req fleet.OwnerActRequest
	if !decode(w, r, &req) {
		return
	}
	res, err := s.f.OwnerAct(r.PathValue("id"), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
