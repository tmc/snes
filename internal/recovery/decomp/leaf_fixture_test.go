package decomp

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/snes/internal/bus"
	"github.com/tmc/snes/internal/cartridge"
	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/recovery"
)

// buildLeafROM constructs a 256-byte tiny ROM for testing leaf routine emission.
// Routine starts at $00:8000:
//
//	$8000: C2 20     REP #$20        (16-bit A)
//	$8002: AD 20 80  LDA $8020       (read 16-bit adjacent ROM data at $8020)
//	$8005: 18        CLC
//	$8006: 69 50 00  ADC #$0050      (add $0050 with carry)
//	$8009: 8D 00 02  STA $0200       (store 16-bit to Low RAM $7E0200)
//	$800C: E2 30     SEP #$30        (8-bit A, 8-bit X/Y; truncates X/Y)
//	$800E: A2 12     LDX #$12        (nonzero X)
//	$8010: A0 34     LDY #$34        (nonzero Y)
//	$8012: C9 70     CMP #$70        (compares low byte 0x70 with 0x70 -> Z=1)
//	$8014: F0 03     BEQ $8019       (branch to target $8019)
//	$8016: EA        NOP
//	$8017: EA        NOP
//	$8018: EA        NOP
//	$8019: 60        RTS
//	$8020: 20 01     ROM DATA (0x0120)
func buildLeafROM(romDataByte byte) []byte {
	rom := make([]byte, 256)
	code := []byte{
		0xC2, 0x20, // REP #$20
		0xAD, 0x20, 0x80, // LDA $8020
		0x18,             // CLC
		0x69, 0x50, 0x00, // ADC #$0050
		0x8D, 0x00, 0x02, // STA $0200
		0xE2, 0x30, // SEP #$30
		0xA2, 0x12, // LDX #$12
		0xA0, 0x34, // LDY #$34
		0xC9, 0x70, // CMP #$70
		0xF0, 0x03, // BEQ +3 ($8019)
		0xEA, // NOP
		0xEA, // NOP
		0xEA, // NOP
		0x60, // RTS
	}
	copy(rom[0:], code)
	// Adjacent ROM data at offset 0x20 (address $8020)
	rom[0x20] = romDataByte
	rom[0x21] = 0x01 // 0x0120 default
	return rom
}

// runGoCPULeaf executes the leaf routine using the reference Go 65816 CPU core.
func runGoCPULeaf(t *testing.T, rom []byte, initCPU CPUState, stackReturnPC uint16) (CPUState, []MemoryWrite) {
	t.Helper()
	b := bus.NewBus()

	// Map Low RAM $0000-$1FFF
	wram := bus.NewWRAMDevice()
	b.Map(0x7E0000, 0x7FFFFF, wram)
	for bank := uint32(0x00); bank <= 0x3F; bank++ {
		b.Map(bank<<16, (bank<<16)|0x1FFF, wram)
	}

	// Pad ROM to 32KB for cartridge mapping
	paddedROM := make([]byte, 32*1024)
	copy(paddedROM, rom)
	cart := cartridge.New(paddedROM)
	cart.MapToBus(b)

	// Set up stack with return address
	s1 := (uint32(initCPU.S) + 1) & 0xFFFF
	s2 := (uint32(initCPU.S) + 2) & 0xFFFF
	lo := uint8(stackReturnPC & 0xFF)
	hi := uint8((stackReturnPC >> 8) & 0xFF)
	b.Write(s1, lo)
	b.Write(s2, hi)

	c := cpu.NewCPU(b)
	c.A = initCPU.A
	c.X = initCPU.X
	c.Y = initCPU.Y
	c.S = initCPU.S
	c.PC = initCPU.PC
	c.P = initCPU.P
	c.D = initCPU.D
	c.DB = initCPU.DB
	c.PB = initCPU.PB
	c.E = initCPU.E

	var writes []MemoryWrite
	b.WriteHook = func(addr uint32, val uint8) {
		canAddr := BusCanonicalAddr(addr)
		// Only record writes made to Low RAM / WRAM (ignoring stack initialization)
		if canAddr >= 0x7E0000 && canAddr < 0x7E1F00 {
			writes = append(writes, MemoryWrite{Address: canAddr, Value: val})
		}
	}

	// Step CPU until RTS returns (PC leaves $8000..$801A)
	steps := 0
	for steps < 100 {
		c.Step()
		steps++
		if c.PC == (stackReturnPC+1)&0xFFFF && c.PB == initCPU.PB {
			break
		}
	}

	exitState := CPUState{
		A:  c.A,
		X:  c.X,
		Y:  c.Y,
		S:  c.S,
		PC: c.PC,
		D:  c.D,
		DB: c.DB,
		PB: c.PB,
		P:  c.P,
		E:  c.E,
	}
	return exitState, writes
}

