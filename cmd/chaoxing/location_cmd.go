package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/location"
	"github.com/DeliciousBuding/chaoxing/internal/statestore"
)

// locationCommand is the local-operator path for private coordinate presets.
// It writes only to the account's own SQLite store; coordinates and labels are
// deliberately never accepted through MCP or committed to the repository.
func locationCommand(stateDir string, args []string) error {
	if len(args) == 0 {
		return errors.New("location subcommand required: list, add, update, delete, bind, unbind, resolve")
	}
	store, err := statestore.Open(filepath.Join(stateDir, "chaoxing.db"))
	if err != nil {
		return errors.New("private state store is unavailable")
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch args[0] {
	case "list":
		fs := newLocationFlags("location list")
		account := fs.String("account", "root", "account key")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		presets, err := store.ListPresets(ctx, *account)
		if err != nil {
			return err
		}
		bindings, err := store.ListBindings(ctx, *account)
		if err != nil {
			return err
		}
		if presets == nil {
			presets = []location.Preset{}
		}
		if bindings == nil {
			bindings = []location.Binding{}
		}
		return emitLocation(map[string]any{"presets": presets, "bindings": bindings})
	case "add", "update":
		fs := newLocationFlags("location " + args[0])
		account := fs.String("account", "root", "account key")
		id := fs.Int64("id", 0, "preset id (update only)")
		label := fs.String("label", "", "human-readable label")
		address := fs.String("address", "", "optional address text")
		lat := fs.Float64("lat", 0, "latitude")
		lng := fs.Float64("lng", 0, "longitude")
		system := fs.String("system", location.CoordinateBD09, "coordinate system: bd09, gcj02 or wgs84")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if args[0] == "update" && *id <= 0 {
			return errors.New("location update requires --id")
		}
		item, err := store.SavePreset(ctx, *account, location.Preset{
			ID: *id, Label: *label, Address: *address,
			Latitude: *lat, Longitude: *lng, CoordinateSystem: *system,
		})
		if err != nil {
			return err
		}
		return emitLocation(map[string]any{"preset": item})
	case "delete":
		fs := newLocationFlags("location delete")
		account := fs.String("account", "root", "account key")
		id := fs.Int64("id", 0, "preset id")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if err := store.DeletePreset(ctx, *account, *id); err != nil {
			return err
		}
		return emitLocation(map[string]any{"deleted": true})
	case "bind":
		fs := newLocationFlags("location bind")
		account := fs.String("account", "root", "account key")
		course := fs.String("course", "", "course id")
		class := fs.String("class", "", "class id")
		schedule := fs.String("schedule", "", "optional schedule key; empty binds the whole class")
		preset := fs.Int64("preset", 0, "preset id")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if err := store.SaveBinding(ctx, *account, location.Binding{
			CourseID: *course, ClassID: *class, ScheduleKey: *schedule, PresetID: *preset,
		}); err != nil {
			return err
		}
		return emitLocation(map[string]any{"saved": true})
	case "unbind":
		fs := newLocationFlags("location unbind")
		account := fs.String("account", "root", "account key")
		course := fs.String("course", "", "course id")
		class := fs.String("class", "", "class id")
		schedule := fs.String("schedule", "", "optional schedule key")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if err := store.DeleteBinding(ctx, *account, *course, *class, *schedule); err != nil {
			return err
		}
		return emitLocation(map[string]any{"deleted": true})
	case "resolve":
		fs := newLocationFlags("location resolve")
		account := fs.String("account", "root", "account key")
		course := fs.String("course", "", "course id")
		class := fs.String("class", "", "class id")
		schedule := fs.String("schedule", "", "optional schedule key")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		value, err := store.Resolve(ctx, *account, *course, *class, *schedule)
		if err != nil {
			return err
		}
		if value == nil {
			return emitLocation(map[string]any{"resolved": false})
		}
		return emitLocation(map[string]any{"resolved": true, "latitude": value.Latitude, "longitude": value.Longitude, "address": value.Address})
	default:
		return errors.New("unknown location subcommand")
	}
}

func newLocationFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

func emitLocation(value map[string]any) error {
	value["ok"] = true
	return json.NewEncoder(os.Stdout).Encode(value)
}

// locationUsage is printed by the top-level help so operators can discover the
// private preset workflow without reading source.
func locationUsage() string {
	return strings.Join([]string{
		"location list    [--account root]",
		"location add     --label <name> --lat <bd09-lat> --lng <bd09-lng> [--address <text>] [--system bd09|gcj02|wgs84]",
		"location update  --id <id> --label <name> --lat <lat> --lng <lng> [--address <text>] [--system ...]",
		"location delete  --id <id>",
		"location bind    --course <id> --class <id> [--schedule <key>] --preset <id>",
		"location unbind  --course <id> --class <id> [--schedule <key>]",
		"location resolve --course <id> --class <id> [--schedule <key>]",
	}, "\n")
}
