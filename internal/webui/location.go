package webui

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/DeliciousBuding/chaoxing/internal/location"
)

func (s *Server) registerLocationRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/locations", s.bounded(s.authenticated(s.listLocations)))
	mux.Handle("POST /api/locations", s.bounded(s.authenticated(s.saveLocation)))
	mux.Handle("PUT /api/locations/{id}", s.bounded(s.authenticated(s.updateLocation)))
	mux.Handle("DELETE /api/locations/{id}", s.bounded(s.authenticated(s.deleteLocation)))
	mux.Handle("GET /api/location-bindings", s.bounded(s.authenticated(s.listLocationBindings)))
	mux.Handle("POST /api/location-bindings", s.bounded(s.authenticated(s.saveLocationBinding)))
	mux.Handle("DELETE /api/location-bindings", s.bounded(s.authenticated(s.deleteLocationBinding)))
	mux.Handle("GET /api/map/config", s.bounded(s.authenticated(s.mapConfig)))
	mux.Handle("POST /api/map/geocode", s.bounded(s.authenticated(s.geocode)))
	mux.Handle("POST /api/map/reverse-geocode", s.bounded(s.authenticated(s.reverseGeocode)))
}

func (s *Server) sessionAccount(r *http.Request) (string, bool) {
	id, ok := r.Context().Value(sessionIDKey{}).(string)
	if !ok || id == "" {
		return "", false
	}
	s.mu.Lock()
	session, exists := s.sessions[id]
	s.mu.Unlock()
	if !exists || session.account == "" {
		return "", false
	}
	return session.account, true
}

func (s *Server) requireLocationService(w http.ResponseWriter, r *http.Request) (string, bool) {
	if s.cfg.Location == nil {
		writeError(w, 501, "LOCATION_DISABLED", "location storage is not configured")
		return "", false
	}
	if !s.requireCapability(w, r, "sensitive") {
		return "", false
	}
	account, ok := s.sessionAccount(r)
	if !ok {
		writeError(w, 401, "UNAUTHENTICATED", "login required")
		return "", false
	}
	return account, true
}

func (s *Server) requireLocationWrite(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !s.requireCapability(w, r, "accounts") {
		return "", false
	}
	return s.requireLocationService(w, r)
}

func (s *Server) listLocations(w http.ResponseWriter, r *http.Request) {
	account, ok := s.requireLocationService(w, r)
	if !ok {
		return
	}
	items, err := s.cfg.Location.ListPresets(r.Context(), account)
	if err != nil {
		writeError(w, 500, "LOCATION_READ_FAILED", "location storage is unavailable")
		return
	}
	if items == nil {
		items = []location.Preset{}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) saveLocation(w http.ResponseWriter, r *http.Request) {
	account, ok := s.requireLocationWrite(w, r)
	if !ok {
		return
	}
	var preset location.Preset
	if !decodeBody(w, r, &preset) {
		return
	}
	preset.ID = 0
	item, err := s.cfg.Location.SavePreset(r.Context(), account, preset)
	if err != nil {
		writeError(w, 400, "INVALID_LOCATION", err.Error())
		return
	}
	s.recordAudit(r, s.requestSession(r), "config", "location.preset.create", strconv.FormatInt(item.ID, 10), account, true, map[string]any{"preset_id": item.ID, "label": item.Label})
	writeJSON(w, 200, item)
}

func (s *Server) updateLocation(w http.ResponseWriter, r *http.Request) {
	account, ok := s.requireLocationWrite(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "INVALID_ARGUMENT", "location id is invalid")
		return
	}
	var preset location.Preset
	if !decodeBody(w, r, &preset) {
		return
	}
	preset.ID = id
	item, err := s.cfg.Location.SavePreset(r.Context(), account, preset)
	if err != nil {
		writeError(w, 400, "INVALID_LOCATION", err.Error())
		return
	}
	s.recordAudit(r, s.requestSession(r), "config", "location.preset.update", strconv.FormatInt(item.ID, 10), account, true, map[string]any{"preset_id": item.ID, "label": item.Label})
	writeJSON(w, 200, item)
}

func (s *Server) deleteLocation(w http.ResponseWriter, r *http.Request) {
	account, ok := s.requireLocationWrite(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "INVALID_ARGUMENT", "location id is invalid")
		return
	}
	if err := s.cfg.Location.DeletePreset(r.Context(), account, id); err != nil {
		writeError(w, 400, "INVALID_LOCATION", err.Error())
		return
	}
	s.recordAudit(r, s.requestSession(r), "config", "location.preset.delete", strconv.FormatInt(id, 10), account, true, nil)
	writeJSON(w, 200, map[string]any{"deleted": true})
}