func compileAndRunRegion(ctx context.Context, t *testing.T, cSource string, fnName string, cases []ReplayCase) ([]ExecResult, error) {
	return compileAndRunRegionWithROM(ctx, t, cSource, fnName, nil, cases)
}

func compileAndRunRegionWithROM(ctx context.Context, t *testing.T, cSource string, fnName string, rom []byte, cases []ReplayCase) ([]ExecResult, error) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	baseTmp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(baseTmp, 0755)
	tmpDir, err := os.MkdirTemp(baseTmp, "leaf-fixture-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cPath := filepath.Join(tmpDir, "leaf.c")
	if err := os.WriteFile(cPath, []byte(cSource), 0644); err != nil {
		t.Fatalf("write runner code: %v", err)
	}

	runner, err := NewCompiledRegionRunnerWithROM(ctx, cPath, fnName, rom)
	if err != nil {
		return nil, fmt.Errorf("compile runner: %w", err)
	}
	defer runner.Close()

	return runner.RunBatch(ctx, cases)
}

func TestLeafFixture_Deterministic(t *testing.T) {
	rom := buildLeafROM(0x20)
	ctx := recovery.Context{E: "clear", M: "set", X: "set", C: "clear"}

	region1, err := DecodeRegion(rom, 0x008000, 26, ctx)
	if err != nil {
		t.Fatalf("decode 1: %v", err)
	}
	c1, err := GenerateRegionC(region1)
	if err != nil {
		t.Fatalf("gen 1: %v", err)
	}

	region2, err := DecodeRegion(rom, 0x008000, 26, ctx)
	if err != nil {
		t.Fatalf("decode 2: %v", err)
	}
	c2, err := GenerateRegionC(region2)
	if err != nil {
		t.Fatalf("gen 2: %v", err)
	}

	if c1 != c2 {
		t.Fatalf("GenerateRegionC is non-deterministic: output differs between identical invocations")
	}
	h1 := sha256.Sum256([]byte(c1))
	h2 := sha256.Sum256([]byte(c2))
	if h1 != h2 {
		t.Fatalf("sha256 mismatch")
	}
}

