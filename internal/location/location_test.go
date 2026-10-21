package location

import "testing"

func TestValidateAndNormalizeRejectsNullIsland(t *testing.T) {
	if _, err := ValidateAndNormalize(Preset{Label: "synthetic", Latitude: 0, Longitude: 0}); err == nil {
		t.Fatal("0,0 was accepted as a location")
	}
}

func TestValidateAndNormalizeAcceptsCampusCoordinate(t *testing.T) {
	got, err := ValidateAndNormalize(Preset{Label: "synthetic", Latitude: 31.23040, Longitude: 121.47370, CoordinateSystem: CoordinateBD09})
	if err != nil {
		t.Fatal(err)
	}
	if got.Latitude != 31.23040 || got.Longitude != 121.47370 || got.CoordinateSystem != CoordinateBD09 {
		t.Fatalf("unexpected normalized preset: %+v", got)
	}
}