func (s *Server) listLocationBindings(w http.ResponseWriter, r *http.Request) {
	account, ok := s.requireLocationService(w, r)
	if !ok {
		return
	}
	items, err := s.cfg.Location.ListBindings(r.Context(), account)
	if err != nil {
		writeError(w, 500, "LOCATION_READ_FAILED", "location storage is unavailable")
		return
	}
	if items == nil {
		items = []location.Binding{}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) saveLocationBinding(w http.ResponseWriter, r *http.Request) {
	account, ok := s.requireLocationWrite(w, r)
	if !ok {
		return
	}
	var binding location.Binding
	if !decodeBody(w, r, &binding) {
		return
	}
	if err := s.cfg.Location.SaveBinding(r.Context(), account, binding); err != nil {
		writeError(w, 400, "INVALID_LOCATION_BINDING", err.Error())
		return
	}
	s.recordAudit(r, s.requestSession(r), "config", "location.binding.save", binding.CourseID+":"+binding.ClassID+":"+binding.ScheduleKey, account, true, map[string]any{"preset_id": binding.PresetID})
	writeJSON(w, 200, map[string]any{"saved": true})
}

func (s *Server) deleteLocationBinding(w http.ResponseWriter, r *http.Request) {
	account, ok := s.requireLocationWrite(w, r)
	if !ok {
		return
	}
	var req struct {
		CourseID    string `json:"course_id"`
		ClassID     string `json:"class_id"`
		ScheduleKey string `json:"schedule_key"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := s.cfg.Location.DeleteBinding(r.Context(), account, req.CourseID, req.ClassID, req.ScheduleKey); err != nil {
		writeError(w, 400, "INVALID_LOCATION_BINDING", err.Error())
		return
	}
	s.recordAudit(r, s.requestSession(r), "config", "location.binding.delete", req.CourseID+":"+req.ClassID+":"+req.ScheduleKey, account, true, nil)
	writeJSON(w, 200, map[string]any{"deleted": true})
}

func (s *Server) mapConfig(w http.ResponseWriter, r *http.Request) {
	if !s.requireCapability(w, r, "accounts") {
		return
	}
	value := map[string]any{"enabled": false}
	if s.cfg.Map != nil && s.cfg.Map.Enabled && s.cfg.Map.AK != "" {
		value = map[string]any{
			"enabled":          true,
			"ak":               s.cfg.Map.AK,
			"center_latitude":  s.cfg.Map.CenterLat,
			"center_longitude": s.cfg.Map.CenterLng,
			"zoom":             s.cfg.Map.Zoom,
		}
		if value["zoom"].(int) == 0 {
			value["zoom"] = 17
		}
	}
	writeJSON(w, 200, value)
}

func (s *Server) geocode(w http.ResponseWriter, r *http.Request) {
	if !s.requireCapability(w, r, "accounts") {
		return
	}
	if s.cfg.Geocoder == nil {
		writeError(w, 501, "MAP_DISABLED", "map geocoding is not configured")
		return
	}
	var req struct {
		Address string `json:"address"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	item, err := s.cfg.Geocoder.Geocode(r.Context(), req.Address)
	if err != nil {
		writeError(w, 502, "GEOCODE_FAILED", err.Error())
		return
	}
	writeJSON(w, 200, item)
}

func (s *Server) reverseGeocode(w http.ResponseWriter, r *http.Request) {
	if !s.requireCapability(w, r, "accounts") {
		return
	}
	if s.cfg.Geocoder == nil {
		writeError(w, 501, "MAP_DISABLED", "map geocoding is not configured")
		return
	}
	var req struct {
		Latitude         *float64 `json:"latitude"`
		Longitude        *float64 `json:"longitude"`
		CoordinateSystem string   `json:"coordinate_system"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Latitude == nil || req.Longitude == nil {
		writeError(w, 400, "INVALID_ARGUMENT", "latitude and longitude are required")
		return
	}
	system := strings.ToLower(strings.TrimSpace(req.CoordinateSystem))
	if system == "" {
		system = location.CoordinateBD09
	}
	switch system {
	case location.CoordinateBD09, location.CoordinateGCJ02, location.CoordinateWGS84:
	default:
		writeError(w, 400, "INVALID_ARGUMENT", "coordinate_system must be bd09, gcj02 or wgs84")
		return
	}
	latitude, longitude := location.ToBD09(*req.Latitude, *req.Longitude, system)
	address, err := s.cfg.Geocoder.ReverseGeocode(r.Context(), latitude, longitude)
	if err != nil {
		writeError(w, 502, "REVERSE_GEOCODE_FAILED", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"address": address})
}
