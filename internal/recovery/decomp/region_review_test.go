package decomp

import (
	"context"
	"fmt"
	"testing"

	"github.com/tmc/snes/internal/recovery"
)

// Independent review regression tests for region.go (review 6B96B786 / coordinator prompt 11).
// Each case decodes a region with DecodeRegionFromBytes, generates C with GenerateRegionC,
// runs it, and compares against the reference Go CPU on every path.
// A case passes if the pipeline refuses (decode/generate error, or runtime refusal)
// or if the C matches the reference exactly. A supported execution that disagrees
// with the reference is a wrong answer.

type zzRegionCase struct {
	name string
	code []byte
	base uint32 // entry address; ROM image starts at $8000
	p    uint8  // entry P
	x    uint16
}

func zzRegionRun(t *testing.T, rc zzRegionCase) (outcome string, detail string) {
	t.Helper()
	rom := make([]byte, 256)
	copy(rom[rc.base-0x8000:], rc.code)
	m, x := "clear", "clear"
	if rc.p&0x20 != 0 {
		m = "set"
	}
	if rc.p&0x10 != 0 {
		x = "set"
	}
	ctx := recovery.Context{E: "clear", M: m, X: x, C: "clear"}
	off := rc.base - 0x8000
	region, err := DecodeRegionFromBytes(rom[off:off+uint32(len(rc.code))], rc.base, ctx, rom[:64], 0x008000, 1000)
	if err != nil {
		return "refused-decode", err.Error()
	}
	src, err := GenerateRegionC(region)
	if err != nil {
		return "refused-generate", err.Error()
	}
	init := CPUState{S: 0x1FDD, PC: uint16(rc.base), P: rc.p, X: rc.x}
	ref, refW := runGoCPULeaf(t, rom, init, 0x805C)
	c := ReplayCase{CaseID: rc.name, InitialState: init, InitialMemory: []MemoryCell{
		{Address: 0x7E1FDE, Value: 0x5C}, {Address: 0x7E1FDF, Value: 0x80}}}
	res, err := compileAndRunRegion(context.Background(), t, src, fmt.Sprintf("execute_sub_%06x", rc.base), []ReplayCase{c})
	if err != nil {
		return "refused-run", err.Error()
	}
	r := res[0]
	if r.MissingRead || r.MMIOAccess || r.WriteOverflow {
		return "refused-runtime", fmt.Sprintf("missing=%v@%06X", r.MissingRead, r.MissingAddr)
	}
	ok, disc := CompareCPUStates(ref, r.State)
	wok, wdisc := CompareWrites(refW, r.Writes)
	if ok && wok {
		return "match", ""
	}
	return "WRONG", fmt.Sprintf("ref A=%04X X=%04X PC=%04X; C A=%04X X=%04X PC=%04X; %s %s",
		ref.A, ref.X, ref.PC, r.State.A, r.State.X, r.State.PC, disc, wdisc)
}

func TestZZRegionReview(t *testing.T) {
	cases := []zzRegionCase{
		// Item 1: BEQ skips REP #$20, so $8004 is reached with M=1 on the
		// taken path and M=0 on the fallthrough path. A9 EA EA decodes as
		// LDA #$EA; NOP under M=1 and as LDA #$EAEA under M=0.
		{name: "I1-width-conflict-taken(Z=1)", code: []byte{0xF0, 0x02, 0xC2, 0x20, 0xA9, 0xEA, 0xEA, 0xE2, 0x20, 0x60}, base: 0x8000, p: 0x32},
		{name: "I1-width-conflict-fallthrough(Z=0)", code: []byte{0xF0, 0x02, 0xC2, 0x20, 0xA9, 0xEA, 0xEA, 0xE2, 0x20, 0x60}, base: 0x8000, p: 0x30},
		// Item 1: branch target inside an operand ($8003 is the operand of
		// LDA #$E8 at $8002). Real CPU on the taken path executes INX at $8003.
		{name: "I1-target-in-operand(Z=1)", code: []byte{0xF0, 0x01, 0xA9, 0xE8, 0x60}, base: 0x8000, p: 0x32},
		// Item 2: BVC/BVS followed by ordinary instructions.
		{name: "I2-BVC-taken(V=0)", code: []byte{0x50, 0x01, 0xE8, 0xE8, 0x60}, base: 0x8000, p: 0x30},
		{name: "I2-BVC-not-taken(V=1)", code: []byte{0x50, 0x01, 0xE8, 0xE8, 0x60}, base: 0x8000, p: 0x70},
		{name: "I2-BVS-taken(V=1)", code: []byte{0x70, 0x01, 0xE8, 0xE8, 0x60}, base: 0x8000, p: 0x70},
		{name: "I2-BVS-not-taken(V=0)", code: []byte{0x70, 0x01, 0xE8, 0xE8, 0x60}, base: 0x8000, p: 0x30},
		// Item 3: BRL to an in-region address that is not otherwise a leader.
		{name: "I3-BRL-internal-target", code: []byte{0x82, 0x01, 0x00, 0xE8, 0xE8, 0x60}, base: 0x8000, p: 0x30},
		// Item 3: unsupported/unsized modes must be refused, not partitioned.
		{name: "I3-MVN-3byte-sized-as-1", code: []byte{0x54, 0x7E, 0x7E, 0x60}, base: 0x8000, p: 0x30},
		{name: "I3-JMP-abs", code: []byte{0x4C, 0x04, 0x80, 0xE8, 0x60}, base: 0x8000, p: 0x30},
		{name: "I3-BRK", code: []byte{0x00, 0x00, 0x60}, base: 0x8000, p: 0x30},
		// Item 3: region that falls off its end without a terminator.
		{name: "I3-no-terminator", code: []byte{0xE8, 0xE8}, base: 0x8000, p: 0x30},
	}
	for _, rc := range cases {
		out, d := zzRegionRun(t, rc)
		t.Logf("%-36s %-16s %s", rc.name, out, d)
		if out == "WRONG" {
			t.Errorf("case %s returned WRONG: %s", rc.name, d)
		}
	}
}

// Item 3: a region crossing $xx:FFFF must not continue into the next bank.
func TestZZRegionBankBoundary(t *testing.T) {
	ctx := recovery.Context{E: "clear", M: "set", X: "set", C: "clear"}
	code := []byte{0xEA, 0xEA, 0xEA, 0x60}
	region, err := DecodeRegionFromBytes(code, 0x00FFFD, ctx, nil, 0, 1000)
	if err != nil {
		t.Logf("I3-bank-boundary refused-decode %v [OK]", err)
		return
	}
	var addrs []string
	for _, b := range region.Blocks {
		for _, in := range b.Instructions {
			addrs = append(addrs, fmt.Sprintf("%06X", in.Address))
		}
	}
	t.Errorf("I3-bank-boundary ACCEPTED instruction addresses %v; expected bank crossing refusal", addrs)
}