func TestLeafFixture_MatchReference(t *testing.T) {
	rom := buildLeafROM(0x20)
	ctx := recovery.Context{E: "clear", M: "set", X: "set", C: "clear"}

	region, err := DecodeRegionFromBytes(rom[:26], 0x008000, ctx, rom[:64], 0x008000, 50000)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	cSource, err := GenerateRegionC(region)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	initCPU := CPUState{
		A:  0x0000,
		X:  0x0000,
		Y:  0x0000,
		S:  0x1FDD,
		PC: 0x8000,
		D:  0x0000,
		DB: 0x00,
		PB: 0x00,
		P:  0x34, // M=1, X=1, I=1
		E:  false,
	}

	// Reference Go CPU execution
	refExit, refWrites := runGoCPULeaf(t, rom, initCPU, 0x805C)

	// ReplayCase setup for Compiled C
	cCase := ReplayCase{
		CaseID:       "leaf-pos-1",
		InitialState: initCPU,
		InitialMemory: []MemoryCell{
			{Address: 0x7E1FDE, Value: 0x5C}, // stack return PC low
			{Address: 0x7E1FDF, Value: 0x80}, // stack return PC high
		},
	}

	res, err := compileAndRunRegion(context.Background(), t, cSource, "execute_sub_008000", []ReplayCase{cCase})
	if err != nil {
		t.Fatalf("run compiled region: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	cExit := res[0].State
	cWrites := res[0].Writes

	// Register compare
	matched, disc := CompareCPUStates(refExit, cExit)
	if !matched {
		t.Errorf("CPU state divergence vs reference: %s\nref: %+v\nc:   %+v", disc, refExit, cExit)
	}

	// Writes compare
	wMatched, wDisc := CompareWrites(refWrites, cWrites)
	if !wMatched {
		t.Errorf("writes divergence vs reference: %s\nref: %+v\nc:   %+v", wDisc, refWrites, cWrites)
	}

	// Verify specific contracts
	if cExit.X != 0x12 {
		t.Errorf("Exit X = $%04X, want $0012 (nonzero exit test)", cExit.X)
	}
	if cExit.Y != 0x34 {
		t.Errorf("Exit Y = $%04X, want $0034 (nonzero exit test)", cExit.Y)
	}
	if (cExit.P & 0x04) != (initCPU.P & 0x04) {
		t.Errorf("Flag I not preserved: entry=$%02X, exit=$%02X", initCPU.P, cExit.P)
	}
	if cExit.PC != 0x805D {
		t.Errorf("Exit PC = $%04X, want $805D", cExit.PC)
	}
	if cExit.S != initCPU.S+2 {
		t.Errorf("Exit S = $%04X, want $%04X", cExit.S, initCPU.S+2)
	}
}

func TestLeafFixture_ROMByteMutation(t *testing.T) {
	romA := buildLeafROM(0x20) // ROM data 0x0120
	romB := buildLeafROM(0x30) // ROM data 0x0130

	ctx := recovery.Context{E: "clear", M: "set", X: "set", C: "clear"}
	regA, _ := DecodeRegionFromBytes(romA[:26], 0x008000, ctx, romA[:64], 0x008000, 50000)
	regB, _ := DecodeRegionFromBytes(romB[:26], 0x008000, ctx, romB[:64], 0x008000, 50000)

	cA, _ := GenerateRegionC(regA)
	cB, _ := GenerateRegionC(regB)

	hA := sha256.Sum256([]byte(cA))
	hB := sha256.Sum256([]byte(cB))

	if hA == hB {
		t.Fatalf("ROM byte mutation did not change emitted C SHA256")
	}

	// Run both and confirm outputs differ
	initCPU := CPUState{S: 0x1FDD, PC: 0x8000, P: 0x30}
	cases := []ReplayCase{
		{
			CaseID:       "leaf-rom",
			InitialState: initCPU,
			InitialMemory: []MemoryCell{
				{Address: 0x7E1FDE, Value: 0x5C},
				{Address: 0x7E1FDF, Value: 0x80},
			},
		},
	}

	resA, errA := compileAndRunRegion(context.Background(), t, cA, "execute_sub_008000", cases)
	resB, errB := compileAndRunRegion(context.Background(), t, cB, "execute_sub_008000", cases)
	if errA != nil || errB != nil {
		t.Fatalf("run failed: %v / %v", errA, errB)
	}

	// romA writes 0x0120 + 0x0050 = 0x0170 (writes [0x70, 0x01])
	// romB writes 0x0130 + 0x0050 = 0x0180 (writes [0x80, 0x01])
	if resA[0].Writes[0].Value == resB[0].Writes[0].Value {
		t.Fatalf("ROM mutation did not change runtime memory write: A=%02X, B=%02X",
			resA[0].Writes[0].Value, resB[0].Writes[0].Value)
	}
}

func TestLeafFixture_CarryAndOverflowMutation(t *testing.T) {
	rom := buildLeafROM(0x20)
	ctx := recovery.Context{E: "clear", M: "set", X: "set", C: "clear"}
	reg, _ := DecodeRegionFromBytes(rom[:26], 0x008000, ctx, rom[:64], 0x008000, 50000)
	cSource, _ := GenerateRegionC(reg)

	initCPU := CPUState{S: 0x1FDD, PC: 0x8000, P: 0x30}
	cCase := ReplayCase{
		CaseID:       "leaf-carry",
		InitialState: initCPU,
		InitialMemory: []MemoryCell{
			{Address: 0x7E1FDE, Value: 0x5C},
			{Address: 0x7E1FDF, Value: 0x80},
		},
	}

	res, err := compileAndRunRegion(context.Background(), t, cSource, "execute_sub_008000", []ReplayCase{cCase})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// Mutated carry control: toggle carry in expected state
	mutatedExit := res[0].State
	mutatedExit.P ^= 0x01
	if matched, _ := CompareCPUStates(mutatedExit, res[0].State); matched {
		t.Errorf("mutated carry was not detected by comparator")
	}

	// Mutated overflow control: toggle overflow in expected state
	mutatedExitV := res[0].State
	mutatedExitV.P ^= 0x40
	if matched, _ := CompareCPUStates(mutatedExitV, res[0].State); matched {
		t.Errorf("mutated overflow was not detected by comparator")
	}
}

func TestLeafFixture_SEPTruncationMutation(t *testing.T) {
	rom := buildLeafROM(0x20)
	ctx := recovery.Context{E: "clear", M: "set", X: "clear", C: "clear"}
	reg, _ := DecodeRegionFromBytes(rom[:26], 0x008000, ctx, rom[:64], 0x008000, 50000)
	cSource, _ := GenerateRegionC(reg)

	// Entry with X having upper byte set: $1234
	initCPU := CPUState{X: 0x1234, Y: 0x5678, S: 0x1FDD, PC: 0x8000, P: 0x20} // X=0 (16-bit)
	cCase := ReplayCase{
		CaseID:       "leaf-sep-trunc",
		InitialState: initCPU,
		InitialMemory: []MemoryCell{
			{Address: 0x7E1FDE, Value: 0x5C},
			{Address: 0x7E1FDF, Value: 0x80},
		},
	}

	res, err := compileAndRunRegion(context.Background(), t, cSource, "execute_sub_008000", []ReplayCase{cCase})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// SEP #$30 truncates X to 8-bit, then LDX #$12 loads 0x12
	if res[0].State.X != 0x0012 {
		t.Errorf("Exit X = $%04X, want $0012", res[0].State.X)
	}
	// If a mutant kept high byte: $1212, comparator must reject
	mutantX := res[0].State
	mutantX.X = 0x1212
	if matched, _ := CompareCPUStates(mutantX, res[0].State); matched {
		t.Errorf("mutated index truncation was not detected by comparator")
	}
}

func TestLeafFixture_RTSOrderAndStoreWidthMutation(t *testing.T) {
	rom := buildLeafROM(0x20)
	ctx := recovery.Context{E: "clear", M: "set", X: "set", C: "clear"}
	reg, _ := DecodeRegionFromBytes(rom[:26], 0x008000, ctx, rom[:64], 0x008000, 50000)
	cSource, _ := GenerateRegionC(reg)

	initCPU := CPUState{S: 0x1FDD, PC: 0x8000, P: 0x30}
	cCase := ReplayCase{
		CaseID:       "leaf-rts",
		InitialState: initCPU,
		InitialMemory: []MemoryCell{
			{Address: 0x7E1FDE, Value: 0x5C}, // lo
			{Address: 0x7E1FDF, Value: 0x80}, // hi
		},
	}

	res, err := compileAndRunRegion(context.Background(), t, cSource, "execute_sub_008000", []ReplayCase{cCase})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// Expected exit PC is $805D
	if res[0].State.PC != 0x805D {
		t.Errorf("Exit PC = $%04X, want $805D", res[0].State.PC)
	}

	// Mutated RTS order: return PC becomes $5C81 instead of $805D
	mutantPC := res[0].State
	mutantPC.PC = 0x5C81
	if matched, _ := CompareCPUStates(mutantPC, res[0].State); matched {
		t.Errorf("mutated RTS order was not detected by comparator")
	}

	// Mutated store width: only 1 write instead of 2
	mutantWrites := []MemoryWrite{{Address: 0x7E0200, Value: 0x70}}
	if matched, _ := CompareWrites(mutantWrites, res[0].Writes); matched {
		t.Errorf("mutated store width was not detected by write comparator")
	}
}

func TestLeafFixture_Rejections(t *testing.T) {
	rom := buildLeafROM(0x20)
	ctx := recovery.Context{E: "clear", M: "set", X: "set", C: "clear"}
	reg, _ := DecodeRegionFromBytes(rom[:26], 0x008000, ctx, rom[:64], 0x008000, 50000)
	cSource, _ := GenerateRegionC(reg)

	// 1. Missing RAM rejection (no stack return word provided)
	t.Run("MissingRAM", func(t *testing.T) {
		initCPU := CPUState{S: 0x1FDD, PC: 0x8000, P: 0x30}
		cCase := ReplayCase{
			CaseID:        "leaf-missing-ram",
			InitialState:  initCPU,
			InitialMemory: nil, // stack uninitialized
		}
		res, err := compileAndRunRegion(context.Background(), t, cSource, "execute_sub_008000", []ReplayCase{cCase})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if !res[0].MissingRead {
			t.Errorf("MissingRead = false, want true for uninitialized stack read")
		}
		if res[0].MissingAddr != 0x7E1FDE {
			t.Errorf("MissingAddr = $%06X, want $7E1FDE", res[0].MissingAddr)
		}
	})

	// 2. Decimal mode rejection
	t.Run("DecimalModeReject", func(t *testing.T) {
		initCPU := CPUState{S: 0x1FDD, PC: 0x8000, P: 0x38} // P.D = 1
		cCase := ReplayCase{
			CaseID:       "leaf-dec",
			InitialState: initCPU,
			InitialMemory: []MemoryCell{
				{Address: 0x7E1FDE, Value: 0x5C},
				{Address: 0x7E1FDF, Value: 0x80},
			},
		}
		res, err := compileAndRunRegion(context.Background(), t, cSource, "execute_sub_008000", []ReplayCase{cCase})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if !res[0].MissingRead {
			t.Errorf("Decimal mode was not rejected on entry")
		}
	})

	// 3. Ambiguous width rejection
	t.Run("AmbiguousWidthReject", func(t *testing.T) {
		badCtx := recovery.Context{E: "clear", M: "unknown", X: "set"}
		_, err := DecodeRegion(rom, 0x008000, 26, badCtx)
		if err == nil {
			t.Errorf("expected error decoding region with unknown M, got nil")
		}
	})

	// 4. Exhausted fuel rejection
	t.Run("ExhaustedFuelReject", func(t *testing.T) {
		fuelReg, _ := DecodeRegionFromBytes(rom[:26], 0x008000, ctx, rom[:64], 0x008000, 50000)
		fuelReg.MaxSteps = 1 // routine visits 2 blocks; MaxSteps=1 triggers fuel exhaustion
		fuelC, _ := GenerateRegionC(fuelReg)
		initCPU := CPUState{S: 0x1FDD, PC: 0x8000, P: 0x30}
		cCase := ReplayCase{
			CaseID:       "leaf-fuel",
			InitialState: initCPU,
			InitialMemory: []MemoryCell{
				{Address: 0x7E1FDE, Value: 0x5C},
				{Address: 0x7E1FDF, Value: 0x80},
			},
		}
		res, err := compileAndRunRegion(context.Background(), t, fuelC, "execute_sub_008000", []ReplayCase{cCase})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if !res[0].MissingRead {
			t.Errorf("fuel exhaustion did not trigger rejection")
		}
	})
}
