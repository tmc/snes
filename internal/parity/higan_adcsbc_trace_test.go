package parity

import (
	"os"
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/disasm"
	"github.com/tmc/snes/internal/parity/libretro"
	"github.com/tmc/snes/internal/parity/libretro/bsnes"
	"github.com/tmc/snes/internal/parity/libretro/snes9x"
)

type higanCPUWriteRec struct {
	frame  int
	cycles uint64
	pb     uint8
	pc     uint16
	addr   uint32
	value  uint8
	a      uint16
	x      uint16
	y      uint16
	p      uint8
	db     uint8
	d      uint16
	s      uint16
	disasm string
}

type higanCPUFirstDivergence struct {
	frame     int
	addr      uint32
	goValue   uint8
	refValue  uint8
	bsnes     uint8
	snes9x    uint8
	lastWrite higanCPUWriteRec
	haveWrite bool
}

func TestHiganADCSBCFirstDivergence(t *testing.T) {
	cases := readHiganTestROMManifest(t)
	want := map[string]bool{
		"ADC8":  true,
		"ADC16": true,
		"SBC8":  true,
		"SBC16": true,
	}
	for _, tc := range cases {
		if !want[tc.Name] {
			continue
		}
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			traceHiganADCSBCFirstDivergence(t, tc)
		})
	}
}

