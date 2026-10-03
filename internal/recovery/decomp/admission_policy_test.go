package decomp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/snes/internal/recovery"
)

func policyControlFixture(t *testing.T) (AdmissionPolicy, []byte, RoutineContract, []captureCPUInsn, *captureData) {
	t.Helper()
	rom := make([]byte, 65536)
	rom[0x8000] = 0x20
	rom[0x8001] = 0x00
	rom[0x8002] = 0x81
	rom[0x8003] = 0xea
	rom[0x8100] = 0xea
	rom[0x8101] = 0x60
	sha := fmt.Sprintf("%x", sha256.Sum256(rom))
	root := CorpusTrustRoot{Label: "independent-contract", ROMSHA256: sha, FixtureSHA256: sha, FixtureReceiptSHA256: sha, FixtureSummarySHA256: sha, DecompressedSHA: sha, CaptureSHA256: sha, CaptureReceiptSHA256: sha, CaptureSummarySHA256: sha, HistorySHA256: sha, HistorySummarySHA256: sha, EngineRevision: "test"}
	contract := RoutineContract{ID: "sub_018100", Corpora: []string{"contract"}, ROMSHA256: sha, Entry: 0x018100, Calls: []RoutineCall{{0x018000, 0x018003, 0x20}}, Returns: []RoutineReturn{{0x018101, 0x60}}, Ranges: []AddressRange{{0x018100, 0x018102}}}
	makeInsn := func(seq uint64, pc uint16, s uint16, cycle uint64, code []byte, next uint16, exitS uint16) captureCPUInsn {
		entry := cpuStateWithCycles{PB: 1, PC: pc, S: s, P: 0x30, Cycles: cycle}
		exit := entry
		exit.PC = next
		exit.S = exitS
		exit.Cycles += 10
		x := captureCPUInsn{Seq: seq, Entry: entry, Exit: exit, Length: len(code), Status: "retired", SuccessorPC: targetPC{1, next}}
		for i, b := range code {
			role := "operand"
			if i == 0 {
				role = "opcode"
			}
			x.Fetches = append(x.Fetches, captureFetch{Addr: 0x010000 | uint32(pc) + uint32(i), Value: b, Role: role, ROMOffset: uint32(pc)})
			x.Fetches[i].ROMOffset = 0x8000 + uint32(pc&0x7fff) + uint32(i)
		}
		return x
	}
	records := []captureCPUInsn{makeInsn(10, 0x8000, 0x1ff, 90, []byte{0x20, 0, 0x81}, 0x8100, 0x1fd), makeInsn(11, 0x8100, 0x1fd, 100, []byte{0xea}, 0x8101, 0x1fd), makeInsn(12, 0x8101, 0x1fd, 110, []byte{0x60}, 0x8003, 0x1ff), makeInsn(13, 0x8003, 0x1ff, 120, []byte{0xea}, 0x8004, 0x1ff)}
	b0, b1 := byte(0x80), byte(0x02)
	cd := &captureData{bus: []busEvent{{ID: 1, Cycle: 95, Space: "wram", Addr: 0x1ff, Op: "write", Value: &b0}, {ID: 2, Cycle: 100, Space: "wram", Addr: 0x1fe, Op: "write", Value: &b1}, {ID: 3, Cycle: 115, Space: "wram", Addr: 0x1fe, Op: "read", Value: &b1}, {ID: 4, Cycle: 120, Space: "wram", Addr: 0x1ff, Op: "read", Value: &b0}}}
	for i := range cd.bus {
		cd.bus[i].Actor = "cpu"
		cd.bus[i].CPUPC = 0x018000
		cd.bus[i].CPUOpcode = 0x20
		if i >= 2 {
			cd.bus[i].CPUPC = 0x018101
			cd.bus[i].CPUOpcode = 0x60
		}
	}
	return AdmissionPolicy{Corpora: map[string]CorpusTrustRoot{"contract": root}, Routines: []RoutineContract{contract}}, rom, contract, records, cd
}

