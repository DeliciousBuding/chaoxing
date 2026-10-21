package statestore

import (
	"context"
	"testing"

	"github.com/DeliciousBuding/chaoxing/internal/location"
)

func TestLocationPresetAndBindingRoundTrip(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	preset, err := s.SavePreset(ctx, "root", location.Preset{
		Label: "Synthetic room", Address: "Synthetic address", Latitude: 39.90420, Longitude: 116.40740, CoordinateSystem: location.CoordinateBD09,
	})
	if err != nil {
		t.Fatal(err)
	}
	if preset.ID == 0 || preset.Account != "root" {
		t.Fatal(preset)
	}
	presets, err := s.ListPresets(ctx, "root")
	if err != nil || len(presets) != 1 || presets[0].Label != "Synthetic room" {
		t.Fatal(presets, err)
	}
	binding := location.Binding{CourseID: "course-1", ClassID: "class-1", PresetID: preset.ID}
	if err := s.SaveBinding(ctx, "root", binding); err != nil {
		t.Fatal(err)
	}
	bindings, err := s.ListBindings(ctx, "root")
	if err != nil || len(bindings) != 1 || bindings[0].PresetID != preset.ID {
		t.Fatal(bindings, err)
	}
	resolved, err := s.Resolve(ctx, "root", "course-1", "class-1", "")
	if err != nil || resolved == nil || resolved.Latitude != preset.Latitude || resolved.Longitude != preset.Longitude || resolved.Address != preset.Address {
		t.Fatal(resolved, err)
	}
	if err := s.DeleteBinding(ctx, "root", "course-1", "class-1", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePreset(ctx, "root", preset.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve(ctx, "root", "course-1", "class-1", ""); err != nil {
		t.Fatal(err)
	}
}

func TestResolveFallsBackToClassBinding(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	preset, err := s.SavePreset(ctx, "root", location.Preset{Label: "Class room", Latitude: 39.9042, Longitude: 116.4074})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveBinding(ctx, "root", location.Binding{CourseID: "course-1", ClassID: "class-1", PresetID: preset.ID}); err != nil {
		t.Fatal(err)
	}
	resolved, err := s.Resolve(ctx, "root", "course-1", "class-1", "Mon-08:00-09:40")
	if err != nil || resolved == nil || resolved.Latitude != 39.9042 {
		t.Fatal(resolved, err)
	}
}

func TestLocationPresetValidation(t *testing.T) {
	s, _ := open(t)
	if _, err := s.SavePreset(context.Background(), "root", location.Preset{Latitude: 39.9, Longitude: 116.4}); err == nil {
		t.Fatal("empty label accepted")
	}
	if _, err := s.SavePreset(context.Background(), "root", location.Preset{Label: "bad", Latitude: 91, Longitude: 116.4}); err == nil {
		t.Fatal("invalid coordinate accepted")
	}
}

func TestDeletePresetCascadesBindings(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	preset, err := s.SavePreset(ctx, "root", location.Preset{Label: "Cascade", Latitude: 39.9042, Longitude: 116.4074})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveBinding(ctx, "root", location.Binding{CourseID: "c", ClassID: "k", PresetID: preset.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePreset(ctx, "root", preset.ID); err != nil {
		t.Fatal(err)
	}
	bindings, err := s.ListBindings(ctx, "root")
	if err != nil || len(bindings) != 0 {
		t.Fatal(bindings, err)
	}
}
