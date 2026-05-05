package parity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const parityGateManifestPath = "testdata/parity_gate_manifest.json"

type parityGateManifest struct {
	TripleParity      tripleParityManifest `json:"triple_parity"`
	WriteTrace        writeTraceManifest   `json:"write_trace"`
	TableDrivenParity []romSmokeManifest   `json:"table_driven_parity"`
}

type tripleParityManifest struct {
	ROM            string   `json:"rom"`
	ROMPathHint    string   `json:"rom_path_hint"`
	Frames         int      `json:"frames"`
	MemoryRegions  []string `json:"memory_regions"`
	ReferenceCores []string `json:"reference_cores"`
	Comment        string   `json:"comment"`
}

type romSmokeManifest struct {
	Name           string   `json:"name"`
	ROM            string   `json:"rom"`
	Frames         int      `json:"frames"`
	ReferenceCores []string `json:"reference_cores"`
	Expectations   []string `json:"expectations"`
	Comment        string   `json:"comment"`
}

type writeTraceManifest struct {
	ROM               string   `json:"rom"`
	ROMPathHint       string   `json:"rom_path_hint"`
	Frames            int      `json:"frames"`
	MemoryRegions     []string `json:"memory_regions"`
	ReferenceCores    []string `json:"reference_cores"`
	Audio             bool     `json:"audio"`
	AudioRMSTolerance float64  `json:"audio_rms_tolerance"`
	KnownDivergences  []string `json:"known_divergences,omitempty"`
	Comment           string   `json:"comment"`
}

func TestParityGateManifest(t *testing.T) {
	manifest := readParityGateManifest(t)
	if err := validateParityGateManifest(manifest); err != nil {
		t.Fatal(err)
	}
}

func readParityGateManifest(t *testing.T) parityGateManifest {
	raw, err := os.ReadFile(parityGateManifestPath)
	if err != nil {
		t.Skipf("no parity gate manifest at %s: %v", parityGateManifestPath, err)
	}
	var manifest parityGateManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse %s: %v", parityGateManifestPath, err)
	}
	if err := validateParityGateManifest(manifest); err != nil {
		t.Fatalf("validate %s: %v", parityGateManifestPath, err)
	}
	return manifest
}

func validateParityGateManifest(manifest parityGateManifest) error {
	if err := validateTripleParityManifest(manifest.TripleParity); err != nil {
		return err
	}
	if err := validateWriteTraceManifest(manifest.WriteTrace); err != nil {
		return err
	}
	if len(manifest.TableDrivenParity) == 0 {
		return fmt.Errorf("table_driven_parity is empty")
	}
	seen := make(map[string]bool)
	for _, rom := range manifest.TableDrivenParity {
		if err := validateROMSmokeManifest(rom); err != nil {
			return err
		}
		if seen[rom.ROM] {
			return fmt.Errorf("%s: duplicate rom", rom.ROM)
		}
		seen[rom.ROM] = true
	}
	return nil
}

func validateWriteTraceManifest(manifest writeTraceManifest) error {
	if manifest.ROM == "" {
		return fmt.Errorf("write_trace: rom is empty")
	}
	if filepath.Base(manifest.ROM) != manifest.ROM {
		return fmt.Errorf("write_trace: rom must be a basename")
	}
	if manifest.ROMPathHint == "" {
		return fmt.Errorf("write_trace: rom_path_hint is empty")
	}
	if filepath.IsAbs(manifest.ROMPathHint) {
		return fmt.Errorf("write_trace: rom_path_hint must be relative")
	}
	if manifest.Frames <= 0 {
		return fmt.Errorf("write_trace: frames must be positive")
	}
	if manifest.Frames > 60 {
		return fmt.Errorf("write_trace: frames must be <= 60")
	}
	if err := validateCoreNames("write_trace", manifest.ReferenceCores); err != nil {
		return err
	}
	if !hasCore(manifest.ReferenceCores, "bsnes") || !hasCore(manifest.ReferenceCores, "snes9x") {
		return fmt.Errorf("write_trace: reference_cores must include bsnes and snes9x")
	}
	if len(manifest.MemoryRegions) == 0 {
		return fmt.Errorf("write_trace: memory_regions is empty")
	}
	for _, region := range manifest.MemoryRegions {
		if region != "WRAM" && region != "VRAM" && region != "CGRAM" {
			return fmt.Errorf("write_trace: unknown memory region %q", region)
		}
	}
	if manifest.Audio && manifest.AudioRMSTolerance <= 0 {
		return fmt.Errorf("write_trace: audio_rms_tolerance must be positive when audio is enabled")
	}
	if strings.TrimSpace(manifest.Comment) == "" {
		return fmt.Errorf("write_trace: comment is empty")
	}
	return nil
}

func validateTripleParityManifest(manifest tripleParityManifest) error {
	if manifest.ROM == "" {
		return fmt.Errorf("triple_parity: rom is empty")
	}
	if filepath.Base(manifest.ROM) != manifest.ROM {
		return fmt.Errorf("triple_parity: rom must be a basename")
	}
	if manifest.ROMPathHint == "" {
		return fmt.Errorf("triple_parity: rom_path_hint is empty")
	}
	if filepath.IsAbs(manifest.ROMPathHint) {
		return fmt.Errorf("triple_parity: rom_path_hint must be relative")
	}
	if manifest.Frames <= 0 {
		return fmt.Errorf("triple_parity: frames must be positive")
	}
	if manifest.Frames > 60 {
		return fmt.Errorf("triple_parity: frames must be <= 60")
	}
	if err := validateCoreNames("triple_parity", manifest.ReferenceCores); err != nil {
		return err
	}
	if len(manifest.MemoryRegions) == 0 {
		return fmt.Errorf("triple_parity: memory_regions is empty")
	}
	if strings.TrimSpace(manifest.Comment) == "" {
		return fmt.Errorf("triple_parity: comment is empty")
	}
	return nil
}

func validateROMSmokeManifest(manifest romSmokeManifest) error {
	if manifest.Name == "" {
		return fmt.Errorf("%s: name is empty", manifest.ROM)
	}
	if manifest.ROM == "" {
		return fmt.Errorf("%s: rom is empty", manifest.Name)
	}
	if filepath.Base(manifest.ROM) != manifest.ROM {
		return fmt.Errorf("%s: rom must be a basename", manifest.Name)
	}
	if manifest.Frames <= 0 {
		return fmt.Errorf("%s: frames must be positive", manifest.ROM)
	}
	if manifest.Frames > 60 {
		return fmt.Errorf("%s: frames must be <= 60", manifest.ROM)
	}
	if err := validateCoreNames(manifest.ROM, manifest.ReferenceCores); err != nil {
		return err
	}
	if len(manifest.Expectations) == 0 {
		return fmt.Errorf("%s: expectations is empty", manifest.ROM)
	}
	if strings.TrimSpace(manifest.Comment) == "" {
		return fmt.Errorf("%s: comment is empty", manifest.ROM)
	}
	return nil
}

func validateCoreNames(label string, cores []string) error {
	if len(cores) == 0 {
		return fmt.Errorf("%s: reference_cores is empty", label)
	}
	seen := make(map[string]bool)
	for _, core := range cores {
		if core != "bsnes" && core != "snes9x" && core != "ares" {
			return fmt.Errorf("%s: unknown core %q", label, core)
		}
		if seen[core] {
			return fmt.Errorf("%s: duplicate core %q", label, core)
		}
		seen[core] = true
	}
	return nil
}

func hasCore(cores []string, want string) bool {
	for _, core := range cores {
		if core == want {
			return true
		}
	}
	return false
}
