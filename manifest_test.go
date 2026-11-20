package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/prusa3d/prusa-connect/sdk/extension"
	"github.com/prusa3d/prusa-connect/sdk/manifest"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
)

func parsed(t *testing.T) manifest.Manifest {
	t.Helper()

	document, err := os.ReadFile("module.yaml")
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}

	held, err := manifest.Parse("module.yaml", document, yaml.Unmarshal)
	if err != nil {
		t.Fatalf("the host would refuse this manifest: %v", err)
	}

	return held
}

func TestTheManifestIsOneTheHostAccepts(t *testing.T) {
	t.Parallel()

	held := parsed(t)

	if held.UI == nil || held.UI.Entry != "ui/index.js" {
		t.Fatalf("the ui block reads as %+v", held.UI)
	}

	for _, slot := range []manifest.SlotName{manifest.SlotDeviceCard, manifest.SlotDeviceSection} {
		sections := held.UI.Contributes.Sections[slot]
		if len(sections) != 1 || sections[0].ID != "power" {
			t.Errorf("the %s slot reads as %+v", slot, sections)
		}
	}

	if string(held.Version) != string(release) {
		t.Errorf("the manifest says %s and main.go says %s", held.Version, release)
	}
}

func TestTheOptionsAreTheThreeTheProcessReads(t *testing.T) {
	t.Parallel()

	held := parsed(t)

	want := map[string]struct {
		kind     extension.OptionType
		required bool
	}{
		"ha_url":   {extension.OptionURL, true},
		"ha_token": {extension.OptionSecret, true},
		"switches": {extension.OptionObject, false},
	}

	if len(held.Options) != len(want) {
		t.Fatalf("the options read as %+v", held.Options)
	}

	for _, option := range held.Options {
		expected, ok := want[option.Key]
		if !ok || option.Type != expected.kind || option.Required != expected.required {
			t.Errorf("the option %s reads as %+v", option.Key, option)
		}
	}
}

func TestTheEgressNamesTheHomeAssistantOptionOnItsPort(t *testing.T) {
	t.Parallel()

	held := parsed(t)

	if len(held.Egress) != 1 {
		t.Fatalf("the egress reads as %+v", held.Egress)
	}

	one := held.Egress[0]
	if one.Option != "ha_url" || !slices.Contains(one.Ports, 8123) || one.From != manifest.EgressFromProcess {
		t.Errorf("the egress reads as %+v", one)
	}
}

// TestTheModuleAsksAndTakesNothingAsItself holds the two checks the routes ask
// the host, and no permission of the module's own.
func TestTheModuleAsksAndTakesNothingAsItself(t *testing.T) {
	t.Parallel()

	held := parsed(t)

	if len(held.Checks) != 2 || held.Checks[0] != actionObserve || held.Checks[1] != actionOperate {
		t.Errorf("the checks read as %+v", held.Checks)
	}

	if len(held.Permissions) != 0 {
		t.Errorf("the module asks for permissions it does not need: %+v", held.Permissions)
	}
}

func TestTheEgressCoversAHomeAssistantBehindAProxy(t *testing.T) {
	t.Parallel()

	ports := parsed(t).Egress[0].Ports
	for _, want := range []int{8123, 443, 80} {
		if !slices.Contains(ports, want) {
			t.Errorf("the egress does not list port %d: %v", want, ports)
		}
	}
}

func TestThePiIsAPlatform(t *testing.T) {
	t.Parallel()

	for _, platform := range parsed(t).Platforms {
		if platform == "linux_arm64" {
			return
		}
	}

	t.Error("linux_arm64 is not among the platforms")
}

func TestTheFragmentIsOneTheHostAccepts(t *testing.T) {
	t.Parallel()

	if _, err := routesapi.ParseFragment(openAPI); err != nil {
		t.Fatalf("the host would refuse the fragment: %v", err)
	}
}

func TestTheLibraryRangeHoldsTheVersionTheLibraryIs(t *testing.T) {
	t.Parallel()

	held := parsed(t)

	wanted, err := manifest.ParseRange(held.UI.Library)
	if err != nil {
		t.Fatalf("the library range is not one the host reads: %v", err)
	}

	packaged, err := os.ReadFile(filepath.Join(".core", "packages", "svelte", "package.json"))
	if err != nil {
		t.Fatalf("read the library's package.json: %v", err)
	}

	var library struct {
		Version string `json:"version"`
	}

	if err = json.Unmarshal(packaged, &library); err != nil {
		t.Fatal(err)
	}

	inside, err := wanted.Includes(extension.Version(library.Version))
	if err != nil || !inside {
		t.Errorf("the manifest asks for %q and the library is %s (%v)", held.UI.Library, library.Version, err)
	}
}
