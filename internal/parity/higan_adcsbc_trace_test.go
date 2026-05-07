package parity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"
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
	refInstructions, refStatus, _, _, _, summary := readReferenceCPUMulJSONL(t, raw)
	refWrites := readHiganADCSBCReferenceWRAMWrites(t, raw)
	if len(refInstructions) == 0 {
		t.Fatalf("ADC/SBC reference trace %s has no instruction rows", path)
	}
	goInstructions, goStatus, write := runHiganADCSBCGoInstructionTraceToFirstCRCWrite(t, "ADC8")
	t.Logf("ADC/SBC reference trace %s rows=%d sha256=%s instruction_rows=%d",
		path, summary["rows"], hashBytes(raw), len(refInstructions))
	t.Logf("ADC8 first watched Go CRC write: frame=%d cycle=%d PB:PC=%02X:%04X addr=%06X value=%02X A/X/Y/P=%04X/%04X/%04X/%02X ; %s",
		write.frame, write.cycles, write.pb, write.pc, write.addr, write.value,
		write.a, write.x, write.y, write.p, write.disasm)
	compareHiganADCSBCInstructionTrace(t, goInstructions, refInstructions, write.cycles)
	logHiganADCSBCGoAtCycle(t, goInstructions, write.cycles)
	logHiganADCSBCReferenceAtCycle(t, refInstructions, write.cycles)
	logHiganADCSBCReferenceWriteMatch(t, refWrites, write)
	compareHiganADCSBCStatusTrace(t, goStatus, refStatus, write.cycles)
	logHiganADCSBCStatusTail(t, "Go", goStatus, write.cycles)
	logHiganADCSBCStatusTail(t, "Ref", refStatus, write.cycles)
}

func readHiganADCSBCReferenceWRAMWrites(t *testing.T, raw []byte) []higanCPUWriteRec {
	t.Helper()
	var writes []higanCPUWriteRec
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal(line, &fields); err != nil {
			t.Fatalf("decode ADC/SBC reference write trace: %v", err)
		}
		kind := strings.ToLower(jsonStringFieldDefault(fields, "kind"))
		if kind != "wram-write" {
			continue
		}
		writes = append(writes, higanCPUWriteRec{
			frame:  int(jsonNumberFieldDefault(fields, "frame")),
			cycles: jsonNumberFieldDefaultAny(fields, "cycles", "cycle"),
			pb:     uint8(jsonNumberFieldDefault(fields, "pb")),
			pc:     uint16(jsonNumberFieldDefault(fields, "pc")),
			addr:   uint32(jsonNumberFieldDefaultAny(fields, "offset", "addr")),
			value:  uint8(jsonNumberFieldDefaultAny(fields, "value", "data")),
			a:      uint16(jsonNumberFieldDefault(fields, "a")),
			x:      uint16(jsonNumberFieldDefault(fields, "x")),
			y:      uint16(jsonNumberFieldDefault(fields, "y")),
			p:      uint8(jsonNumberFieldDefault(fields, "p")),
			db:     uint8(jsonNumberFieldDefault(fields, "db")),
			d:      uint16(jsonNumberFieldDefault(fields, "d")),
			s:      uint16(jsonNumberFieldDefault(fields, "s")),
		})
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return writes
}