func TestAdmissionPolicyOwnsInputs(t *testing.T) {
	p, rom, _, _, _ := policyControlFixture(t)
	v, err := NewEvidenceVerifierWithPolicy(t.TempDir(), p, rom)
	if err != nil {
		t.Fatal(err)
	}
	digest := v.PolicySHA256()
	p.Corpora["contract"] = CorpusTrustRoot{}
	p.Routines[0].Ranges[0].End = 0
	rom[0x8100] = 0
	if v.PolicySHA256() != digest || v.policy.rom[0x8100] != 0xea || v.policy.routines["sub_018100"].Ranges[0].End != 0x018102 {
		t.Fatal("caller mutated policy or ROM")
	}
	for _, mutate := range []func(*AdmissionPolicy){func(p *AdmissionPolicy) {
		r := p.Corpora["contract"]
		r.DecompressedSHA = ""
		p.Corpora["contract"] = r
	}, func(p *AdmissionPolicy) { p.Routines[0].Aliases = []string{p.Routines[0].ID} }, func(p *AdmissionPolicy) { p.Routines[0].Calls[0].Resume++ }, func(p *AdmissionPolicy) { p.Routines[0].Ranges = append(p.Routines[0].Ranges, p.Routines[0].Ranges[0]) }} {
		p, rom, _, _, _ := policyControlFixture(t)
		mutate(&p)
		if _, err := NewEvidenceVerifierWithPolicy("", p, rom); err == nil {
			t.Fatal("accepted invalid policy")
		}
	}
}

func TestAdmissionGenericControl(t *testing.T) {
	p, rom, c, records, cd := policyControlFixture(t)
	v, err := NewEvidenceVerifierWithPolicy("", p, rom)
	if err != nil {
		t.Fatal(err)
	}
	replay := &ReplayCase{CallSeq: 10, ReturnSeq: 13, CallPC: 0x018000, ReturnInsnPC: 0x018101}
	if _, err := v.verifyRoutineWindow(replay, c, records, cd); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		edit func([]captureCPUInsn, *captureData, *RoutineContract)
	}{
		{"bad fetched ROM", func(r []captureCPUInsn, _ *captureData, _ *RoutineContract) { r[1].Fetches[0].Value = 0x60 }},
		{"bad resume", func(r []captureCPUInsn, _ *captureData, _ *RoutineContract) { r[2].SuccessorPC.Addr++ }},
		{"bad stack", func(_ []captureCPUInsn, d *captureData, _ *RoutineContract) { x := byte(3); d.bus[2].Value = &x }},
		{"DMA actor", func(_ []captureCPUInsn, d *captureData, _ *RoutineContract) { d.bus[0].Actor = "dma" }},
		{"missing actor", func(_ []captureCPUInsn, d *captureData, _ *RoutineContract) { d.bus[0].Actor = "" }},
		{"missing stack read", func(_ []captureCPUInsn, d *captureData, _ *RoutineContract) { d.bus = d.bus[:3] }},
		{"wrong width", func(r []captureCPUInsn, _ *captureData, _ *RoutineContract) { r[1].Length = 2 }},
		{"emulation", func(r []captureCPUInsn, _ *captureData, _ *RoutineContract) { r[1].Entry.E = true }},
		{"closure escape", func(_ []captureCPUInsn, _ *captureData, c *RoutineContract) { c.Ranges[0].End = 0x018101 }},
		{"enclosing gap", func(_ []captureCPUInsn, d *captureData, _ *RoutineContract) { d.gaps = []captureGap{{1, 20}} }},
		{"transition", func(_ []captureCPUInsn, d *captureData, _ *RoutineContract) {
			d.transitions = []captureTransition{{Cycle: 95, Kind: "nmi"}}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, _, c, r, d := policyControlFixture(t)
			tt.edit(r, d, &c)
			if _, err := v.verifyRoutineWindow(replay, c, r, d); err == nil {
				t.Fatal("accepted malformed routine")
			}
		})
	}
}

