package main

import (
	"path/filepath"
	"testing"

	"github.com/DeliciousBuding/chaoxing/internal/location"
	"github.com/DeliciousBuding/chaoxing/internal/statestore"
)

func TestLocationCommandRoundTrip(t *testing.T) {
	stateDir := t.TempDir()
	if err := locationCommand(stateDir, []string{
		"add", "--label", "synthetic-lab", "--lat", "39.9087", "--lng", "116.3975",
		"--address", "synthetic address",
	}); err != nil {
		t.Fatal(err)
	}
	store, err := statestore.Open(filepath.Join(stateDir, "chaoxing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	presets, err := store.ListPresets(t.Context(), "root")
	if err != nil || len(presets) != 1 {
		t.Fatalf("presets=%v err=%v", presets, err)
	}
	preset := presets[0]
	if preset.Label != "synthetic-lab" || preset.CoordinateSystem != location.CoordinateBD09 {
		t.Fatal(preset)
	}
	if preset.Latitude != 39.9087 || preset.Longitude != 116.3975 {
		t.Fatal(preset)
	}

	if err := locationCommand(stateDir, []string{
		"bind", "--course", "course-1", "--class", "class-1", "--preset", "1",
	}); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.Resolve(t.Context(), "root", "course-1", "class-1", "")
	if err != nil || resolved == nil || resolved.Latitude != 39.9087 {
		t.Fatalf("resolved=%v err=%v", resolved, err)
	}
	if err := locationCommand(stateDir, []string{
		"unbind", "--course", "course-1", "--class", "class-1",
	}); err != nil {
		t.Fatal(err)
	}
	if resolved, err := store.Resolve(t.Context(), "root", "course-1", "class-1", ""); err != nil || resolved != nil {
		t.Fatalf("binding survived unbind: %v %v", resolved, err)
	}
}

func TestLocationCommandConvertsCoordinateSystems(t *testing.T) {
	stateDir := t.TempDir()
	if err := locationCommand(stateDir, []string{
		"add", "--label", "wgs", "--lat", "39.9042", "--lng", "116.4074", "--system", "wgs84",
	}); err != nil {
		t.Fatal(err)
	}
	store, err := statestore.Open(filepath.Join(stateDir, "chaoxing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	presets, err := store.ListPresets(t.Context(), "root")
	if err != nil || len(presets) != 1 {
		t.Fatal(presets, err)
	}
	if presets[0].Latitude == 39.9042 && presets[0].Longitude == 116.4074 {
		t.Fatal("wgs84 input was stored without conversion")
	}
}

func TestLocationCommandRejectsInvalidInput(t *testing.T) {
	stateDir := t.TempDir()
	for _, args := range [][]string{
		{"add", "--lat", "39.9", "--lng", "116.4"},
		{"add", "--label", "x", "--lat", "999", "--lng", "116.4"},
		{"bind", "--course", "c", "--class", "k"},
		{"unknown"},
	} {
		if err := locationCommand(stateDir, args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
