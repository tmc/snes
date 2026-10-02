package decomp

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/structure"
)

func TestDefaultVerifierHasNoAuthority(t *testing.T) {
	v := NewEvidenceVerifier(t.TempDir())
	if len(v.policy.roots) != 0 || len(v.policy.routines) != 0 || len(v.policy.blocks) != 0 {
		t.Fatal("default verifier contains implicit authority")
	}
	c := ReplayCase{SchemaVersion: "snes-routine-case-v1", CaseID: "self-asserted", Evidence: &CaseEvidence{Corpus: "operator-not-supplied"}}
	PopulateHashes(&c)
	rec, err := v.Admit(&c, nil, nil)
	if err == nil || rec.Admitted || rec.Reason != "unknown corpus" {
		t.Fatalf("default admission: %+v %v", rec, err)
	}
	digest := ComputeAdmissionDigest(c.CaseID, c.RoutineID, c.CaseHash, "a", "b", "c")
	if v.IsAdmitted(c.CaseHash, digest) {
		t.Fatal("public digest granted admission")
	}
}

func TestPolicyOwnsBlockInstructions(t *testing.T) {
	policy, rom, _, _, _ := policyControlFixture(t)
	policy.Blocks = map[string][]uint32{"author-probe": {0x018100, 0x018101}}
	v, err := NewEvidenceVerifierWithPolicy("", policy, rom)
	if err != nil {
		t.Fatal(err)
	}
	pin := v.PolicySHA256()
	policy.Blocks["author-probe"][0] = 0
	delete(policy.Blocks, "author-probe")
	if v.policy.blocks["author-probe"][0] != 0x018100 || v.PolicySHA256() != pin {
		t.Fatal("caller mutated reviewed block policy")
	}
	for _, pcs := range [][]uint32{nil, {0x7e0000}, {0x018100, 0x018100}} {
		policy.Blocks = map[string][]uint32{"invalid": pcs}
		if _, err := NewEvidenceVerifierWithPolicy("", policy, rom); err == nil {
			t.Fatal("invalid block policy admitted")
		}
	}
}

func TestExplicitRunnerROMBinding(t *testing.T) {
	// Authored probe: read ROM datum, write one RAM cell, and return.
	rom := make([]byte, 32768)
	code := []byte{0xad, 0x10, 0x80, 0x8d, 0x00, 0x02, 0x60}
	copy(rom, code)
	rom[0x10] = 0x73
	reg, err := DecodeRegionFromBytes(code, 0x8000, recovery.Context{E: "clear", M: "set", X: "set", C: "unknown"}, rom, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	reg.Name = "author_rom_probe"
	reg.ROMBytes = nil
	source, err := GenerateRegionC(reg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "probe.c")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	runner, err := NewCompiledRegionRunnerWithROM(context.Background(), path, "execute_"+reg.Name, rom)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	pin := fmt.Sprintf("%x", sha256.Sum256(rom))
	rom[0x10] ^= 0xff
	if runner.ROMSHA256() != pin || runner.ROMBytes()[0x10] != 0x73 {
		t.Fatal("runner retained caller's ROM slice")
	}
	c := ReplayCase{CaseID: "author-probe", InitialState: CPUState{S: 0x1fd, PC: 0x8000, P: 0x30}, InitialMemory: []MemoryCell{{Address: 0x7e01fe, Value: 0xff}, {Address: 0x7e01ff, Value: 0x8f}}}
	out, err := runner.RunBatch(context.Background(), []ReplayCase{c})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].MissingRead || len(out[0].Writes) != 1 || out[0].Writes[0].Address != 0x7e0200 || out[0].Writes[0].Value != 0x73 {
		t.Fatalf("bound ROM probe: %+v", out)
	}
	unbound, err := NewCompiledRegionRunner(context.Background(), path, "execute_"+reg.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer unbound.Close()
	out, err = unbound.RunBatch(context.Background(), []ReplayCase{c})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || !out[0].MissingRead {
		t.Fatalf("missing ROM probe: %+v", out)
	}
	c.ROMSHA256 = strings.Repeat("0", 64)
	if _, err := RunEmulatorRoutineWithROM(context.Background(), c, runner.ROMBytes()); err == nil {
		t.Fatal("reference leg accepted substituted ROM identity")
	}
}

func TestBlockReplayUsesExplicitAuthority(t *testing.T) {
	dir, policy, rom, c := syntheticAdmissionFixture(t)
	c.SchemaVersion = "snes-replay-case-v2"
	c.RoutineID = ""
	c.BlockID = "author-nop"
	c.CaseID = "author-single-step"
	c.ExitSeq = c.EntrySeq
	c.InstructionCount = 1
	c.CallSeq, c.ReturnSeq = 0, 0
	c.CallPC, c.ReturnInsnPC = 0, 0
	c.EndCycle = 110
	c.ObservedExit = c.InitialState
	c.ObservedExit.PC++
	c.ObservedExit.Cycles = 110
	c.ObservedNextPC = 0x018101
	c.InitialMemory = nil
	c.Evidence.InitialMemorySource = nil
	policy.Blocks = map[string][]uint32{c.BlockID: {0x018100}}
	v, err := NewEvidenceVerifierWithPolicy(dir, policy, rom)
	if err != nil {
		t.Fatal(err)
	}
	entry := recovery.Context{E: "clear", M: "set", X: "set", C: "unknown"}
	block := &structure.BasicBlock{ID: c.BlockID, StartAddress: 0x018100, EndAddress: 0x018101, Successors: []uint32{0x018101}, Instructions: []recovery.Instruction{{ID: "author-nop", Address: 0x018100, Bytes: "ea", Opcode: 0xea, Mnemonic: "nop", Context: entry}}}
	ir, err := LiftBlock(block, entry)
	if err != nil {
		t.Fatal(err)
	}
	if rec, err := v.Admit(&c, block, ir); err != nil || !rec.Admitted {
		t.Fatalf("admit=%+v %v", rec, err)
	}
	runner, err := NewCompiledRunner(context.Background(), ir)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	cfg := DefaultVerifyConfig()
	cfg.ROMSHA256 = c.ROMSHA256
	cfg.ProjectRevision = "author-block"
	proof := v.ExecuteThreeWayReplay(context.Background(), ir, c, runner, cfg)
	if !proof.CapturedProofEligible || !proof.Matched {
		t.Fatalf("explicit block replay=%+v", proof)
	}
	ordinary := ExecuteThreeWayReplay(context.Background(), ir, c, runner, cfg)
	if ordinary.CapturedProofEligible {
		t.Fatal("default replay borrowed explicit authority")
	}
	source, err := GenerateCompilableC(ir)
	if err != nil {
		t.Fatal(err)
	}
	proof.AdmissionDigest = "different-policy"
	v.ValidateReplayReceiptFreshness(&proof, &c, ir, source, c.ROMSHA256, cfg.ProjectRevision)
	if proof.CapturedProofEligible || !proof.Metadata.IsStale {
		t.Fatal("different policy receipt retained eligibility")
	}
}
