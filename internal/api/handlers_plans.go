package api

import (
	"net/http"

	"github.com/google/uuid"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/db/sqlc"
)

type planJSON struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	CPURequestM   int32     `json:"cpu_request_m"`
	CPULimitM     int32     `json:"cpu_limit_m"`
	MemoryLimitMB int32     `json:"memory_limit_mb"`
	StorageGB     int32     `json:"storage_gb"`
	Active        bool      `json:"active"`
}

func toPlanJSON(p sqlc.Plan) planJSON {
	return planJSON{
		ID:            p.ID,
		Name:          p.Name,
		CPURequestM:   p.CpuRequestM,
		CPULimitM:     p.CpuLimitM,
		MemoryLimitMB: p.MemoryLimitMb,
		StorageGB:     p.StorageGb,
		Active:        p.Active,
	}
}

func (s *Server) listActivePlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.store.ListActivePlans(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := make([]planJSON, len(plans))
	for i, p := range plans {
		out[i] = toPlanJSON(p)
	}
	writeJSON(w, http.StatusOK, out)
}
