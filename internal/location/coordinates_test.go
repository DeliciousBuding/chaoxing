package location

import (
	"math"
	"testing"
)

func TestBD09GCJ02RoundTrip(t *testing.T) {
	latitude, longitude := 39.90420, 116.40740
	gLat, gLng := BD09ToGCJ02(latitude, longitude)
	bLat, bLng := GCJ02ToBD09(gLat, gLng)
	if math.Abs(latitude-bLat) > 0.00001 || math.Abs(longitude-bLng) > 0.00001 {
		t.Fatalf("round trip: %.8f %.8f -> %.8f %.8f", latitude, longitude, bLat, bLng)
	}
}

func TestWGS84GCJ02IsSmallInChina(t *testing.T) {
	latitude, longitude := 39.90259, 116.39135
	gLat, gLng := WGS84ToGCJ02(latitude, longitude)
	if math.Abs(latitude-gLat) > 0.01 || math.Abs(longitude-gLng) > 0.01 {
		t.Fatalf("unexpected delta: %.8f %.8f", latitude-gLat, longitude-gLng)
	}
}

func TestValidateAndNormalizeConvertsToBD09(t *testing.T) {
	preset, err := ValidateAndNormalize(Preset{
		Label: "Synthetic", Address: "Synthetic", Latitude: 39.90259, Longitude: 116.39135, CoordinateSystem: CoordinateWGS84,
	})
	if err != nil {
		t.Fatal(err)
	}
	if preset.CoordinateSystem != CoordinateBD09 {
		t.Fatal(preset.CoordinateSystem)
	}
	if preset.Latitude == 39.90259 && preset.Longitude == 116.39135 {
		t.Fatal("coordinate was not converted")
	}
}
