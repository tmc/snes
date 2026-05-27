package testroms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/parity/libretro"
	"github.com/tmc/snes/internal/ppu"
)

func TestPinballDreamsVRAMOneByteProbe(t *testing.T) {
	if os.Getenv("PINBALL_DREAMS_PROBE") == "" {
		t.Skip("set PINBALL_DREAMS_PROBE=1")
	}
	romPath := os.Getenv("PINBALL_DREAMS_ROM")
	if romPath == "" {
		for _, rom := range allTestROMs(t) {
			if strings.EqualFold(filepath.Base(rom), "Pinball Dreams (USA).sfc") {
				romPath = rom
				break
			}
		}
	}
	if romPath == "" {
		t.Fatal("Pinball Dreams ROM not found")
	}
	rom, err := os.ReadFile(romPath)
	if err != nil {
		t.Fatalf("read ROM: %v", err)
	}

	const frame = 30
	sys := snes.NewSystem(nil)
	if err := sys.LoadROM(rom); err != nil {
		t.Fatalf("LoadROM: %v", err)
	}
	sys.Power()
	for i := 0; i < frame; i++ {
		if err := sys.Run(); err != nil {
			t.Fatalf("Go frame %d: %v", i+1, err)
		}
	}

	corePath, err := allROMCorePath("snes9x")
	if err != nil {
		t.Fatal(err)
	}
	core, err := libretro.New(corePath)
	if err != nil {
		t.Fatalf("libretro.New: %v", err)
	}
	core.Init()
	if !core.LoadGame(romPath) {
		t.Fatalf("LoadGame %s", romPath)
	}
	for i := 0; i < frame; i++ {
		core.Run()
	}

	var addrs []uint32
	for addr := uint32(0); addr < 0x10000; addr++ {
		g := sys.PPU.VRAM[addr]
		r := core.PeekMemory(3, addr)
		if g == r || (g == 0x00 && r == 0x55) {
			continue
		}
		addrs = append(addrs, addr)
		t.Logf("diff vram[%04x]: go=%02x ref=%02x", addr, g, r)
	}
	if len(addrs) == 0 {
		t.Fatal("no diff found")
	}

	watch := map[uint32]bool{}
	for _, addr := range addrs {
		watch[addr] = true
	}
	type write struct {
		frame    int
		space    string
		addr     uint32
		register uint16
		before   uint8
		after    uint8
		pb       uint8
		pc       uint16
		op       uint8
		h        uint16
		v        uint16
		inidisp  uint8
		vmain    uint8
		vmaddr   uint16
		y        uint16
		p        uint8
	}
	var writes []write
	var ioWrites []write
	sys = snes.NewSystem(nil)
	if err := sys.LoadROM(rom); err != nil {
		t.Fatalf("reload ROM: %v", err)
	}
	sys.Power()
	curFrame := 0
	sys.Bus.WriteHook = func(addr uint32, value uint8) {
		if addr != 0x2100 && (addr < 0x2115 || addr > 0x2119) {
			return
		}
		if curFrame < 28 {
			return
		}
		h, v := sys.PPU.LatchBeam()
		ioWrites = append(ioWrites, write{
			frame:   curFrame,
			space:   "mmio",
			addr:    addr,
			after:   value,
			pb:      sys.CPU.LastOpcodePB,
			pc:      sys.CPU.LastOpcodePC,
			op:      sys.CPU.LastOpcode,
			h:       h,
			v:       v,
			inidisp: sys.PPU.INIDISP,
			vmain:   sys.PPU.VRAMIncMode,
			vmaddr:  sys.PPU.VRAMAddr,
			y:       sys.CPU.Y,
			p:       sys.CPU.P,
		})
	}
	sys.PPU.WriteHook = func(ev ppu.WriteEvent) {
		if ev.Space != "vram" || !watch[ev.Addr] {
			return
		}
		h, v := sys.PPU.LatchBeam()
		writes = append(writes, write{
			frame:    curFrame,
			space:    ev.Space,
			addr:     ev.Addr,
			register: ev.Register,
			before:   ev.Before,
			after:    ev.After,
			pb:       sys.CPU.LastOpcodePB,
			pc:       sys.CPU.LastOpcodePC,
			op:       sys.CPU.LastOpcode,
			h:        h,
			v:        v,
			inidisp:  sys.PPU.INIDISP,
			vmain:    sys.PPU.VRAMIncMode,
			vmaddr:   sys.PPU.VRAMAddr,
			y:        sys.CPU.Y,
			p:        sys.CPU.P,
		})
	}
	for i := 0; i < frame; i++ {
		curFrame = i + 1
		if err := sys.Run(); err != nil {
			t.Fatalf("Go traced frame %d: %v", i+1, err)
		}
	}
	if len(writes) == 0 {
		t.Log("no Go direct writes to differing VRAM byte")
	}
	for _, w := range writes {
		t.Logf("write f=%d h=%d v=%d P=%02x Y=%04x INIDISP=%02x VMAIN=%02x VMADDR=%04x %s[%04x] %02x->%02x via $%04x at %02x:%04x op=%02x",
			w.frame, w.h, w.v, w.p, w.y, w.inidisp, w.vmain, w.vmaddr, w.space, w.addr, w.before, w.after, w.register, w.pb, w.pc, w.op)
	}
	t.Log("--- IO writes f>=28 to $2100/$2115..$2119 ---")
	for _, w := range ioWrites {
		t.Logf("io f=%d h=%d v=%d P=%02x Y=%04x INIDISP=%02x VMAIN=%02x VMADDR=%04x [%04x]=%02x at %02x:%04x op=%02x",
			w.frame, w.h, w.v, w.p, w.y, w.inidisp, w.vmain, w.vmaddr, w.addr, w.after, w.pb, w.pc, w.op)
	}
}

