// Package location owns preset coordinates, coordinate-system conversion and
// the private location-binding contract used by the management UI and watcher.
package location

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"
)

const (
	CoordinateBD09  = "bd09"
	CoordinateGCJ02 = "gcj02"
	CoordinateWGS84 = "wgs84"
)

// Location is the normalized sign-in coordinate contract. The management UI
// and watcher always exchange BD-09 values after conversion.
type Location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Address   string  `json:"address,omitempty"`
}

// Preset is one persisted named location.
type Preset struct {
	ID               int64     `json:"id,omitempty"`
	Account          string    `json:"account,omitempty"`
	Label            string    `json:"label"`
	Address          string    `json:"address,omitempty"`
	Latitude         float64   `json:"latitude"`
	Longitude        float64   `json:"longitude"`
	CoordinateSystem string    `json:"coordinate_system,omitempty"`
	CreatedAt        time.Time `json:"created_at,omitempty"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
}

// Binding associates a preset with one course/class, optionally narrowed to a
// schedule key. Empty ScheduleKey means the class-wide fallback.
type Binding struct {
	Account     string    `json:"account,omitempty"`
	CourseID    string    `json:"course_id"`
	ClassID     string    `json:"class_id"`
	ScheduleKey string    `json:"schedule_key,omitempty"`
	PresetID    int64     `json:"preset_id"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

// Service persists and resolves private location presets.
type Service interface {
	ListPresets(ctx context.Context, account string) ([]Preset, error)
	SavePreset(ctx context.Context, account string, preset Preset) (Preset, error)
	DeletePreset(ctx context.Context, account string, id int64) error
	ListBindings(ctx context.Context, account string) ([]Binding, error)
	SaveBinding(ctx context.Context, account string, binding Binding) error
	DeleteBinding(ctx context.Context, account string, courseID, classID, scheduleKey string) error
	Resolve(ctx context.Context, account, courseID, classID, scheduleKey string) (*Location, error)
}

// Geocoder is the bounded map-provider adapter. Implementations must not log
// the API key or caller coordinates.
type Geocoder interface {
	Geocode(ctx context.Context, address string) (Location, error)
	ReverseGeocode(ctx context.Context, latitude, longitude float64) (string, error)
}

// MapConfig is the sanitized browser-map contract. AK is intentionally not
// persisted by this package; deployments keep it in private configuration.
type MapConfig struct {
	Enabled   bool    `json:"enabled"`
	AK        string  `json:"ak,omitempty"`
	CenterLat float64 `json:"center_latitude,omitempty"`
	CenterLng float64 `json:"center_longitude,omitempty"`
	Zoom      int     `json:"zoom,omitempty"`
}

// Resolver binds one account to a Service for the watcher.
type Resolver struct {
	service Service
	account string
}

func NewResolver(service Service, account string) *Resolver {
	return &Resolver{service: service, account: account}
}

func (r *Resolver) Resolve(ctx context.Context, courseID, classID, scheduleKey string) (*Location, error) {
	if r == nil || r.service == nil {
		return nil, nil
	}
	return r.service.Resolve(ctx, r.account, courseID, classID, scheduleKey)
}

// ValidateAndNormalize validates caller input and converts it to BD-09.
func ValidateAndNormalize(p Preset) (Preset, error) {
	p.Label = strings.TrimSpace(p.Label)
	p.Address = strings.TrimSpace(p.Address)
	p.CoordinateSystem = strings.ToLower(strings.TrimSpace(p.CoordinateSystem))
	if p.Label == "" {
		return Preset{}, errors.New("location label is required")
	}
	if len(p.Label) > 64 || len(p.Address) > 256 {
		return Preset{}, errors.New("location label or address is too long")
	}
	if p.CoordinateSystem == "" {
		p.CoordinateSystem = CoordinateBD09
	}
	switch p.CoordinateSystem {
	case CoordinateBD09, CoordinateGCJ02, CoordinateWGS84:
	default:
		return Preset{}, errors.New("coordinate_system must be bd09, gcj02 or wgs84")
	}
	if !finiteCoordinate(p.Latitude, p.Longitude) || p.Latitude < -90 || p.Latitude > 90 || p.Longitude < -180 || p.Longitude > 180 {
		return Preset{}, errors.New("location coordinates are outside range")
	}
	p.Latitude, p.Longitude = ToBD09(p.Latitude, p.Longitude, p.CoordinateSystem)
	p.CoordinateSystem = CoordinateBD09
	return p, nil
}

func finiteCoordinate(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}

// ToBD09 converts the supported coordinate systems to Baidu BD-09.
func ToBD09(latitude, longitude float64, system string) (float64, float64) {
	switch strings.ToLower(strings.TrimSpace(system)) {
	case CoordinateGCJ02:
		return GCJ02ToBD09(latitude, longitude)
	case CoordinateWGS84:
		gLat, gLng := WGS84ToGCJ02(latitude, longitude)
		return GCJ02ToBD09(gLat, gLng)
	default:
		return latitude, longitude
	}
}
