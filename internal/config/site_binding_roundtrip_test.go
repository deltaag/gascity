package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/gastownhall/gascity/internal/fsys"
)

// siteTomlWithUnknownContent seeds .gc/site.toml with tables and keys this gc
// binary does not model (future site overlays such as [control_plane] and
// [storage.bindings.*], an unknown top-level key, and an unknown per-rig key)
// alongside the known workspace identity and rig bindings.
const siteTomlWithUnknownContent = `workspace_name = "acme"
workspace_prefix = "ac"
future_flag = true

[control_plane]
url = "https://cp.example"

[storage.bindings.main]
backend = "doltlite"
path = "/var/data/main"

[[rig]]
name = "alpha"
path = "/srv/alpha"
pinned = true
`

const roundTripCityToml = `[workspace]
name = "acme"

[[rigs]]
name = "alpha"

[[agent]]
name = "worker"
`

func writeRoundTripCity(t *testing.T) (cityDir, cityPath string) {
	t.Helper()
	cityDir = t.TempDir()
	cityPath = filepath.Join(cityDir, "city.toml")
	if err := os.WriteFile(cityPath, []byte(roundTripCityToml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cityDir, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SiteBindingPath(cityDir), []byte(siteTomlWithUnknownContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return cityDir, cityPath
}

func loadRoundTripCity(t *testing.T, cityPath string) *City {
	t.Helper()
	cfg, err := Load(fsys.OSFS{}, cityPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := ApplySiteBindingsForEdit(fsys.OSFS{}, filepath.Dir(cityPath), cfg); err != nil {
		t.Fatalf("ApplySiteBindingsForEdit: %v", err)
	}
	return cfg
}

func decodeSiteTomlRaw(t *testing.T, cityDir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(SiteBindingPath(cityDir))
	if err != nil {
		t.Fatalf("reading site.toml: %v", err)
	}
	raw := map[string]any{}
	if _, err := toml.Decode(string(data), &raw); err != nil {
		t.Fatalf("decoding site.toml: %v\n%s", err, data)
	}
	return raw
}

func assertUnknownSiteContentPreserved(t *testing.T, raw map[string]any) {
	t.Helper()
	if got := raw["future_flag"]; got != true {
		t.Errorf("future_flag = %#v, want true; site.toml = %#v", got, raw)
	}
	cp, _ := raw["control_plane"].(map[string]any)
	if got := cp["url"]; got != "https://cp.example" {
		t.Errorf("control_plane.url = %#v, want https://cp.example; site.toml = %#v", got, raw)
	}
	storage, _ := raw["storage"].(map[string]any)
	bindings, _ := storage["bindings"].(map[string]any)
	main, _ := bindings["main"].(map[string]any)
	if main["backend"] != "doltlite" || main["path"] != "/var/data/main" {
		t.Errorf("storage.bindings.main = %#v, want backend=doltlite path=/var/data/main", main)
	}
}

func siteRigEntries(raw map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	rigs, _ := raw["rig"].([]map[string]any)
	for _, rig := range rigs {
		name, _ := rig["name"].(string)
		out[name] = rig
	}
	return out
}

// TestSiteBindingWritersPreserveUnknownContent covers every exported writer
// that rewrites .gc/site.toml: each must round-trip tables and keys it does
// not model instead of rebuilding the file from the known fields alone.
func TestSiteBindingWritersPreserveUnknownContent(t *testing.T) {
	cases := []struct {
		name string
		// write performs the rewrite; it returns whether rig "alpha" is
		// expected to still be bound afterwards.
		write func(t *testing.T, cityDir, cityPath string) (alphaBound bool)
	}{
		{name: "PersistRigSiteBindings", write: func(t *testing.T, cityDir, _ string) bool {
			err := PersistRigSiteBindings(fsys.OSFS{}, cityDir, []Rig{
				{Name: "alpha", Path: "/srv/alpha"},
				{Name: "beta", Path: "/srv/beta"},
			})
			if err != nil {
				t.Fatalf("PersistRigSiteBindings: %v", err)
			}
			return true
		}},
		{name: "PersistWorkspaceSiteBinding", write: func(t *testing.T, cityDir, _ string) bool {
			if err := PersistWorkspaceSiteBinding(fsys.OSFS{}, cityDir, "renamed", "rn"); err != nil {
				t.Fatalf("PersistWorkspaceSiteBinding: %v", err)
			}
			return true
		}},
		{name: "WriteCityAndRigSiteBindingsForEdit", write: func(t *testing.T, _, cityPath string) bool {
			cfg := loadRoundTripCity(t, cityPath)
			if err := WriteCityAndRigSiteBindingsForEdit(fsys.OSFS{}, cityPath, cfg); err != nil {
				t.Fatalf("WriteCityAndRigSiteBindingsForEdit: %v", err)
			}
			return true
		}},
		{name: "WriteCityAndRigSiteBindingsForEditRemovingRigs", write: func(t *testing.T, _, cityPath string) bool {
			cfg := loadRoundTripCity(t, cityPath)
			cfg.Rigs = nil
			if err := WriteCityAndRigSiteBindingsForEditRemovingRigs(fsys.OSFS{}, cityPath, cfg, "alpha"); err != nil {
				t.Fatalf("WriteCityAndRigSiteBindingsForEditRemovingRigs: %v", err)
			}
			return false
		}},
		{name: "AppendRigAndWriteSiteBindingsForEdit", write: func(t *testing.T, _, cityPath string) bool {
			cfg := loadRoundTripCity(t, cityPath)
			newRig := Rig{Name: "beta", Path: "/srv/beta"}
			cfg.Rigs = append(cfg.Rigs, newRig)
			if err := AppendRigAndWriteSiteBindingsForEdit(fsys.OSFS{}, cityPath, cfg, newRig); err != nil {
				t.Fatalf("AppendRigAndWriteSiteBindingsForEdit: %v", err)
			}
			return true
		}},
		{name: "WriteCityAgentSuspendedForEdit", write: func(t *testing.T, _, cityPath string) bool {
			cfg := loadRoundTripCity(t, cityPath)
			if err := WriteCityAgentSuspendedForEdit(fsys.OSFS{}, cityPath, cfg, "worker", true); err != nil {
				t.Fatalf("WriteCityAgentSuspendedForEdit: %v", err)
			}
			return true
		}},
		{name: "AppendAgentPatchSuspendedForEdit", write: func(t *testing.T, _, cityPath string) bool {
			cfg := loadRoundTripCity(t, cityPath)
			if err := AppendAgentPatchSuspendedForEdit(fsys.OSFS{}, cityPath, cfg, "", "worker", true); err != nil {
				t.Fatalf("AppendAgentPatchSuspendedForEdit: %v", err)
			}
			return true
		}},
		{name: "StripAgentPatchSuspendedForEdit", write: func(t *testing.T, _, cityPath string) bool {
			cfg := loadRoundTripCity(t, cityPath)
			if err := AppendAgentPatchSuspendedForEdit(fsys.OSFS{}, cityPath, cfg, "", "worker", true); err != nil {
				t.Fatalf("AppendAgentPatchSuspendedForEdit: %v", err)
			}
			cfg = loadRoundTripCity(t, cityPath)
			if err := StripAgentPatchSuspendedForEdit(fsys.OSFS{}, cityPath, cfg, "", "worker"); err != nil {
				t.Fatalf("StripAgentPatchSuspendedForEdit: %v", err)
			}
			return true
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cityDir, cityPath := writeRoundTripCity(t)
			alphaBound := tc.write(t, cityDir, cityPath)

			raw := decodeSiteTomlRaw(t, cityDir)
			assertUnknownSiteContentPreserved(t, raw)

			rigs := siteRigEntries(raw)
			alpha, ok := rigs["alpha"]
			if ok != alphaBound {
				t.Fatalf("rig alpha bound = %v, want %v; site.toml = %#v", ok, alphaBound, raw)
			}
			if alphaBound {
				if alpha["path"] != "/srv/alpha" {
					t.Errorf("rig alpha path = %#v, want /srv/alpha", alpha["path"])
				}
				if alpha["pinned"] != true {
					t.Errorf("rig alpha unknown key pinned = %#v, want true", alpha["pinned"])
				}
			}

			// The known fields must still load through the lenient decoder.
			if _, err := LoadSiteBinding(fsys.OSFS{}, cityDir); err != nil {
				t.Fatalf("LoadSiteBinding after rewrite: %v", err)
			}
		})
	}
}

// TestPersistSiteBindingKeepsFileWithOnlyUnknownContent verifies that
// clearing every known field does not delete .gc/site.toml while other
// tables remain: the file is removed only when nothing at all is left.
func TestPersistSiteBindingKeepsFileWithOnlyUnknownContent(t *testing.T) {
	cityDir, _ := writeRoundTripCity(t)

	if err := PersistWorkspaceSiteBinding(fsys.OSFS{}, cityDir, "", ""); err != nil {
		t.Fatalf("PersistWorkspaceSiteBinding: %v", err)
	}
	if err := persistRigSiteBindings(fsys.OSFS{}, cityDir, nil, rigNameSet([]string{"alpha"})); err != nil {
		t.Fatalf("persistRigSiteBindings: %v", err)
	}

	raw := decodeSiteTomlRaw(t, cityDir)
	assertUnknownSiteContentPreserved(t, raw)
	for _, key := range []string{"workspace_name", "workspace_prefix", "rig"} {
		if _, ok := raw[key]; ok {
			t.Errorf("known key %q = %#v survived clearing; want removed", key, raw[key])
		}
	}
}

// TestPersistSiteBindingRemovesFileWhenNothingRemains verifies the file is
// still deleted when the cleared binding leaves no content at all.
func TestPersistSiteBindingRemovesFileWhenNothingRemains(t *testing.T) {
	cityDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cityDir, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "workspace_name = \"acme\"\n\n[[rig]]\nname = \"alpha\"\npath = \"/srv/alpha\"\n"
	if err := os.WriteFile(SiteBindingPath(cityDir), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := PersistWorkspaceSiteBinding(fsys.OSFS{}, cityDir, "", ""); err != nil {
		t.Fatalf("PersistWorkspaceSiteBinding: %v", err)
	}
	if err := persistRigSiteBindings(fsys.OSFS{}, cityDir, nil, rigNameSet([]string{"alpha"})); err != nil {
		t.Fatalf("persistRigSiteBindings: %v", err)
	}
	if _, err := os.Stat(SiteBindingPath(cityDir)); !os.IsNotExist(err) {
		t.Fatalf("site.toml stat err = %v, want not-exist once nothing remains", err)
	}
}

// TestPersistSiteBindingKeepsEmptyUnknownTable verifies that even an empty
// unknown table counts as content worth keeping.
func TestPersistSiteBindingKeepsEmptyUnknownTable(t *testing.T) {
	cityDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cityDir, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "workspace_name = \"acme\"\n\n[control_plane]\n"
	if err := os.WriteFile(SiteBindingPath(cityDir), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PersistWorkspaceSiteBinding(fsys.OSFS{}, cityDir, "", ""); err != nil {
		t.Fatalf("PersistWorkspaceSiteBinding: %v", err)
	}
	raw := decodeSiteTomlRaw(t, cityDir)
	if _, ok := raw["control_plane"]; !ok {
		t.Fatalf("empty [control_plane] table dropped; site.toml = %#v", raw)
	}
	if _, ok := raw["workspace_name"]; ok {
		t.Fatalf("workspace_name survived clearing; site.toml = %#v", raw)
	}
}

// TestPersistSiteBindingRoundTripIsIdempotent verifies a no-op rewrite of a
// file carrying unknown content leaves it stable across repeated writes.
func TestPersistSiteBindingRoundTripIsIdempotent(t *testing.T) {
	cityDir, _ := writeRoundTripCity(t)
	rigs := []Rig{{Name: "alpha", Path: "/srv/alpha"}}
	if err := PersistRigSiteBindings(fsys.OSFS{}, cityDir, rigs); err != nil {
		t.Fatalf("first PersistRigSiteBindings: %v", err)
	}
	first, err := os.ReadFile(SiteBindingPath(cityDir))
	if err != nil {
		t.Fatal(err)
	}
	if err := PersistRigSiteBindings(fsys.OSFS{}, cityDir, rigs); err != nil {
		t.Fatalf("second PersistRigSiteBindings: %v", err)
	}
	second, err := os.ReadFile(SiteBindingPath(cityDir))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("repeated rewrite changed site.toml:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
