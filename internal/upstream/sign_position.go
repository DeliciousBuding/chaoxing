package upstream

import "encoding/json"

// One coordinate sample supplies every representation in a submission.
func signPosition(activity Activity, info activeInfo, req SignRequest, tight bool, trace *signAuditTrace) (locationResultPayload, error) {
	hasCenter := info.LocationLat != nil && info.LocationLng != nil
	if (info.LocationLat == nil) != (info.LocationLng == nil) {
		return locationResultPayload{}, fault("UPSTREAM_SCHEMA", "published location is incomplete")
	}
	if hasCenter && !validCoordinate(*info.LocationLat, *info.LocationLng) {
		return locationResultPayload{}, fault("UPSTREAM_SCHEMA", "published location is invalid")
	}
	var lat, lng float64
	if req.Latitude != nil && req.Longitude != nil {
		lat, lng = *req.Latitude, *req.Longitude
		if !validCoordinate(lat, lng) {
			return locationResultPayload{}, fault("INVALID_ARGUMENT", "location is outside range")
		}
		if hasCenter && info.LocationRange.ok && info.LocationRange.value > 0 && coordinateDistanceMeters(lat, lng, *info.LocationLat, *info.LocationLng) > float64(info.LocationRange.value) {
			return locationResultPayload{}, fault("INVALID_ARGUMENT", "preset location is outside the activity range")
		}
	} else if hasCenter {
		lat, lng = *info.LocationLat, *info.LocationLng
	} else {
		return locationResultPayload{}, fault("UNSUPPORTED", "location input is unavailable")
	}
	lat, lng = jitterLocation(lat, lng, tight)
	p := locationResultPayload{Result: 1, Latitude: lat, Longitude: lng, Address: firstNonBlank(req.Address, activity.Course, "课堂位置")}
	if trace != nil {
		trace.Address = p.Address
		a, b := roundCoordinate(lat, 6), roundCoordinate(lng, 6)
		trace.Latitude, trace.Longitude = &a, &b
	}
	return p, nil
}

func positionJSON(p *locationResultPayload, mock bool) string {
	value := *p
	value.Latitude, value.Longitude = roundCoordinate(value.Latitude, 6), roundCoordinate(value.Longitude, 6)
	value.MockData = ""
	if mock {
		value.MockData = `{"strategy":0,"probability":-1}`
	}
	b, _ := json.Marshal(value)
	return string(b)
}

func appendPositionResult(pairs []string, p *locationResultPayload) []string {
	if p != nil {
		return append(pairs, "locationResult", positionJSON(p, true))
	}
	return pairs
}

func appendPosition(pairs []string, p *locationResultPayload, result bool) []string {
	if p == nil {
		return pairs
	}
	pairs = append(pairs, "location", positionJSON(p, false))
	if result {
		pairs = appendPositionResult(pairs, p)
	}
	return pairs
}