func TestVRAMResidualProbe(t *testing.T) {
	if os.Getenv("VRAM_RESIDUAL_PROBE") == "" {
		t.Skip("set VRAM_RESIDUAL_PROBE=1")
	}
	romName := os.Getenv("VRAM_RESIDUAL_ROM_NAME")
	romPath := os.Getenv("VRAM_RESIDUAL_ROM")
	if romPath == "" {
		for _, rom := range allTestROMs(t) {
			if filepath.Base(rom) == romName {
				romPath = rom
				break
			}
		}
	}
	if romPath == "" {
		t.Fatalf("ROM not found: set VRAM_RESIDUAL_ROM or VRAM_RESIDUAL_ROM_NAME")
	}
	goStart := envInt("VRAM_RESIDUAL_GO_START", 30)
	goWindow := envInt("VRAM_RESIDUAL_GO_WINDOW", 1)
	refWindow := envInt("VRAM_RESIDUAL_REF_WINDOW", 90)
	if goWindow < 1 {
		goWindow = 1
	}
	rom, err := os.ReadFile(romPath)
	if err != nil {
		t.Fatalf("read ROM: %v", err)
	}

	sys := snes.NewSystem(nil)
	if err := sys.LoadROM(rom); err != nil {
		t.Fatalf("LoadROM: %v", err)
	}
	sys.Power()
	goVRAM := make([][]byte, goWindow)
	for i := 0; i < goStart+goWindow-1; i++ {
		if err := sys.Run(); err != nil {
			t.Fatalf("Go frame %d: %v", i+1, err)
		}
		if i >= goStart-1 {
			vram := make([]byte, len(sys.PPU.VRAM))
			copy(vram, sys.PPU.VRAM[:])
			goVRAM[i-goStart+1] = vram
		}
	}

	corePath, err := allROMCorePath("snes9x")
	if err != nil {
		t.Fatal(err)
	}
	core, err := libretro.New(corePath)
	if err != nil {
		t.Fatalf("libretro.New: %v", err)
	}
	core.Init()
	if !core.LoadGame(romPath) {
		t.Fatalf("LoadGame %s", romPath)
	}

	bestDiffs := 1 << 30
	bestRef := 0
	bestGo := 0
	var bestRefVRAM []byte
	for refFrame := 1; refFrame <= refWindow; refFrame++ {
		core.Run()
		ref := make([]byte, 0x10000)
		for addr := uint32(0); addr < 0x10000; addr++ {
			ref[addr] = core.PeekMemory(3, addr)
		}
		for gi, goBuf := range goVRAM {
			diffs := normalizedDiffBytes(goBuf, ref)
			if len(diffs) < bestDiffs {
				bestDiffs = len(diffs)
				bestRef = refFrame
				bestGo = goStart + gi
				bestRefVRAM = ref
			}
		}
	}
	t.Logf("best rom=%s diffs=%d go_frame=%d ref_frame=%d", filepath.Base(romPath), bestDiffs, bestGo, bestRef)
	diffs := normalizedDiffBytes(goVRAM[bestGo-goStart], bestRefVRAM)
	if len(diffs) > 32 {
		diffs = diffs[:32]
	}
	for _, addr := range diffs {
		t.Logf("diff vram[%04x]: go=%02x ref=%02x", addr, goVRAM[bestGo-goStart][addr], bestRefVRAM[addr])
	}
}

func normalizedDiffBytes(goVRAM, refVRAM []byte) []uint32 {
	var diffs []uint32
	for addr := uint32(0); addr < 0x10000; addr++ {
		g := goVRAM[addr]
		r := refVRAM[addr]
		if g == r || (g == 0x00 && r == 0x55) {
			continue
		}
		diffs = append(diffs, addr)
	}
	return diffs
}