func TestAdmissionCallPushBytes(t *testing.T) {
	for _, tt := range []struct {
		name         string
		bank         byte
		pc, next     uint16
		code, values []byte
		resume       uint32
	}{
		{"known JSR", 0, 0x805a, 0x85fc, []byte{0x20, 0xfc, 0x85}, []byte{0x80, 0x5c}, 0x00805d},
		{"JSL saved bank", 5, 0x9000, 0x8100, []byte{0x22, 0, 0x81, 1}, []byte{5, 0x90, 3}, 0x059004},
		{"JSR PC wrap", 1, 0xfffd, 0x8100, []byte{0x20, 0, 0x81}, []byte{0xff, 0xff}, 0x010000},
		{"JSL PC wrap", 5, 0xfffc, 0x8100, []byte{0x22, 0, 0x81, 1}, []byte{5, 0xff, 0xff}, 0x050000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			entry := cpuStateWithCycles{PB: tt.bank, PC: tt.pc, S: 0x1ff, Cycles: 10}
			exit := entry
			exit.PC = tt.next
			exit.S -= uint16(len(tt.values))
			exit.Cycles = 20
			if tt.code[0] == 0x22 {
				exit.PB = tt.code[3]
			}
			insn := captureCPUInsn{Entry: entry, Exit: exit, Fetches: []captureFetch{{Value: tt.code[0]}}, SuccessorPC: targetPC{exit.PB, exit.PC}}
			var events []busEvent
			for i, w := range tt.values {
				value := w
				events = append(events, busEvent{Actor: "cpu", CPUPC: physicalCPU(entry), CPUOpcode: tt.code[0], ID: uint64(i + 1), Cycle: 11 + uint64(i), Space: "wram", Addr: uint32(entry.S) - uint32(i), Op: "write", Value: &value})
			}
			call, err := verifyCallInstruction(insn, tt.code, events)
			if err != nil {
				t.Fatal(err)
			}
			if call.resume != tt.resume {
				t.Fatalf("resume %06X != %06X", call.resume, tt.resume)
			}
			*events[0].Value ^= 1
			if _, err := verifyCallInstruction(insn, tt.code, events); err == nil {
				t.Fatal("accepted wrong saved-return byte")
			}
		})
	}
}

func TestAdmissionWRAMIsNotROM(t *testing.T) {
	p, rom, _, records, _ := policyControlFixture(t)
	p.Routines[0].Ranges = []AddressRange{{0x7e8000, 0x7e8102}}
	p.Routines[0].Entry = 0x7e8100
	if _, err := NewEvidenceVerifierWithPolicy("", p, rom); err == nil {
		t.Fatal("accepted WRAM ROM closure")
	}
	p, rom, _, records, _ = policyControlFixture(t)
	v, err := NewEvidenceVerifierWithPolicy("", p, rom)
	if err != nil {
		t.Fatal(err)
	}
	records[1].Entry.PB = 0x7e
	if _, err := v.verifiedInstruction(records[1]); err == nil {
		t.Fatal("accepted WRAM instruction as ROM")
	}
	p.Routines[0].Calls[0] = RoutineCall{0x7e8000, 0x7e8003, 0x20}
	if _, err := NewEvidenceVerifierWithPolicy("", p, rom); err == nil {
		t.Fatal("accepted WRAM callsite as ROM")
	}
}

func syntheticAdmissionFixture(t *testing.T) (string, AdmissionPolicy, []byte, ReplayCase) {
	t.Helper()
	policy, rom, contract, records, cd := policyControlFixture(t)
	dir := t.TempDir()
	var stream bytes.Buffer
	enc := json.NewEncoder(&stream)
	emit := func(x any) {
		if err := enc.Encode(x); err != nil {
			t.Fatal(err)
		}
	}
	sha := policy.Corpora["contract"].ROMSHA256
	emit(map[string]any{"kind": "run", "run": captureRunHeader{ROMSHA256: sha, EngineRevision: "test", Start: "power_on"}})
	for _, e := range cd.bus {
		emit(map[string]any{"kind": "bus", "id": e.ID, "cycle": e.Cycle, "space": e.Space, "addr": e.Addr, "op": e.Op, "value": *e.Value, "cpu": map[string]any{"pbr": e.CPUPC >> 16, "pc": e.CPUPC & 0xffff, "opcode": e.CPUOpcode}})
	}
	for _, insn := range records {
		emit(map[string]any{"kind": "cpu_insn", "insn": insn})
	}
	write := func(name string, b []byte) *EvidenceFileRef {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		return &EvidenceFileRef{Path: path, SHA256: fmt.Sprintf("%x", sha256.Sum256(b))}
	}
	fixture := write("fixture.jsonl", stream.Bytes())
	fixture.DecompressedSHA256 = fixture.SHA256
	rb, _ := json.Marshal(streamReceipt{Outcome: "complete", StreamSHA256: fixture.SHA256})
	receipt := write("receipt.json", rb)
	sb, _ := json.Marshal(runSummary{ROMHash: sha, TraceHash: fixture.SHA256, Op: "write", AddressRanges: []addressRange{{Space: "wram", Start: 0x1fe, End: 0x1ff}}})
	summary := write("summary.json", sb)
	fixture.Receipt = receipt
	fixture.Summary = summary
	root := policy.Corpora["contract"]
	root.FixtureSHA256 = fixture.SHA256
	root.DecompressedSHA = fixture.SHA256
	root.FixtureReceiptSHA256 = receipt.SHA256
	root.FixtureSummarySHA256 = summary.SHA256
	root.CaptureSHA256 = fixture.SHA256
	root.CaptureReceiptSHA256 = receipt.SHA256
	root.CaptureSummarySHA256 = summary.SHA256
	root.HistorySHA256 = fixture.SHA256
	root.HistoryReceiptSHA256 = receipt.SHA256
	root.HistorySummarySHA256 = summary.SHA256
	policy.Corpora["contract"] = root
	state := func(x cpuStateWithCycles) CPUState {
		return CPUState{A: x.A, X: x.X, Y: x.Y, S: x.S, D: x.D, DB: x.DB, PB: x.PB, PC: x.PC, P: x.P, E: x.E, Cycles: x.Cycles}
	}
	c := ReplayCase{SchemaVersion: "snes-routine-case-v1", CaseID: "synthetic-new-entry", RoutineID: contract.ID, RunID: fixture.SHA256, StreamSHA256: fixture.SHA256, ROMSHA256: sha, EngineRevision: "test", CallPC: contract.Calls[0].Address, CallSeq: 10, EntrySeq: 11, ExitSeq: 12, ReturnSeq: 13, ReturnInsnPC: 0x018101, InstructionCount: 2, InitialState: state(records[1].Entry), ObservedExit: state(records[2].Exit), ObservedNextPC: 0x018003, StartCycle: 100, EndCycle: 120, InitialMemory: []MemoryCell{{Address: 0x7e01fe, Value: 2}, {Address: 0x7e01ff, Value: 0x80}}, Evidence: &CaseEvidence{Corpus: "contract", Label: root.Label, Fixture: fixture, Capture: fixture, History: fixture, InitialMemorySource: []InitialMemorySourceClaim{{Address: 0x7e01fe, Source: "confirmed_by_prior_write"}, {Address: 0x7e01ff, Source: "confirmed_by_prior_write"}}}}
	return dir, policy, rom, c
}

