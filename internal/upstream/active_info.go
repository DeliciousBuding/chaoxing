package upstream

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
)

// Published centers can be numbers, numeric strings, or empty strings when
// no center is supplied. Empty values must not invalidate a configured preset.
func (info *activeInfo) UnmarshalJSON(body []byte) error {
	type plain activeInfo
	var payload struct {
		plain
		Latitude  json.RawMessage `json:"locationLatitude"`
		Longitude json.RawMessage `json:"locationLongitude"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}
	*info = activeInfo(payload.plain)
	var err error
	if info.LocationLat, err = optionalCoordinate(payload.Latitude); err != nil {
		return err
	}
	info.LocationLng, err = optionalCoordinate(payload.Longitude)
	return err
}

func optionalCoordinate(raw json.RawMessage) (*float64, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return nil, nil
	}
	if strings.HasPrefix(text, `"`) {
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, err
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, nil
		}
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil, errors.New("invalid published coordinate")
	}
	return &value, nil
}
