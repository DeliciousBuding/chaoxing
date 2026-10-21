package webui

import (
	"context"
	"net/http"
	"testing"

	"github.com/DeliciousBuding/chaoxing/internal/access"

	"github.com/DeliciousBuding/chaoxing/internal/location"
	"strconv"
)

type fakeLocationStore struct {
	account string
	presets map[int64]location.Preset
	nextID  int64
	binding location.Binding
}

func (f *fakeLocationStore) ListPresets(_ context.Context, account string) ([]location.Preset, error) {
	if account != f.account {
		return []location.Preset{}, nil
	}
	out := make([]location.Preset, 0, len(f.presets))
	for _, preset := range f.presets {
		out = append(out, preset)
	}
	return out, nil
}

func (f *fakeLocationStore) SavePreset(_ context.Context, account string, preset location.Preset) (location.Preset, error) {
	if account != f.account {
		return location.Preset{}, context.Canceled
	}
	normalized, err := location.ValidateAndNormalize(preset)
	if err != nil {
		return location.Preset{}, err
	}
	f.nextID++
	normalized.ID = f.nextID
	normalized.Account = account
	if f.presets == nil {
		f.presets = map[int64]location.Preset{}
	}
	f.presets[normalized.ID] = normalized
	return normalized, nil
}

func (f *fakeLocationStore) DeletePreset(_ context.Context, account string, id int64) error {
	if account != f.account || f.presets[id].ID == 0 {
		return context.Canceled
	}
	delete(f.presets, id)
	return nil
}

func (f *fakeLocationStore) ListBindings(_ context.Context, account string) ([]location.Binding, error) {
	if account != f.account {
		return []location.Binding{}, nil
	}
	return []location.Binding{f.binding}, nil
}

func (f *fakeLocationStore) SaveBinding(_ context.Context, account string, binding location.Binding) error {
	if account != f.account {
		return context.Canceled
	}
	f.binding = binding
	return nil
}

func (f *fakeLocationStore) DeleteBinding(context.Context, string, string, string, string) error {
	return nil
}

func (f *fakeLocationStore) Resolve(_ context.Context, account, courseID, classID, scheduleKey string) (*location.Location, error) {
	if account != f.account {
		return nil, nil
	}
	if f.presets[f.binding.PresetID].ID == 0 || courseID != f.binding.CourseID || classID != f.binding.ClassID || scheduleKey != f.binding.ScheduleKey {
		return nil, nil
	}
	preset := f.presets[f.binding.PresetID]
	return &location.Location{Latitude: preset.Latitude, Longitude: preset.Longitude, Address: preset.Address}, nil
}

type fakeGeocoder struct{}

func (fakeGeocoder) Geocode(_ context.Context, address string) (location.Location, error) {
	return location.Location{Latitude: 39.9042, Longitude: 116.4074, Address: address}, nil
}

func (fakeGeocoder) ReverseGeocode(context.Context, float64, float64) (string, error) {
	return "湖南省长沙市岳麓区Synthetic road", nil
}

func newLocationTestServer(t *testing.T) (*http.ServeMux, *fakeLocationStore) {
	t.Helper()
	hash, err := access.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeLocationStore{account: "root"}
	server, err := New(Config{
		Version: "test", Issuer: "https://mcp.example.com", OwnerPasswordHash: hash,
		Location: store, Map: &location.MapConfig{Enabled: true, AK: "synthetic-ak", CenterLat: 39.9042, CenterLng: 116.4074, Zoom: 17},
		Geocoder: fakeGeocoder{},
		Execute:  func(context.Context, string, string, map[string]any) (any, error) { return nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server.Register(mux)
	return mux, store
}

func TestLocationRoutesRequireLoginAndCSRF(t *testing.T) {
	mux, _ := newLocationTestServer(t)
	if w := do(t, mux, "GET", "/api/locations", ""); w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := do(t, mux, "GET", "/api/map/config", ""); w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code, w.Body.String())
	}
	w := do(t, mux, "GET", "/api/auth/session", "")
	csrf := cookieValue(w, csrfCookie)
	csrfCookie := &http.Cookie{Name: csrfCookie, Value: csrf}
	w = doCSRF(t, mux, "POST", "/api/auth/login", `{"password":"correct horse battery staple"}`, csrf, csrfCookie)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	sessionCookie := &http.Cookie{Name: sessionCookie, Value: cookieValue(w, sessionCookie)}
	if sessionCookie.Value == "" {
		t.Fatal("login cookie missing")
	}
	w = do(t, mux, "POST", "/api/locations", `{"label":"Synthetic building","latitude":39.90420,"longitude":116.40740}`, csrfCookie, sessionCookie)
	if w.Code != http.StatusForbidden {
		t.Fatal("missing CSRF accepted", w.Code, w.Body.String())
	}
	w = doCSRF(t, mux, "POST", "/api/locations", `{"label":"Synthetic building","latitude":39.90420,"longitude":116.40740}`, csrf, csrfCookie, sessionCookie)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestLocationRoutesSaveResolveAndMapConfig(t *testing.T) {
	mux, store := newLocationTestServer(t)
	w := do(t, mux, "GET", "/api/auth/session", "")
	csrf := cookieValue(w, csrfCookie)
	csrfCookie := &http.Cookie{Name: csrfCookie, Value: csrf}
	w = doCSRF(t, mux, "POST", "/api/auth/login", `{"password":"correct horse battery staple"}`, csrf, csrfCookie)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	session := &http.Cookie{Name: sessionCookie, Value: cookieValue(w, sessionCookie)}

	w = doCSRF(t, mux, "POST", "/api/locations", `{"label":"Synthetic building","address":"Synthetic road","latitude":39.90259,"longitude":116.39135,"coordinate_system":"wgs84"}`, csrf, csrfCookie, session)
	value := jsonMap(t, w)
	if value["coordinate_system"] != "bd09" || value["latitude"].(float64) == 39.90259 {
		t.Fatal(value)
	}
	presetID := int64(value["id"].(float64))
	w = doCSRF(t, mux, "POST", "/api/location-bindings", `{"course_id":"c","class_id":"k","preset_id":`+strconv.FormatInt(presetID, 10)+`,"schedule_key":"Tue-08:00-09:40"}`, csrf, csrfCookie, session)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	w = doCSRF(t, mux, "POST", "/api/map/geocode", `{"address":"Synthetic university"}`, csrf, csrfCookie, session)
	value = jsonMap(t, w)
	if value["address"] != "Synthetic university" {
		t.Fatal(value)
	}
	w = do(t, mux, "GET", "/api/map/config", "", csrfCookie, session)
	value = jsonMap(t, w)
	if value["enabled"] != true || value["ak"] != "synthetic-ak" {
		t.Fatal(value)
	}
	item, err := store.Resolve(context.Background(), "root", "c", "k", "Tue-08:00-09:40")
	if err != nil || item == nil {
		t.Fatal(item, err)
	}
	if item.Longitude == 116.39135 {
		t.Fatal("WGS-84 coordinate was not normalized")
	}
}