func TestHiganADCSBCReferenceInstructionTrace(t *testing.T) {
	path := os.Getenv("HIGAN_ADCSBC_REF_TRACE")
	if path == "" {
		path = os.Getenv("ADCSBC_REF_TRACE")
	}
	if path == "" {
		t.Skip("set HIGAN_ADCSBC_REF_TRACE or ADCSBC_REF_TRACE to a bsnes/ares JSONL instruction trace artifact")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	refInstructions, _, _, _, _, summary := readReferenceCPUMulJSONL(t, raw)
	if len(refInstructions) == 0 {
		t.Fatalf("ADC/SBC reference trace %s has no instruction rows", path)
	}
	goInstructions, write := runHiganADCSBCGoInstructionTraceToFirstCRCWrite(t, "ADC8")
	t.Logf("ADC/SBC reference trace %s rows=%d sha256=%s instruction_rows=%d",
		path, summary["rows"], hashBytes(raw), len(refInstructions))
	t.Logf("ADC8 first watched Go CRC write: frame=%d cycle=%d PB:PC=%02X:%04X addr=%06X value=%02X A/X/Y/P=%04X/%04X/%04X/%02X ; %s",
		write.frame, write.cycles, write.pb, write.pc, write.addr, write.value,
		write.a, write.x, write.y, write.p, write.disasm)
	compareHiganADCSBCInstructionTrace(t, goInstructions, refInstructions, write.cycles)
}

func traceHiganADCSBCFirstDivergence(t *testing.T, tc higanTestROMCase) {
	t.Helper()
	targets := higanWRAMKnownDivergences(tc)
	if len(targets) == 0 {
		t.Fatalf("%s has no WRAM known divergences to trace", tc.Name)
	}
	rom := readHiganROM(t, tc)
	goSys := newHiganGoSystem(t, rom)
	bsn := newHiganReference(t, bsnes.DefaultPath(), tc.Path)
	s9x := newHiganReference(t, snes9x.DefaultPath(), tc.Path)
	regions := higanComparableRegions(bsn, s9x)
	wram, ok := regions["WRAM"]
	if !ok {
		t.Fatalf("%s: WRAM is not exposed by both references", tc.Name)
	}

	watch := make(map[uint32]bool)
	last := make(map[uint32]higanCPUWriteRec)
	history := make(map[uint32][]higanCPUWriteRec)
	for _, target := range targets {
		watch[target.Addr] = true
	}
	curFrame := 0
	goSys.Bus.WriteHook = func(addr uint32, value uint8) {
		if !isWRAM(addr) {
			return
		}
		idx := wramIndex(addr)
		if !watch[idx] {
			return
		}
		rec := makeHiganCPUWriteRec(goSys, curFrame, addr, value)
		last[idx] = rec
		history[idx] = append(history[idx], rec)
	}

	first := make(map[uint32]higanCPUFirstDivergence)
	for frame := 0; frame < tc.Frames; frame++ {
		curFrame = frame
		if err := goSys.Run(); err != nil {
			t.Fatal(err)
		}
		bsn.Run()
		s9x.Run()
		for _, target := range targets {
			if _, ok := first[target.Addr]; ok {
				continue
			}
			got := wram.read(goSys, target.Addr)
			bsnesByte := bsn.PeekMemory(wram.id, target.Addr)
			snes9xByte := s9x.PeekMemory(wram.id, target.Addr)
			if bsnesByte != snes9xByte || got == bsnesByte {
				continue
			}
			rec, have := last[target.Addr]
			first[target.Addr] = higanCPUFirstDivergence{
				frame:     frame,
				addr:      target.Addr,
				goValue:   got,
				refValue:  bsnesByte,
				bsnes:     bsnesByte,
				snes9x:    snes9xByte,
				lastWrite: rec,
				haveWrite: have,
			}
		}
	}

	for _, target := range targets {
		got := wram.read(goSys, target.Addr)
		bsnesByte := bsn.PeekMemory(wram.id, target.Addr)
		snes9xByte := s9x.PeekMemory(wram.id, target.Addr)
		if bsnesByte != snes9xByte {
			t.Fatalf("%s WRAM $%04X no longer reference-agreeing: bsnes=%02X snes9x=%02X",
				tc.Name, target.Addr, bsnesByte, snes9xByte)
		}
		if got != target.Go || bsnesByte != target.Ref {
			t.Fatalf("%s WRAM $%04X residual changed: Go=%02X Ref=%02X, want Go=%02X Ref=%02X",
				tc.Name, target.Addr, got, bsnesByte, target.Go, target.Ref)
		}
		divergence, ok := first[target.Addr]
		if !ok {
			t.Fatalf("%s WRAM $%04X ends divergent but no frame-level first divergence was recorded", tc.Name, target.Addr)
		}
		t.Logf("%s first WRAM divergence at $%04X after frame %d: Go=%02X Ref=%02X bsnes=%02X snes9x=%02X",
			tc.Name, divergence.addr, divergence.frame, divergence.goValue, divergence.refValue, divergence.bsnes, divergence.snes9x)
		if divergence.haveWrite {
			logHiganCPUWriteRec(t, tc.Name, "last write before first divergence", divergence.lastWrite)
		} else {
			t.Logf("%s WRAM $%04X first divergence has no captured Go CPU write before it", tc.Name, target.Addr)
		}
		logHiganCPUWriteTail(t, tc.Name, target.Addr, history[target.Addr])
	}
}

func runHiganADCSBCGoInstructionTraceToFirstCRCWrite(t *testing.T, name string) ([]cpuInstructionEvent, higanCPUWriteRec) {
	t.Helper()
	tc, ok := higanManifestCase(t, name)
	if !ok {
		t.Fatalf("%s has no %s row", higanTestROMManifestPath, name)
	}
	targets := higanWRAMKnownDivergences(tc)
	if len(targets) == 0 {
		t.Fatalf("%s has no WRAM known divergences to trace", name)
	}
	rom := readHiganROM(t, tc)
	sys := newHiganGoSystem(t, rom)
	watch := make(map[uint32]bool)
	for _, target := range targets {
		watch[target.Addr] = true
	}
	var hit higanCPUWriteRec
	var haveHit bool
	sys.Bus.WriteHook = func(addr uint32, value uint8) {
		if haveHit || !isWRAM(addr) {
			return
		}
		idx := wramIndex(addr)
		if !watch[idx] {
			return
		}
		hit = makeHiganCPUWriteRec(sys, sys.PPU.SaveState().FrameCount, addr, value)
		haveHit = true
	}

	var instructions []cpuInstructionEvent
	for !haveHit {
		c := sys.CPU
		ppuState := sys.PPU.SaveState()
		instructions = append(instructions, cpuInstructionEvent{
			Frame:    ppuState.FrameCount,
			Cycles:   c.Cycles,
			PB:       c.PB,
			PC:       c.PC,
			Opcode:   sys.Bus.Read(uint32(c.PB)<<16 | uint32(c.PC)),
			Operand0: sys.Bus.Read(uint32(c.PB)<<16 | uint32(c.PC+1)),
			Operand1: sys.Bus.Read(uint32(c.PB)<<16 | uint32(c.PC+2)),
			HCounter: uint16(ppuState.HCounter),
			VCounter: uint16(ppuState.VCounter),
			Field:    boolByte(ppuState.PPUField),
			A:        c.A,
			X:        c.X,
			Y:        c.Y,
			P:        c.P,
			DB:       c.DB,
			D:        c.D,
			S:        c.S,
			Disasm:   disasm.Disassemble65816(c, sys.Bus),
		})
		c.Run()
		if len(instructions) > 1000000 {
			t.Fatalf("%s Go trace did not reach watched CRC write within 1000000 CPU instructions", name)
		}
	}
	return instructions, hit
}

func compareHiganADCSBCInstructionTrace(t *testing.T, goTrace, refTrace []cpuInstructionEvent, stopCycle uint64) {
	t.Helper()
	if len(goTrace) == 0 || len(refTrace) == 0 {
		t.Fatalf("cannot compare empty ADC/SBC instruction traces: Go=%d Ref=%d", len(goTrace), len(refTrace))
	}
	n := minInt(len(goTrace), len(refTrace))
	lastDelta := int64(refTrace[0].Cycles) - int64(goTrace[0].Cycles)
	for i := 0; i < n; i++ {
		goEv := goTrace[i]
		refEv := refTrace[i]
		if goEv.PB != refEv.PB || goEv.PC != refEv.PC || goEv.Opcode != refEv.Opcode {
			t.Logf("ADC/SBC instruction sequence split row %d: Go cycle=%d PB:PC=%02X:%04X opcode=%02X A/X/Y/P=%04X/%04X/%04X/%02X; Ref cycle=%d PB:PC=%02X:%04X opcode=%02X A/X/Y/P=%04X/%04X/%04X/%02X",
				i, goEv.Cycles, goEv.PB, goEv.PC, goEv.Opcode, goEv.A, goEv.X, goEv.Y, goEv.P,
				refEv.Cycles, refEv.PB, refEv.PC, refEv.Opcode, refEv.A, refEv.X, refEv.Y, refEv.P)
			return
		}
		delta := int64(refEv.Cycles) - int64(goEv.Cycles)
		if delta != lastDelta {
			t.Logf("ADC/SBC first cycle-delta change row %d: PB:PC=%02X:%04X opcode=%02X previous_delta=%d current_delta=%d Go cycle=%d Ref cycle=%d A/X/Y/P Go=%04X/%04X/%04X/%02X Ref=%04X/%04X/%04X/%02X",
				i, goEv.PB, goEv.PC, goEv.Opcode, lastDelta, delta, goEv.Cycles, refEv.Cycles,
				goEv.A, goEv.X, goEv.Y, goEv.P, refEv.A, refEv.X, refEv.Y, refEv.P)
			lastDelta = delta
		}
		if goEv.Cycles >= stopCycle {
			t.Logf("ADC/SBC traces stayed instruction-aligned through Go cycle %d at row %d before first watched CRC write", stopCycle, i)
			return
		}
	}
	t.Logf("ADC/SBC compared %d instruction rows without sequence split; Go rows=%d Ref rows=%d stopCycle=%d", n, len(goTrace), len(refTrace), stopCycle)
}

func higanWRAMKnownDivergences(tc higanTestROMCase) []higanTestROMKnownDivergence {
	var out []higanTestROMKnownDivergence
	for _, divergence := range tc.KnownDivergences {
		if divergence.Region == "WRAM" {
			out = append(out, divergence)
		}
	}
	return out
}

func readHiganROM(t *testing.T, tc higanTestROMCase) []byte {
	t.Helper()
	checkFile(t, tc.Path)
	rom, err := os.ReadFile(tc.Path)
	if err != nil {
		t.Fatal(err)
	}
	if tc.SHA256 != "" {
		if got := hashBytes(rom); got != tc.SHA256 {
			t.Fatalf("%s sha256 = %s, want %s", tc.Path, got, tc.SHA256)
		}
	}
	return rom
}

func newHiganGoSystem(t *testing.T, rom []byte) *snes.System {
	t.Helper()
	sys := snes.NewSystem(nil)
	if err := sys.LoadROM(rom); err != nil {
		t.Fatal(err)
	}
	sys.Power()
	return sys
}

func newHiganReference(t *testing.T, corePath, romPath string) *libretro.Bridge {
	t.Helper()
	checkFile(t, corePath)
	core, err := libretro.New(corePath)
	if err != nil {
		t.Fatal(err)
	}
	core.Logger = t
	core.Init()
	if !core.LoadGame(romPath) {
		t.Fatalf("load %s", romPath)
	}
	return core
}

func makeHiganCPUWriteRec(sys *snes.System, frame int, addr uint32, value uint8) higanCPUWriteRec {
	c := sys.CPU
	return higanCPUWriteRec{
		frame:  frame,
		cycles: c.Cycles,
		pb:     c.PB,
		pc:     c.PC,
		addr:   addr,
		value:  value,
		a:      c.A,
		x:      c.X,
		y:      c.Y,
		p:      c.P,
		db:     c.DB,
		d:      c.D,
		s:      c.S,
		disasm: disasm.Disassemble65816(c, sys.Bus),
	}
}

func logHiganCPUWriteTail(t *testing.T, name string, addr uint32, trace []higanCPUWriteRec) {
	t.Helper()
	if len(trace) == 0 {
		t.Logf("%s WRAM $%04X has no captured Go CPU writes", name, addr)
		return
	}
	start := 0
	if len(trace) > 4 {
		start = len(trace) - 4
	}
	for i := start; i < len(trace); i++ {
		logHiganCPUWriteRec(t, name, "write tail", trace[i])
	}
}

func logHiganCPUWriteRec(t *testing.T, name, label string, rec higanCPUWriteRec) {
	t.Helper()
	t.Logf("%s %s: frame=%d cycle=%d PB:PC=%02X:%04X addr=%06X value=%02X A/X/Y/P=%04X/%04X/%04X/%02X DB/D/S=%02X/%04X/%04X ; %s",
		name, label, rec.frame, rec.cycles, rec.pb, rec.pc, rec.addr, rec.value,
		rec.a, rec.x, rec.y, rec.p, rec.db, rec.d, rec.s, rec.disasm)
}
