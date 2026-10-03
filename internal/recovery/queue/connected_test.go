package queue

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/decomp"
)

func connectedTestProfile(sha string) ConnectedProfile {
	return ConnectedProfile{ID: "sub_008010", ROMSHA256: sha, Entry: 0x8010, Context: recovery.Context{E: "clear", M: "set", X: "set", C: "unknown"}, Spans: []decomp.CodeSpan{{Start: 0x8010, End: 0x8011}, {Start: 0x8020, End: 0x8021}, {Start: 0x8030, End: 0x8033}}, IndirectTargets: map[uint32][]uint32{0x8030: {0x8020}}, RefusalTargets: map[uint32]string{0x8040: "outside reviewed closure"}, MaxInstructions: 10, MaxSteps: 100}
}

func connectedTestPolicy(sha string) decomp.AdmissionPolicy {
	pin := hash([]byte("fixture"))
	root := decomp.CorpusTrustRoot{Label: "synthetic", EngineRevision: "synthetic", ROMSHA256: sha, FixtureSHA256: pin, FixtureReceiptSHA256: pin, FixtureSummarySHA256: pin, CaptureSHA256: pin, CaptureReceiptSHA256: pin, CaptureSummarySHA256: pin, HistorySHA256: pin, HistorySummarySHA256: pin, DecompressedSHA: pin}
	ranges := []decomp.AddressRange{{Start: 0x8010, End: 0x8011}, {Start: 0x8020, End: 0x8021}, {Start: 0x8030, End: 0x8033}}
	return decomp.AdmissionPolicy{Corpora: map[string]decomp.CorpusTrustRoot{"synthetic": root}, Routines: []decomp.RoutineContract{{ID: "sub_008010", Corpora: []string{"synthetic"}, ROMSHA256: sha, Entry: 0x8010, Calls: []decomp.RoutineCall{{Address: 0x8000, Resume: 0x8003, Opcode: 0x20}}, Returns: []decomp.RoutineReturn{{Address: 0x8010, Opcode: 0x60}}, Ranges: ranges, Kind: "connected_routine", Connected: &decomp.ConnectedContract{CallerPC: 0x8000, CallerOpcode: 0x20, ContinuationPC: 0x8003, Spans: ranges, HelperExitPC: 0x8030, AllowedIndirectTargets: map[uint32][]uint32{0x8030: {0x8020}}, HandlerEntryPC: 0x8020, HandlerReturnPC: 0x8020, TerminalReturnPC: 0x8010, ExpectedEntryS: 0x1f9, ExpectedReturnS: 0x1fb, StackReturnBytes: []byte{2, 0x80}}}}}
}

func TestConnectedProfileAndSingularPolicy(t *testing.T) {
	sha := hash([]byte("rom"))
	p := connectedTestProfile(sha)
	policy := connectedTestPolicy(sha)
	if err := validConnectedProfile(p, sha); err != nil {
		t.Fatal(err)
	}
	if _, err := connectedPolicy(p, policy); err != nil {
		t.Fatal(err)
	}
	p.IndirectTargets[0x8030] = []uint32{0x8020, 0x8010, 0x8030}
	if err := validConnectedProfile(p, sha); err == nil || !strings.Contains(err.Error(), "more than two targets") {
		t.Fatalf("third target: %v", err)
	}
	p = connectedTestProfile(sha)
	policy.Routines[0].Connected.HandlerEntryPC = 0x8050
	if _, err := connectedPolicy(p, policy); err == nil || !strings.Contains(err.Error(), "target differs") {
		t.Fatalf("policy mismatch: %v", err)
	}
	p = connectedTestProfile(sha)
	if err := validConnectedProfile(p, hash([]byte("other ROM"))); err == nil {
		t.Fatal("accepted substituted ROM")
	}
}

func TestConnectedAllRefusedReport(t *testing.T) {
	cfg := testConfig(t)
	rom, err := os.ReadFile(cfg.ROMPath)
	if err != nil {
		t.Fatal(err)
	}
	copy(rom[:3], []byte{0x20, 0x10, 0x80})
	rom[0x10] = 0x60
	rom[0x20] = 0x60
	copy(rom[0x30:0x33], []byte{0xdc, 0x00, 0x00})
	if err := os.WriteFile(cfg.ROMPath, rom, 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(cfg.ProjectDir, "recovery.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc recovery.Document
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc.ROM = recovery.ComputeROMIdentity(rom, rom, "none", "lorom")
	if err := writeJSON(filepath.Join(cfg.ProjectDir, "recovery.json"), doc); err != nil {
		t.Fatal(err)
	}
	cfg.Revision = recovery.ComputeProjectRevision(cfg.ProjectDir, &doc)
	sha := hash(rom)
	cfg.ConnectedProfilePath = filepath.Join(cfg.CorpusRoot, "profile.json")
	if err := writeJSON(cfg.ConnectedProfilePath, connectedTestProfile(sha)); err != nil {
		t.Fatal(err)
	}
	cfg.PolicyPath = filepath.Join(cfg.CorpusRoot, "policy.json")
	if err := writeJSON(cfg.PolicyPath, connectedTestPolicy(sha)); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(cfg.PolicyPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.PolicySHA256 = hash(data)
	c := decomp.ReplayCase{CaseID: "refused", RoutineID: "other-routine", ROMSHA256: sha, InitialState: decomp.CPUState{PC: 0x8010}}
	data, err = json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.CasesPath, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := RunConnected(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "blocked" || r.Cases != 1 || r.Refused != 1 || r.Admitted != 0 || r.SourceSHA256 != "" {
		t.Fatalf("unexpected report: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(cfg.OutDir, "generated.c")); !os.IsNotExist(err) {
		t.Fatalf("all-refused run generated C: %v", err)
	}
	var admissions []decomp.AdmissionRecord
	data, err = os.ReadFile(filepath.Join(cfg.OutDir, "admissions.json"))
	if err != nil || json.Unmarshal(data, &admissions) != nil || len(admissions) != 1 || admissions[0].Admitted {
		t.Fatalf("missing refusal record: %v %+v", err, admissions)
	}
}

func TestConnectedSourceArtifactBinding(t *testing.T) {
	dir := t.TempDir()
	r := &ConnectedReport{SourceSHA256: hash([]byte("original"))}
	if err := os.WriteFile(filepath.Join(dir, "generated.c"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkConnectedArtifacts(dir, r); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "generated.c"), []byte("mutated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkConnectedArtifacts(dir, r); err == nil {
		t.Fatal("accepted changed generated source")
	}
}