func TestAdmissionNewSyntheticRoutine(t *testing.T) {
	dir, policy, rom, c := syntheticAdmissionFixture(t)
	sha := c.ROMSHA256
	v, err := NewEvidenceVerifierWithPolicy(dir, policy, rom)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.PrefetchFixtures([]ReplayCase{c}); err != nil {
		t.Fatal(err)
	}
	rec, err := v.Admit(&c, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Admitted || !v.IsAdmitted(c.CaseHash, c.AdmissionDigest) {
		t.Fatal("synthetic routine was not admitted")
	}
	if defaultEvidenceVerifier.IsAdmitted(c.CaseHash, c.AdmissionDigest) {
		t.Fatal("explicit policy grant escaped into default verifier")
	}
	region, err := DecodeRegionWithConfig(DecodeRegionConfig{CodeBytes: rom[0x8100:0x8102], EntryAddr: 0x018100, EntryCtx: recovery.Context{E: "clear", M: "set", X: "set", C: "unknown"}, PinnedROM: rom, ROMBaseAddr: 0x018100, MaxSteps: 20})
	if err != nil {
		t.Fatal(err)
	}
	region.ROMBytes = nil
	code, err := GenerateRegionC(region)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "generated.c")
	if err := os.WriteFile(path, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	runner, err := NewCompiledRegionRunnerWithROM(context.Background(), path, "execute_"+region.Name, rom)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	if err := runner.BindRegion(region, "synthetic-policy-test"); err != nil {
		t.Fatal(err)
	}
	proof, err := v.ExecuteThreeWayRoutineReplay(context.Background(), runner, c)
	if err != nil {
		t.Fatal(err)
	}
	if !proof.CapturedProofEligible || !proof.Matched || !proof.CPUTransitionMatch || !proof.EffectsMatch {
		t.Fatalf("synthetic replay refused or mismatched: %+v", proof)
	}
	legacy, err := ExecuteThreeWayRoutineReplay(context.Background(), runner, c)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.CapturedProofEligible {
		t.Fatal("global replay borrowed explicit policy authority")
	}
	changed, err := NewEvidenceVerifierWithPolicy(dir, policy, rom)
	if err != nil {
		t.Fatal(err)
	}
	changed.ValidateRoutineReplayReceiptFreshness(&proof, &c, region, code, sha, "synthetic-policy-test", runner)
	if proof.CapturedProofEligible {
		t.Fatal("fresh verifier without admission reused grant")
	}
}
