package queue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/candidates"
	"github.com/tmc/snes/internal/recovery/decomp"
)

func TestPreviewNamedBindingUsesRefusalFrontier(t *testing.T) {
	cfg := testConfig(t)
	rom, err := os.ReadFile(cfg.ROMPath)
	if err != nil {
		t.Fatal(err)
	}
	copy(rom, []byte{0xa5, 0x10, 0x60})
	if err := os.WriteFile(cfg.ROMPath, rom, 0600); err != nil {
		t.Fatal(err)
	}
	identity := recovery.ComputeROMIdentity(rom, rom, "none", "lorom")
	if err := writeJSON(filepath.Join(cfg.ProjectDir, "recovery.json"), recovery.NewDocument(identity)); err != nil {
		t.Fatal(err)
	}
	c := decomp.ReplayCase{RoutineID: "bounded", ROMSHA256: identity.NormalizedSHA256, InitialState: decomp.CPUState{PC: 0x8000, P: 0x30}}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.CasesPath, append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	p := candidates.Proposal{Entry: 0x8000, Start: 0x8000, End: 0x8003, RefusalFrontiers: []candidates.Frontier{{From: 0x8000, Target: 0x8002, Reason: "reviewed_boundary"}}}
	cfg.CandidatePath = filepath.Join(filepath.Dir(cfg.CasesPath), "candidate.json")
	if err := writeJSON(cfg.CandidatePath, map[string]any{"id": "bounded", "proposal": p}); err != nil {
		t.Fatal(err)
	}
	symbols := []decomp.ByteSymbol{{Name: "observed_byte", Address: 0x7e0010, Evidence: "authored source"}}
	cfg.NamedSymbolsPath = filepath.Join(filepath.Dir(cfg.CasesPath), "symbols.json")
	if err := writeJSON(cfg.NamedSymbolsPath, symbols); err != nil {
		t.Fatal(err)
	}
	got, err := PreviewNamedBinding(cfg)
	if err != nil {
		t.Fatal(err)
	}
	region, err := decodeCandidateRegion(rom, p, entryContext(c.InitialState), cfg.MaxSteps)
	if err != nil {
		t.Fatal(err)
	}
	region.ROMBytes = nil
	want, err := decomp.NamedRegionBindingSHA256(region, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("preview digest %s differs from replay digest %s", got, want)
	}
	p.RefusalFrontiers = nil
	other, err := decodeCandidateRegion(rom, p, entryContext(c.InitialState), cfg.MaxSteps)
	if err != nil {
		t.Fatal(err)
	}
	other.ROMBytes = nil
	without, err := decomp.NamedRegionBindingSHA256(other, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if got == without {
		t.Fatal("refusal frontier did not distinguish binding digest")
	}
}
