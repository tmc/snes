package candidates

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/coverage"
)

func docFixture() *recovery.Document {
	d := recovery.NewDocument(recovery.ROMIdentity{NormalizedSHA256: "rom"})
	d.Evidence = []recovery.Evidence{{ID: "obs", Kind: "observed", Details: "run:source-stream"}}
	add := func(a uint32, b string, op byte) {
		d.Instructions = append(d.Instructions, recovery.Instruction{ID: fmt.Sprintf("i-%x", a), Address: a, Bytes: b, Opcode: op, Context: recovery.Context{E: "clear", M: "set", X: "set", C: "clear"}, Evidence: []string{"obs"}})
	}
	add(0x8000, "200880", 0x20)
	add(0x8003, "6b", 0x6b)
	add(0x8008, "d003", 0xd0)
	add(0x800a, "a901", 0xa9)
	add(0x800c, "60", 0x60)
	d.Edges = []recovery.Edge{{ID: "call", Kind: "call", Source: "i-8000", Destination: 0x8008, Evidence: []string{"obs"}}}
	return d
}

func TestMineDeterministicFrontiers(t *testing.T) {
	d := docFixture()
	idx := &coverage.Index{ROMHash: "rom", Sites: []coverage.Site{{Address: 0x8000, Hits: 1 << 63}, {Address: 0x8000, Hits: 1 << 63}}}
	inventory := []Occurrence{{CaseID: "nested-not-outer", SourceID: "cases", ROMSHA256: "rom", EntryPC: 0x8000, ReturnInsnPC: 0x800c, Complete: true}, {CaseID: "real", SourceID: "cases", ROMSHA256: "rom", EntryPC: 0x8000, ReturnInsnPC: 0x8003, Complete: true}, {CaseID: "interrupted", SourceID: "cases", ROMSHA256: "rom", EntryPC: 0x8000, Interrupted: true}, {CaseID: "wrong-return", ROMSHA256: "rom", EntryPC: 0x8008, ReturnInsnPC: 0x8003, Complete: true}}
	a, err := Mine(d, idx, inventory, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for i, j := 0, len(d.Instructions)-1; i < j; i, j = i+1, j-1 {
		d.Instructions[i], d.Instructions[j] = d.Instructions[j], d.Instructions[i]
	}
	b, err := Mine(d, idx, inventory, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	if string(aj) != string(bj) {
		t.Fatal("nondeterministic output")
	}
	found := false
	for _, c := range a.Candidates {
		if c.Status != "proposed_unqualified" {
			t.Fatal("candidate promoted")
		}
		if c.Entry == 0x8000 && c.Kind == "caller_callee" {
			found = true
			if c.ReportedCompleteExecutions != 1 || c.ReportedInterruptedExecutions != 1 || c.ObservedEntryHits != "18446744073709551616" {
				t.Fatalf("wrong counts: %+v", c)
			}
			if len(c.Proposal.RefusalFrontiers) != 1 || c.Proposal.RefusalFrontiers[0].Target != 0x800d {
				t.Fatalf("missing cold frontier: %+v", c.Proposal)
			}
			if len(c.EvidenceIDs) != 1 {
				t.Fatal("evidence lost")
			}
		}
		if c.Entry == 0x8008 && c.ReportedCompleteExecutions != 0 {
			t.Fatal("foreign return counted")
		}
	}
	if !found {
		t.Fatal("composed candidate missing")
	}
}

func TestMineAmbiguityBoundsAndInterrupt(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*recovery.Document)
		opts   Options
		flag   string
	}{
		{"ambiguous", func(d *recovery.Document) {
			v := d.Instructions[0]
			v.ID = "variant"
			v.Context.M = "clear"
			d.Instructions = append(d.Instructions, v)
		}, Options{}, "ambiguous_instruction_context"},
		{"interrupt", func(d *recovery.Document) {
			d.Edges = append(d.Edges, recovery.Edge{Source: "i-8000", Kind: "interrupt", Destination: 0x9000})
		}, Options{}, "interrupt_edge"},
		{"bounded", func(*recovery.Document) {}, Options{MaxInstructions: 1}, "instruction_bound"},
		{"decimal", func(d *recovery.Document) { d.Instructions[0].Opcode = 0xf8; d.Instructions[0].Bytes = "f8" }, Options{}, "unsupported_decimal_enable"},
		{"mmio", func(d *recovery.Document) {
			d.Instructions[0].Opcode = 0x8d
			d.Instructions[0].Bytes = "8d0021"
			d.Instructions[0].Mode = "absolute"
		}, Options{}, "potential_mmio"},
		{"cycle", func(d *recovery.Document) { d.Instructions[0].Opcode = 0x80; d.Instructions[0].Bytes = "80fe" }, Options{}, "control_cycle_or_recursion"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := docFixture()
			tt.modify(d)
			r, err := Mine(d, nil, nil, nil, tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, c := range r.Candidates {
				if c.Entry == 0x8000 {
					for _, f := range c.Flags {
						if f == tt.flag {
							found = true
						}
					}
				}
			}
			if !found {
				t.Fatalf("missing flag %s: %+v", tt.flag, r.Candidates)
			}
		})
	}
}

