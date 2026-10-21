package webui

import "net/http"

func (s *Server) registerScheduleRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/watch/schedules", s.bounded(s.authenticated(s.listWatchSchedules)))
}

// listWatchSchedules exposes only the static schedule projection needed to
// choose an independent location binding. It never returns coordinates or
// account credentials.
func (s *Server) listWatchSchedules(w http.ResponseWriter, r *http.Request) {
	account, ok := s.sessionAccount(r)
	if !ok {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return
	}
	items := []ScheduleView{}
	if s.cfg.Schedules != nil {
		if value := s.cfg.Schedules(account); value != nil {
			items = value
		}
	}
	termStart := ""
	if s.cfg.ScheduleTermStart != nil {
		termStart = s.cfg.ScheduleTermStart(account)
	}
	writeJSON(w, 200, map[string]any{
		"items": items, "count": len(items), "timezone": "Asia/Shanghai",
		"coordinate_system": "bd09", "term_start": termStart,
	})
}
