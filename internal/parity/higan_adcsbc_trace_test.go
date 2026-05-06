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