func TestReadInventory(t *testing.T) {
	valid := `{"schema_version":"snes-routine-case-v1","case_id":"case","run_id":"run","stream_sha256":"stream","rom_sha256":"rom","entry_pc":32768,"return_insn_pc":32780,"entry_seq":0,"exit_seq":2,"return_seq":3,"instruction_count":3,"captured_proof_eligible":true}` + "\n"
	o, src, err := ReadInventory(strings.NewReader(valid), "input")
	if err != nil {
		t.Fatal(err)
	}
	if len(o) != 1 || !o[0].Complete || src.SHA256 == "" || o[0].SourceID != "input" {
		t.Fatalf("bad inventory %+v", o)
	}
	for _, s := range []string{valid + valid, `{`, strings.Replace(valid, "snes-routine-case-v1", "other", 1)} {
		if _, _, err := ReadInventory(strings.NewReader(s), "input"); err == nil {
			t.Fatal("invalid inventory accepted")
		}
	}
	incomplete := strings.Replace(valid, `"return_seq":3`, `"return_seq":4`, 1)
	o, _, err = ReadInventory(strings.NewReader(incomplete), "input")
	if err != nil || o[0].Complete {
		t.Fatal("incomplete interval counted")
	}
}

func ExampleMine() {
	doc := recovery.NewDocument(recovery.ROMIdentity{})
	report, err := Mine(doc, nil, nil, nil, Options{})
	fmt.Println(err, len(report.Candidates))
	// Output: <nil> 0
}

func ExampleReadInventory() {
	occurrences, source, err := ReadInventory(strings.NewReader(""), "empty.jsonl")
	fmt.Println(err, len(occurrences), source.Kind)
	// Output: <nil> 0 reported_routine_case_inventory
}

func TestMineEntryCarryVariants(t *testing.T) {
	d := docFixture()
	v := d.Instructions[0]
	v.ID = "carry-variant"
	v.Context.C = "set"
	d.Instructions = append(d.Instructions, v)
	r, err := Mine(d, nil, nil, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range r.Candidates {
		if c.Entry == 0x8000 {
			for _, f := range c.Flags {
				if f == "ambiguous_instruction_context" {
					t.Fatal("varying carry mistaken for decoding ambiguity")
				}
			}
			if len(c.Proposal.Contexts) != 2 {
				t.Fatal("entry carry evidence lost")
			}
		}
	}
}

func TestMineIdentityAndByteBounds(t *testing.T) {
	d := docFixture()
	if _, err := Mine(d, &coverage.Index{ROMHash: "other"}, nil, nil, Options{}); err == nil {
		t.Fatal("foreign coverage accepted")
	}
	r, err := Mine(d, nil, nil, nil, Options{MaxBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range r.Candidates {
		if c.ByteSpan > 4 {
			t.Fatalf("byte bound exceeded: %+v", c)
		}
	}
}