func logHiganADCSBCReferenceWriteMatch(t *testing.T, refWrites []higanCPUWriteRec, goWrite higanCPUWriteRec) {
	t.Helper()
	if len(refWrites) == 0 {
		t.Log("ADC/SBC reference trace has no WRAM write rows; set BSNES_ADCSBC_TRACE_WRITES=1 when generating it")
		return
	}
	for i, ref := range refWrites {
		if ref.addr == wramIndex(goWrite.addr) && ref.value == goWrite.value && ref.pb == goWrite.pb && ref.pc == goWrite.pc {
			t.Logf("ADC/SBC matching Ref WRAM write[%d]: frame=%d cycle=%d PB:PC=%02X:%04X addr=%06X value=%02X A/X/Y/P=%04X/%04X/%04X/%02X; Go frame=%d cycle=%d",
				i, ref.frame, ref.cycles, ref.pb, ref.pc, ref.addr, ref.value,
				ref.a, ref.x, ref.y, ref.p, goWrite.frame, goWrite.cycles)
			return
		}
	}
	t.Logf("ADC/SBC reference trace has %d WRAM write rows but none match Go addr=%04X value=%02X PB:PC=%02X:%04X",
		len(refWrites), wramIndex(goWrite.addr), goWrite.value, goWrite.pb, goWrite.pc)
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

func runHiganADCSBCGoInstructionTraceToFirstCRCWrite(t *testing.T, name string) ([]cpuInstructionEvent, []cpuStatusEvent, higanCPUWriteRec) {
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
	var statusTrace []cpuStatusEvent
	var frame int
	wrapCPUStatusTracePages(sys, &frame, &statusTrace)
	sys.Bus.WriteHook = func(addr uint32, value uint8) {
		if haveHit || !isWRAM(addr) {
			return
		}
		pc := sys.CPU.PC
		if sys.CPU.PB != 0 || (pc != 0x812d && pc != 0x8143) {
			return
		}
		if value == 0xff {
			return
		}
		idx := wramIndex(addr)
		if !watch[idx] {
			return
		}
		hit = makeHiganCPUWriteRec(sys, frame, addr, value)
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
		frame = sys.PPU.SaveState().FrameCount
		if len(instructions) > 1000000 {
			t.Fatalf("%s Go trace did not reach watched CRC write within 1000000 CPU instructions", name)
		}
	}
	return instructions, statusTrace, hit
}

func compareHiganADCSBCInstructionTrace(t *testing.T, goTrace, refTrace []cpuInstructionEvent, stopCycle uint64) {
	t.Helper()
	if len(goTrace) == 0 || len(refTrace) == 0 {
		t.Fatalf("cannot compare empty ADC/SBC instruction traces: Go=%d Ref=%d", len(goTrace), len(refTrace))
	}
	n := minInt(len(goTrace), len(refTrace))
	lastDelta := int64(refTrace[0].Cycles) - int64(goTrace[0].Cycles)
	loggedDelta := false
	for i := 0; i < n; i++ {
		goEv := goTrace[i]
		refEv := refTrace[i]
		if goEv.PB != refEv.PB || goEv.PC != refEv.PC || goEv.Opcode != refEv.Opcode {
			t.Logf("ADC/SBC instruction sequence split row %d: Go cycle=%d PB:PC=%02X:%04X opcode=%02X A/X/Y/P=%04X/%04X/%04X/%02X; Ref cycle=%d PB:PC=%02X:%04X opcode=%02X A/X/Y/P=%04X/%04X/%04X/%02X",
				i, goEv.Cycles, goEv.PB, goEv.PC, goEv.Opcode, goEv.A, goEv.X, goEv.Y, goEv.P,
				refEv.Cycles, refEv.PB, refEv.PC, refEv.Opcode, refEv.A, refEv.X, refEv.Y, refEv.P)
			return
		}
		if goEv.A != refEv.A || goEv.X != refEv.X || goEv.Y != refEv.Y || goEv.P != refEv.P ||
			goEv.DB != refEv.DB || goEv.D != refEv.D || goEv.S != refEv.S {
			t.Logf("ADC/SBC first CPU state split row %d: PB:PC=%02X:%04X opcode=%02X Go cycle=%d H/V/F=%d/%d/%d A/X/Y/P/DB/D/S=%04X/%04X/%04X/%02X/%02X/%04X/%04X; Ref cycle=%d H/V/F=%d/%d/%d A/X/Y/P/DB/D/S=%04X/%04X/%04X/%02X/%02X/%04X/%04X",
				i, goEv.PB, goEv.PC, goEv.Opcode,
				goEv.Cycles, goEv.HCounter, goEv.VCounter, goEv.Field, goEv.A, goEv.X, goEv.Y, goEv.P, goEv.DB, goEv.D, goEv.S,
				refEv.Cycles, refEv.HCounter, refEv.VCounter, refEv.Field, refEv.A, refEv.X, refEv.Y, refEv.P, refEv.DB, refEv.D, refEv.S)
			return
		}
		delta := int64(refEv.Cycles) - int64(goEv.Cycles)
		if delta != lastDelta && !loggedDelta {
			t.Logf("ADC/SBC first cycle-delta change row %d: PB:PC=%02X:%04X opcode=%02X previous_delta=%d current_delta=%d Go cycle=%d Ref cycle=%d A/X/Y/P Go=%04X/%04X/%04X/%02X Ref=%04X/%04X/%04X/%02X",
				i, goEv.PB, goEv.PC, goEv.Opcode, lastDelta, delta, goEv.Cycles, refEv.Cycles,
				goEv.A, goEv.X, goEv.Y, goEv.P, refEv.A, refEv.X, refEv.Y, refEv.P)
			loggedDelta = true
		}
		lastDelta = delta
		if goEv.Cycles >= stopCycle {
			t.Logf("ADC/SBC traces stayed instruction-aligned through Go cycle %d at row %d before first watched CRC write", stopCycle, i)
			return
		}
	}
	t.Logf("ADC/SBC compared %d instruction rows without sequence split; Go rows=%d Ref rows=%d stopCycle=%d", n, len(goTrace), len(refTrace), stopCycle)
}

func logHiganADCSBCGoAtCycle(t *testing.T, goTrace []cpuInstructionEvent, cycle uint64) {
	t.Helper()
	if len(goTrace) == 0 {
		return
	}
	idx := 0
	for idx+1 < len(goTrace) && goTrace[idx+1].Cycles <= cycle {
		idx++
	}
	for i := idx - 2; i <= idx+2; i++ {
		if i < 0 || i >= len(goTrace) {
			continue
		}
		ev := goTrace[i]
		t.Logf("ADC/SBC Go near CRC write[%d]: cycle=%d PB:PC=%02X:%04X opcode=%02X H/V/F=%d/%d/%d A/X/Y/P=%04X/%04X/%04X/%02X DB/D/S=%02X/%04X/%04X ; %s",
			i, ev.Cycles, ev.PB, ev.PC, ev.Opcode, ev.HCounter, ev.VCounter, ev.Field,
			ev.A, ev.X, ev.Y, ev.P, ev.DB, ev.D, ev.S, ev.Disasm)
	}
}

func logHiganADCSBCReferenceAtCycle(t *testing.T, refTrace []cpuInstructionEvent, cycle uint64) {
	t.Helper()
	if len(refTrace) == 0 {
		return
	}
	idx := 0
	for idx+1 < len(refTrace) && refTrace[idx+1].Cycles <= cycle {
		idx++
	}
	for i := idx - 2; i <= idx+2; i++ {
		if i < 0 || i >= len(refTrace) {
			continue
		}
		ev := refTrace[i]
		t.Logf("ADC/SBC Ref near Go CRC write[%d]: cycle=%d PB:PC=%02X:%04X opcode=%02X H/V/F=%d/%d/%d A/X/Y/P=%04X/%04X/%04X/%02X DB/D/S=%02X/%04X/%04X ; %s",
			i, ev.Cycles, ev.PB, ev.PC, ev.Opcode, ev.HCounter, ev.VCounter, ev.Field,
			ev.A, ev.X, ev.Y, ev.P, ev.DB, ev.D, ev.S, ev.Disasm)
	}
}

func logHiganADCSBCStatusTail(t *testing.T, label string, trace []cpuStatusEvent, stopCycle uint64) {
	t.Helper()
	var filtered []cpuStatusEvent
	for _, ev := range trace {
		if ev.Cycles > stopCycle {
			break
		}
		if ev.Addr == 0x4212 {
			filtered = append(filtered, ev)
		}
	}
	if len(filtered) == 0 {
		t.Logf("ADC/SBC %s has no $4212 status reads before cycle %d", label, stopCycle)
		return
	}
	start := 0
	if len(filtered) > 8 {
		start = len(filtered) - 8
	}
	t.Logf("ADC/SBC %s $4212 reads before cycle %d: total=%d", label, stopCycle, len(filtered))
	for i := start; i < len(filtered); i++ {
		ev := filtered[i]
		t.Logf("ADC/SBC %s $4212[%d]: cycle=%d PB:PC=%02X:%04X value=%02X cpuH/V=%d/%d ppuH/V/F=%d/%d/%d P=%02X A=%04X ; %s",
			label, i, ev.Cycles, ev.PB, ev.PC, ev.Value, ev.HCounter, ev.VCounter,
			ev.PPUHCounter, ev.PPUVCounter, ev.PPUField, ev.P, ev.A, ev.Disasm)
	}
}

func compareHiganADCSBCStatusTrace(t *testing.T, goTrace, refTrace []cpuStatusEvent, stopCycle uint64) {
	t.Helper()
	go4212 := higanStatus4212Before(goTrace, stopCycle)
	ref4212 := higanStatus4212Before(refTrace, stopCycle)
	n := minInt(len(go4212), len(ref4212))
	for i := 0; i < n; i++ {
		g, r := go4212[i], ref4212[i]
		if g.Cycles != r.Cycles || g.PB != r.PB || g.PC != r.PC || g.Value != r.Value {
			t.Logf("ADC/SBC first $4212 split row %d: Go cycle=%d PB:PC=%02X:%04X value=%02X cpuH/V=%d/%d ppuH/V/F=%d/%d/%d P=%02X; Ref cycle=%d PB:PC=%02X:%04X value=%02X cpuH/V=%d/%d ppuH/V/F=%d/%d/%d P=%02X",
				i,
				g.Cycles, g.PB, g.PC, g.Value, g.HCounter, g.VCounter, g.PPUHCounter, g.PPUVCounter, g.PPUField, g.P,
				r.Cycles, r.PB, r.PC, r.Value, r.HCounter, r.VCounter, r.PPUHCounter, r.PPUVCounter, r.PPUField, r.P)
			return
		}
	}
	if len(go4212) != len(ref4212) {
		t.Logf("ADC/SBC $4212 traces match through %d rows but lengths differ: Go=%d Ref=%d",
			n, len(go4212), len(ref4212))
		return
	}
	t.Logf("ADC/SBC $4212 trace matches through %d reads before cycle %d", n, stopCycle)
}

func higanStatus4212Before(trace []cpuStatusEvent, stopCycle uint64) []cpuStatusEvent {
	var out []cpuStatusEvent
	for _, ev := range trace {
		if ev.Cycles > stopCycle {
			break
		}
		if ev.Addr == 0x4212 {
			out = append(out, ev)
		}
	}
	return out
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
