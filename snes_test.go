package snes

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/snes/emulator"
	"github.com/tmc/snes/internal/apu"
	"github.com/tmc/snes/internal/bus"
	"github.com/tmc/snes/internal/cartridge"
	"github.com/tmc/snes/internal/input"
)

func TestIODeviceJoypadSerialRead(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}

	sys.Controller1.SetButton(input.ButtonB, true)
	sys.Controller1.SetButton(input.ButtonStart, true)
	sys.Controller1.SetButton(input.ButtonR, true)

	io.Write(0x4016, 1)
	io.Write(0x4016, 0)

	want := []uint8{
		1, 0, 0, 1,
		0, 0, 0, 0,
		0, 0, 0, 1,
		0, 0, 0, 0,
		1,
	}
	for i, want := range want {
		if got := io.Read(0x4016) & 1; got != want {
			t.Fatalf("read %d = %d, want %d", i, got, want)
		}
	}
}

func TestIODeviceJoypadSerialReadPreservesOpenBusBits(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}

	sys.Bus.MDR = 0xa5
	if got := io.Read(0x4016); got != 0xa4 {
		t.Fatalf("$4016 idle serial read = %02X, want A4", got)
	}
	sys.Bus.MDR = 0xa5
	if got := io.Read(0x4017); got != 0xbc {
		t.Fatalf("$4017 disconnected serial read = %02X, want BC", got)
	}

	if err := sys.Connect(1, 1); err != nil {
		t.Fatalf("connect port2: %v", err)
	}

	sys.Controller1.SetButton(input.ButtonB, true)
	sys.Controller2.SetButton(input.ButtonB, true)
	io.Write(0x4016, 1)
	io.Write(0x4016, 0)

	sys.Bus.MDR = 0xa5
	if got := io.Read(0x4016); got != 0xa5 {
		t.Fatalf("$4016 pressed serial read = %02X, want A5", got)
	}
	sys.Bus.MDR = 0xa5
	if got := io.Read(0x4017); got != 0xbd {
		t.Fatalf("$4017 pressed serial read = %02X, want BD", got)
	}
}

func TestIODeviceAutoJoypadSnapshot(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}

	sys.Controller1.SetButton(input.ButtonA, true)
	if err := sys.Connect(1, 1); err != nil {
		t.Fatalf("connect port 2: %v", err)
	}
	sys.Controller2.SetButton(input.ButtonB, true)
	io.Write(0x4200, 0x01)

	if got := io.Read(0x4218); got != 0x80 {
		t.Fatalf("JOY1L = %02X, want 80", got)
	}
	if got := io.Read(0x4219); got != 0x00 {
		t.Fatalf("JOY1H = %02X, want 00", got)
	}
	if got := io.Read(0x421A); got != 0x00 {
		t.Fatalf("JOY2L = %02X, want 00", got)
	}
	if got := io.Read(0x421B); got != 0x80 {
		t.Fatalf("JOY2H = %02X, want 80", got)
	}
}

func TestIODeviceAPUPortReadStopsBeforePortYield(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}

	prepareAPUOutputPortYield(t, sys, 0x5A)
	if got := sys.APU.ReadPort(0); got != 0 {
		t.Fatalf("port before CPU read = %02X, want 00", got)
	}

	// 83 master clocks round up to the assignment boundary at SMP clock 8.
	sys.CPU.Cycles = 83
	if got := io.Read(0x2140); got != 0x00 {
		t.Fatalf("first $2140 read = %02X, want 00", got)
	}
	if got := sys.APU.ReadPort(0); got != 0x00 {
		t.Fatalf("port after first $2140 read = %02X, want 00", got)
	}

	sys.CPU.Cycles += 12
	sys.Scheduler.Sync(sys.APU)
	if got := io.Read(0x2140); got != 0x5A {
		t.Fatalf("later $2140 read = %02X, want 5A", got)
	}
}

func TestIODeviceAPUPortWriteUsesPortSync(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}

	prepareAPUOutputPortYield(t, sys, 0xA5)
	sys.CPU.Cycles = 83
	io.Write(0x2141, 0x77)

	if got := sys.APU.InPorts[1]; got != 0x77 {
		t.Fatalf("APU input port 1 = %02X, want 77", got)
	}
	if got := sys.APU.ReadPort(0); got != 0xA5 {
		t.Fatalf("APU output port after CPU write = %02X, want A5", got)
	}
}

func prepareAPUOutputPortYield(t *testing.T, sys *System, value uint8) {
	t.Helper()

	sys.APU.Control = 0
	sys.APU.Processor.PC = 0x0200
	sys.APU.Processor.A = value
	sys.APU.RAM[0x0200] = 0xC4 // MOV dp,A
	sys.APU.RAM[0x0201] = 0xF4
	result := sys.APU.RunUntilTarget(8, apu.SyncPostCPU)
	if result.Yield != apu.YieldAPUPortWrite {
		t.Fatalf("test setup yield = %d, want %d", result.Yield, apu.YieldAPUPortWrite)
	}
}

func TestIODeviceAutoJoypadEnablesHVBJOYBusy(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}

	sys.CPU.Cycles = 225*1364 + 128

	io.Write(0x4200, 0x01)
	if !sys.PPU.AutoJoypad {
		t.Fatalf("PPU AutoJoypad = false, want true after $4200 bit 0")
	}
	if got := io.Read(0x4212); got&0x01 == 0 {
		t.Fatalf("HVBJOY during auto-joypad busy = %02X, want bit0 set", got)
	}

	io.Write(0x4200, 0x00)
	if sys.PPU.AutoJoypad {
		t.Fatalf("PPU AutoJoypad = true, want false after $4200 bit 0 clear")
	}
	if got := io.Read(0x4212); got&0x01 != 0 {
		t.Fatalf("HVBJOY after auto-joypad disable = %02X, want bit0 clear", got)
	}
}

func TestIODeviceHVBJOYUsesPALBeamTiming(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}

	sys.palTiming = true
	sys.CPU.Cycles = 262 * 1364
	if got := io.Read(0x4212); got&0x80 == 0 {
		t.Fatalf("PAL HVBJOY at NTSC frame boundary = %02X, want vblank bit set", got)
	}

	sys.palTiming = false
	sys.CPU.Cycles = 262 * 1364
	if got := io.Read(0x4212); got&0x80 != 0 {
		t.Fatalf("NTSC HVBJOY at next frame boundary = %02X, want vblank bit clear", got)
	}
}

func TestIODeviceRDNMISyncsPPUBeforeRead(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}

	sys.Bus.MDR = 0x70
	sys.CPU.Cycles = 225 * 1364
	if got := io.Read(0x4210); got != 0xF2 {
		t.Fatalf("RDNMI at synced vblank entry = %02X, want open-bus bits 4-6 plus bit 7 and version 2", got)
	}
	if !sys.PPU.NMIFlag {
		t.Fatalf("NMIFlag cleared during NMI hold")
	}

	sys.CPU.Cycles += 4
	sys.Bus.MDR = 0x70
	if got := io.Read(0x4210); got != 0xF2 {
		t.Fatalf("RDNMI after NMI hold = %02X, want bit 7 set before clear", got)
	}
	if sys.PPU.NMIFlag {
		t.Fatalf("NMIFlag still set after post-hold RDNMI read")
	}
}

func TestIODeviceNMIReenableUsesLatchedRDNMI(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}

	sys.CPU.Cycles = 225*1364 + 4
	sys.PPU.NMIFlag = true
	io.Write(0x4200, 0x80)
	if !sys.CPU.NMIPending {
		t.Fatalf("NMI re-enable with latched RDNMI did not trigger NMI")
	}

	sys.CPU.NMIPending = false
	sys.Bus.MDR = 0
	if got := io.Read(0x4210); got&0x80 == 0 {
		t.Fatalf("RDNMI read = %02X, want latched NMI bit", got)
	}

	io.Write(0x4200, 0x00)
	io.Write(0x4200, 0x80)
	if sys.CPU.NMIPending {
		t.Fatalf("NMI re-enable after RDNMI clear triggered during live VBlank")
	}
}

func TestIODeviceHVBJOYUsesCPUCounter(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}

	sys.Bus.MDR = 0x3E
	sys.CPU.Cycles = 12*1364 + 275*4
	if got := io.Read(0x4212); got != 0x7E {
		t.Fatalf("HVBJOY at synced hblank entry = %02X, want open-bus bits 1-5 plus bit 6", got)
	}
}

func TestIODeviceHVBJOYDoesNotUsePPUCounter(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}

	sys.Bus.MDR = 0x3E
	sys.CPU.Cycles = 54
	for got := sys.PPU.ReadHVBJOY(); got&0x80 == 0; got = sys.PPU.ReadHVBJOY() {
		sys.PPU.Run()
		if sys.PPU.FrameCount > 0 {
			t.Fatalf("overran frame before PPU vblank")
		}
	}
	if got := io.Read(0x4212); got != 0x3E {
		t.Fatalf("HVBJOY after PPU reaches vblank = %02X, want CPU-counter status with open-bus bits", got)
	}
}

func TestMDMAENDefersTransferToNextOpcodeBoundary(t *testing.T) {
	sys := NewSystem(nil)

	rom := bus.NewRAMDevice(0x100)
	rom.Write(0x8000, 0xEA) // NOP
	sys.Bus.Map(0x008000, 0x0080FF, rom)
	sys.CPU.PB = 0x00
	sys.CPU.PC = 0x8000
	sys.CPU.P = 0x34

	sys.wram.Write(0x7E0000, 0x8F)
	ch := &sys.DMA.Channels[0]
	ch.Control = 0x00
	ch.Target = 0x00
	ch.SrcBank = 0x7E
	ch.SrcAddr = 0x0000
	ch.Size = 1

	sys.io.Write(0x420B, 0x01)
	if !sys.DMA.SaveState().Execution.Pending || sys.DMA.Enable != 0x01 {
		t.Fatalf("pending DMA = %02X, want 01", sys.DMA.Enable)
	}
	if got := sys.PPU.INIDISP; got != 0x00 {
		t.Fatalf("INIDISP after MDMAEN write = %02X, want deferred transfer", got)
	}

	sys.CPU.Run()
	if sys.DMA.SaveState().Execution.Pending {
		t.Fatalf("pending DMA after next opcode = %02X, want clear", sys.DMA.Enable)
	}
	if got := sys.PPU.INIDISP; got != 0x8F {
		t.Fatalf("INIDISP after deferred DMA = %02X, want 8F", got)
	}
	if got := sys.CPU.PC; got != 0x8001 {
		t.Fatalf("PC after NOP = %04X, want 8001", got)
	}
}

func TestCPUReadLatchesBeamBeforeFinalReadClocks(t *testing.T) {
	sys := NewSystem(nil)
	sys.wrio = 0x80
	sys.CPU.PC = 0
	sys.CPU.PB = 0
	sys.CPU.P = 0x24
	sys.CPU.E = false
	sys.CPU.Cycles = 9
	sys.Bus.Write(0x000000, 0xAD) // LDA $2137
	sys.Bus.Write(0x000001, 0x37)
	sys.Bus.Write(0x000002, 0x21)

	sys.CPU.Step()

	if got := sys.PPU.ReadRegister(0x213C); got != 0x08 {
		t.Fatalf("latched H = %02X, want 08", got)
	}
}

func TestIODeviceSLHVLatchesFloorDotAtReadTimestamp(t *testing.T) {
	sys := NewSystem(nil)
	sys.Power()
	sys.wrio = 0x80
	sys.CPU.Cycles = 1070836

	sys.io.Read(0x2137)
	if got := sys.io.Read(0x213C); got != 0x19 {
		t.Fatalf("first OPHCT low = %02X, want 19", got)
	}

	sys = NewSystem(nil)
	sys.Power()
	sys.wrio = 0x80
	sys.CPU.Cycles = 1071066
	sys.io.Read(0x2137)
	if got := sys.io.Read(0x213C); got != 0x52 {
		t.Fatalf("second OPHCT low = %02X, want 52", got)
	}
}

func TestIODevicemultiplyResultDelay(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}

	sys.CPU.MultiplicationResult = 0x1234
	io.Write(0x4202, 0xFF)
	io.Write(0x4203, 0x01)

	if got := io.Read(0x4216); got != 0x00 {
		t.Fatalf("early MPYL = %02X, want cleared 00", got)
	}
	if got := io.Read(0x4217); got != 0x00 {
		t.Fatalf("early MPYH = %02X, want cleared 00", got)
	}
}

func TestCPUInternalMultiplyProgression(t *testing.T) {
	sys := NewSystem(nil)
	sys.CPU.MultiplicandA = 0xFF
	sys.CPU.StartMultiply(0x01)

	program := []uint8{
		0xAD, 0x16, 0x42, // LDA $4216
		0xAD, 0x16, 0x42, // LDA $4216
		0xAD, 0x16, 0x42, // LDA $4216
	}
	for i, v := range program {
		sys.Bus.Write(uint32(i), v)
	}

	sys.CPU.Step()
	if got := uint8(sys.CPU.A); got != 0x07 {
		t.Fatalf("first MPYL = %02X, want 07", got)
	}
	sys.CPU.Step()
	if got := uint8(sys.CPU.A); got != 0x7F {
		t.Fatalf("second MPYL = %02X, want 7F", got)
	}
	sys.CPU.Step()
	if got := uint8(sys.CPU.A); got != 0xFF {
		t.Fatalf("final MPYL = %02X, want FF", got)
	}
	if sys.CPU.MultiplyCounter != 0 {
		t.Fatalf("multiply counter = %d, want 0", sys.CPU.MultiplyCounter)
	}
}

func TestCPUMultiplyUsesRDDIVShiftRegister(t *testing.T) {
	sys := NewSystem(nil)
	sys.CPU.MultiplicandA = 0xFF
	sys.CPU.StartMultiply(0x01)

	if got := sys.CPU.Quotient; got != 0x01FF {
		t.Fatalf("rddiv start = %04X, want 01FF", got)
	}
	sys.CPU.AddCycles(12)
	if got := sys.CPU.MultiplicationResult; got != 0x0003 {
		t.Fatalf("rdmpy after two idles = %04X, want 0003", got)
	}
	if got := sys.CPU.Quotient; got != 0x007F {
		t.Fatalf("rddiv after two idles = %04X, want 007F", got)
	}
	if got := sys.CPU.MultiplyDividend; got != sys.CPU.Quotient {
		t.Fatalf("trace rddiv mirror = %04X, want %04X", got, sys.CPU.Quotient)
	}
}

func TestCPUDivideUpdatesTraceRDDIV(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}

	io.Write(0x4204, 0x34)
	io.Write(0x4205, 0x12)
	io.Write(0x4206, 0x00)

	if got := sys.CPU.Quotient; got != 0xFFFF {
		t.Fatalf("rddiv divide by zero = %04X, want FFFF", got)
	}
	if got := sys.CPU.MultiplyDividend; got != sys.CPU.Quotient {
		t.Fatalf("trace rddiv mirror = %04X, want %04X", got, sys.CPU.Quotient)
	}
}

func TestCPUMathStateRoundTrip(t *testing.T) {
	sys := NewSystem(nil)
	sys.CPU.MultiplicandA = 0xFF
	sys.CPU.StartMultiply(0x01)

	program := []uint8{0xEA}
	for i, v := range program {
		sys.Bus.Write(uint32(i), v)
	}
	sys.CPU.Step()

	state := sys.CPU.SaveState()
	restored := NewSystem(nil)
	restored.CPU.LoadState(state)

	if restored.CPU.MultiplicationResult != sys.CPU.MultiplicationResult {
		t.Fatalf("rdmpy = %04X, want %04X", restored.CPU.MultiplicationResult, sys.CPU.MultiplicationResult)
	}
	if restored.CPU.MultiplyCounter != sys.CPU.MultiplyCounter {
		t.Fatalf("multiply counter = %d, want %d", restored.CPU.MultiplyCounter, sys.CPU.MultiplyCounter)
	}
	if restored.CPU.MultiplyDividend != sys.CPU.MultiplyDividend {
		t.Fatalf("multiply dividend = %04X, want %04X", restored.CPU.MultiplyDividend, sys.CPU.MultiplyDividend)
	}
	if restored.CPU.MultiplyShift != sys.CPU.MultiplyShift {
		t.Fatalf("multiply shift = %04X, want %04X", restored.CPU.MultiplyShift, sys.CPU.MultiplyShift)
	}
}

func TestCPUMultiplyAdvancesOnInternalIdle(t *testing.T) {
	sys := NewSystem(nil)
	sys.CPU.MultiplicandA = 0xFF
	sys.CPU.StartMultiply(0x01)

	sys.CPU.AddCycles(12)

	if got := sys.CPU.MultiplicationResult; got != 0x0003 {
		t.Fatalf("rdmpy after two idles = %04X, want 0003", got)
	}
	if got := sys.CPU.MultiplyCounter; got != 6 {
		t.Fatalf("multiply counter = %d, want 6", got)
	}
}

func TestCPUInternalIOReadDoesNotUpdateMDR(t *testing.T) {
	sys := NewSystem(nil)
	sys.CPU.PC = 0
	sys.CPU.PB = 0
	sys.CPU.P = 0x24
	sys.CPU.E = false
	sys.CPU.MultiplicationResult = 0x0077
	sys.Bus.Write(0x000000, 0xAD) // LDA $4216
	sys.Bus.Write(0x000001, 0x16)
	sys.Bus.Write(0x000002, 0x42)

	sys.CPU.Step()

	if got := uint8(sys.CPU.A); got != 0x77 {
		t.Fatalf("A = %02X, want 77", got)
	}
	if got := sys.Bus.MDR; got != 0x42 {
		t.Fatalf("MDR = %02X, want operand high 42", got)
	}
}

func TestCPUDRAMRefreshAdvancesMathALU(t *testing.T) {
	sys := NewSystem(nil)
	sys.CPU.Cycles = 537
	sys.CPU.MultiplicandA = 0xFF
	sys.CPU.StartMultiply(0x01)

	sys.CPU.AddCycles(1)

	if got := sys.CPU.Cycles; got != 578 {
		t.Fatalf("cycles = %d, want 578", got)
	}
	if got := sys.CPU.MultiplicationResult; got != 0x001F {
		t.Fatalf("rdmpy after refresh = %04X, want 001F", got)
	}
	if got := sys.CPU.MultiplyCounter; got != 3 {
		t.Fatalf("multiply counter = %d, want 3", got)
	}
}

func TestCPUDRAMRefreshUsesLineStartDMAPhase(t *testing.T) {
	sys := NewSystem(nil)
	const scanlineCycles = 1364
	lineStart := uint64(429 * scanlineCycles)
	sys.CPU.Cycles = lineStart + 533

	sys.CPU.AddCycles(1)

	if got, want := sys.CPU.Cycles-lineStart, uint64(574); got != want {
		t.Fatalf("line cycle after refresh = %d, want %d", got, want)
	}
}

func TestGSUCartridgeDoesNotOverrideSystemWindows(t *testing.T) {
	sys := NewSystem(nil)
	rom := make([]byte, 0x8000)
	for i := range rom {
		rom[i] = 0xAA
	}
	rom[0x7FD5] = 0x20
	rom[0x7FD6] = 0x13
	rom[0x7FD8] = 0
	rom[0x7FFC] = 0x00
	rom[0x7FFD] = 0x80
	if err := sys.LoadROM(rom); err != nil {
		t.Fatalf("LoadROM: %v", err)
	}

	sys.Bus.Write(0x001FFF, 0x5A)
	if got := sys.Bus.Read(0x001FFF); got != 0x5A {
		t.Fatalf("WRAM mirror read = %02X, want 5A", got)
	}

	sys.Bus.MDR = 0x70
	sys.CPU.Cycles = 225 * 1364
	if got := sys.Bus.Read(0x004210); got != 0xF2 {
		t.Fatalf("RDNMI through GSU cartridge map = %02X, want F2", got)
	}
}

func TestSystemWRAMPowerOnZeroed(t *testing.T) {
	sys := NewSystem(nil)
	for _, address := range []uint32{0x000004, 0x7E0004, 0x7F0004} {
		if got := sys.Bus.Read(address); got != 0 {
			t.Fatalf("read %06X = %02X, want 00", address, got)
		}
	}
}

func TestWRAMDataPortWriteAndRead(t *testing.T) {
	sys := NewSystem(nil)
	sys.Bus.Write(0x002181, 0x34)
	sys.Bus.Write(0x002182, 0x20)
	sys.Bus.Write(0x002183, 0x01)

	sys.Bus.Write(0x002180, 0x4c)
	sys.Bus.Write(0x002180, 0x49)

	if got := sys.Bus.Read(0x7f2034); got != 0x4c {
		t.Fatalf("WRAM[12034] = %02X, want 4C", got)
	}
	if got := sys.Bus.Read(0x7f2035); got != 0x49 {
		t.Fatalf("WRAM[12035] = %02X, want 49", got)
	}

	sys.Bus.Write(0x002181, 0x34)
	sys.Bus.Write(0x002182, 0x20)
	sys.Bus.Write(0x002183, 0x01)
	if got := sys.Bus.Read(0x002180); got != 0x4c {
		t.Fatalf("$2180 first read = %02X, want 4C", got)
	}
	if got := sys.Bus.Read(0x002180); got != 0x49 {
		t.Fatalf("$2180 second read = %02X, want 49", got)
	}
}

func TestWRAMDataPortAddressWraps17Bit(t *testing.T) {
	sys := NewSystem(nil)
	sys.Bus.Write(0x002181, 0xff)
	sys.Bus.Write(0x002182, 0xff)
	sys.Bus.Write(0x002183, 0xff)

	sys.Bus.Write(0x002180, 0xaa)
	sys.Bus.Write(0x002180, 0xbb)

	if got := sys.Bus.Read(0x7fffff); got != 0xaa {
		t.Fatalf("WRAM[1FFFF] = %02X, want AA", got)
	}
	if got := sys.Bus.Read(0x7e0000); got != 0xbb {
		t.Fatalf("WRAM[00000] = %02X, want BB", got)
	}
}

func TestWRAMDataPortHighAddressUsesOnlyBit0(t *testing.T) {
	sys := NewSystem(nil)
	sys.Bus.Write(0x002181, 0x00)
	sys.Bus.Write(0x002182, 0x00)
	sys.Bus.Write(0x002183, 0xfe)
	sys.Bus.Write(0x002180, 0x11)

	sys.Bus.Write(0x002181, 0x00)
	sys.Bus.Write(0x002182, 0x00)
	sys.Bus.Write(0x002183, 0xff)
	sys.Bus.Write(0x002180, 0x22)

	if got := sys.Bus.Read(0x7e0000); got != 0x11 {
		t.Fatalf("WRAM[00000] = %02X, want 11", got)
	}
	if got := sys.Bus.Read(0x7f0000); got != 0x22 {
		t.Fatalf("WRAM[10000] = %02X, want 22", got)
	}
}

func TestWRAMDataPortStateRoundTrip(t *testing.T) {
	sys := NewSystem(nil)
	sys.Bus.Write(0x002181, 0x10)
	sys.Bus.Write(0x002182, 0x00)
	sys.Bus.Write(0x002183, 0x01)
	sys.Bus.Write(0x002180, 0xab)

	state, err := sys.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	restored := NewSystem(nil)
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	restored.Bus.Write(0x002180, 0xcd)

	if got := restored.Bus.Read(0x7f0010); got != 0xab {
		t.Fatalf("restored WRAM[10010] = %02X, want AB", got)
	}
	if got := restored.Bus.Read(0x7f0011); got != 0xcd {
		t.Fatalf("restored WRAM[10011] = %02X, want CD", got)
	}
}

func TestSystemLoadROMSaveRAMAndState(t *testing.T) {
	sys := NewSystem(nil)
	rom := make([]byte, 0x8000)
	rom[0x7ffc] = 0x00
	rom[0x7ffd] = 0x80

	if err := sys.LoadROM(rom); err != nil {
		t.Fatalf("LoadROM: %v", err)
	}
	sys.Power()

	if !sys.Loaded() {
		t.Fatal("Loaded = false, want true")
	}

	if err := sys.LoadSaveRAM([]byte{0x11, 0x22, 0x33}); err != nil {
		t.Fatalf("LoadSaveRAM: %v", err)
	}
	if err := sys.SetInputState(0, emulator.StandardButtonA|emulator.StandardButtonStart); err != nil {
		t.Fatalf("SetInputState: %v", err)
	}

	sys.Bus.Write(0x7e0010, 0xab)
	sys.CPU.A = 0x1234
	sys.CPU.PC = 0x4567
	sys.PPU.INIDISP = 0x8f
	sys.PPU.FrontBuffer[0] = 0x1111
	sys.DMA.HDMAEnable = 0x80
	sys.APU.OutPorts[0] = 0x5a

	state, err := sys.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	sys.Bus.Write(0x7e0010, 0)
	sys.CPU.A = 0
	sys.CPU.PC = 0
	sys.PPU.INIDISP = 0
	sys.PPU.FrontBuffer[0] = 0
	sys.DMA.HDMAEnable = 0
	sys.APU.OutPorts[0] = 0
	sys.Controller1.SetState(0)
	if err := sys.LoadSaveRAM(nil); err != nil {
		t.Fatalf("clear SaveRAM: %v", err)
	}

	if err := sys.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}

	if got := sys.Bus.Read(0x7e0010); got != 0xab {
		t.Fatalf("WRAM = %02X, want AB", got)
	}
	if sys.CPU.A != 0x1234 || sys.CPU.PC != 0x4567 {
		t.Fatalf("CPU restored = A:%04X PC:%04X, want A:1234 PC:4567", sys.CPU.A, sys.CPU.PC)
	}
	if sys.PPU.INIDISP != 0x8f || sys.FrameBuffer()[0] != 0x1111 {
		t.Fatalf("PPU restored = INIDISP:%02X Pixel:%04X", sys.PPU.INIDISP, sys.FrameBuffer()[0])
	}
	if sys.DMA.HDMAEnable != 0x80 {
		t.Fatalf("HDMAEnable = %02X, want 80", sys.DMA.HDMAEnable)
	}
	if sys.APU.OutPorts[0] != 0x5a {
		t.Fatalf("APU OutPorts[0] = %02X, want 5A", sys.APU.OutPorts[0])
	}
	if got := sys.SaveRAM(); len(got) < 3 || got[0] != 0x11 || got[1] != 0x22 || got[2] != 0x33 {
		t.Fatalf("SaveRAM = %v, want prefix [17 34 51]", got[:3])
	}
	if got := sys.Controller1.Poll(); got != emulator.StandardButtonA|emulator.StandardButtonStart {
		t.Fatalf("controller state = %04X, want %04X", got, emulator.StandardButtonA|emulator.StandardButtonStart)
	}
}

func TestSystemStateHashes(t *testing.T) {
	sys := NewSystem(nil)
	rom := newBootableTestROM()
	if err := sys.LoadROM(rom); err != nil {
		t.Fatalf("LoadROM: %v", err)
	}
	sys.Power()

	before, err := sys.StateHashes()
	if err != nil {
		t.Fatalf("StateHashes: %v", err)
	}
	for _, name := range []string{"cpu", "wram", "vram", "cgram", "oam", "ppu", "dma", "apu", "scheduler", "framebuffer"} {
		if before[name] == "" {
			t.Fatalf("StateHashes missing %q: %#v", name, before)
		}
	}

	sys.Bus.Write(0x7e0022, 0x5a)
	after, err := sys.StateHashes()
	if err != nil {
		t.Fatalf("StateHashes after write: %v", err)
	}
	if after["wram"] == before["wram"] {
		t.Fatalf("WRAM hash did not change after WRAM write")
	}
}

func TestUnserializeWithOptionsIgnoresROMHash(t *testing.T) {
	rom := make([]byte, 0x8000)
	rom[0x7ffc] = 0x00
	rom[0x7ffd] = 0x80
	base := NewSystem(nil)
	if err := base.LoadROM(rom); err != nil {
		t.Fatalf("base LoadROM: %v", err)
	}
	base.Power()
	state, err := base.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	otherROM := append([]byte(nil), rom...)
	otherROM[0x100] ^= 0xff
	restored := NewSystem(nil)
	if err := restored.LoadROM(otherROM); err != nil {
		t.Fatalf("restored LoadROM: %v", err)
	}
	if err := restored.Unserialize(state); err == nil {
		t.Fatal("Unserialize with mismatched ROM succeeded, want error")
	}
	if err := restored.UnserializeWithOptions(state, UnserializeOptions{IgnoreROMHash: true}); err != nil {
		t.Fatalf("UnserializeWithOptions: %v", err)
	}
}

func TestSystemReadWRAMAt(t *testing.T) {
	sys := NewSystem(nil)

	sys.Bus.Write(0x7E1234, 0xAB)
	sys.Bus.Write(0x7F0001, 0xCD)
	sys.Bus.MDR = 0x5A

	buf := make([]byte, 2)
	n, err := sys.ReadWRAMAt(buf, 0x1234)
	if err != nil {
		t.Fatalf("ReadWRAMAt: %v", err)
	}
	if n != len(buf) {
		t.Fatalf("ReadWRAMAt n = %d, want %d", n, len(buf))
	}
	if buf[0] != 0xAB || buf[1] != 0x00 {
		t.Fatalf("ReadWRAMAt = % X, want AB 00", buf)
	}
	if sys.Bus.MDR != 0x5A {
		t.Fatalf("Bus.MDR = %02X, want 5A", sys.Bus.MDR)
	}

	buf = []byte{0}
	n, err = sys.ReadWRAMAt(buf, 0x10001)
	if err != nil {
		t.Fatalf("ReadWRAMAt high bank: %v", err)
	}
	if n != 1 || buf[0] != 0xCD {
		t.Fatalf("ReadWRAMAt high bank n=%d buf=% X, want 1 CD", n, buf)
	}

	buf = make([]byte, 2)
	n, err = sys.ReadWRAMAt(buf, 128*1024-1)
	if n != 1 || !errors.Is(err, io.EOF) {
		t.Fatalf("ReadWRAMAt boundary n=%d err=%v, want 1 io.EOF", n, err)
	}
	if _, err := sys.ReadWRAMAt(buf, -1); err == nil {
		t.Fatal("ReadWRAMAt negative offset succeeded")
	}
}

func TestSystemPortsAndConnect(t *testing.T) {
	sys := NewSystem(nil)

	if got := sys.Connected(0); got != 1 {
		t.Fatalf("Connected(0) = %d, want 1", got)
	}
	if err := sys.Connect(0, 0); err != nil {
		t.Fatalf("Connect none: %v", err)
	}
	if err := sys.SetInputState(0, emulator.StandardButtonB); err == nil {
		t.Fatal("SetInputState without controller succeeded, want error")
	}
	if err := sys.Connect(0, 1); err != nil {
		t.Fatalf("Connect controller: %v", err)
	}
	if err := sys.SetInputState(0, emulator.StandardButtonB); err != nil {
		t.Fatalf("SetInputState with controller: %v", err)
	}
	if got := sys.Controller1.Poll(); got != emulator.StandardButtonB {
		t.Fatalf("controller state = %04X, want %04X", got, emulator.StandardButtonB)
	}
	if got := len(sys.Inputs(0, 1)); got != 12 {
		t.Fatalf("Inputs length = %d, want 12", got)
	}
	if err := sys.Connect(1, 1); err != nil {
		t.Fatalf("Connect port2 controller: %v", err)
	}
	if err := sys.SetInputState(1, emulator.StandardButtonX); err != nil {
		t.Fatalf("SetInputState port2: %v", err)
	}
	if got := sys.Controller2.Poll(); got != emulator.StandardButtonX {
		t.Fatalf("controller2 state = %04X, want %04X", got, emulator.StandardButtonX)
	}
	if err := sys.Connect(0, 2); err != nil {
		t.Fatalf("Connect mouse: %v", err)
	}
	if got := len(sys.Inputs(0, 2)); got != 4 {
		t.Fatalf("mouse Inputs length = %d, want 4", got)
	}
	if err := sys.SetInputState(0, emulator.StandardButtonB); err == nil {
		t.Fatal("SetInputState with mouse succeeded, want error")
	}
}

func TestIODeviceMouseSerialRead(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}
	if err := sys.Connect(0, 2); err != nil {
		t.Fatalf("connect mouse: %v", err)
	}
	sys.Mouse1.Sensitivity = input.MouseSensitivityMid
	if err := sys.SetMouseState(0, true, false, -5, 7); err != nil {
		t.Fatalf("SetMouseState: %v", err)
	}

	io.Write(0x4016, 1)
	var got uint32
	for i := 0; i < 32; i++ {
		got = got<<1 | uint32(io.Read(0x4016)&1)
	}
	want := uint32(0x1)<<28 |
		uint32(input.MouseSensitivityMid)<<24 |
		1<<22 |
		7<<8 |
		1<<7 |
		5
	if got != want {
		t.Fatalf("mouse report = %08X, want %08X", got, want)
	}
	if got := io.Read(0x4016) & 1; got != 1 {
		t.Fatalf("mouse idle bit = %d, want 1", got)
	}
}

func TestIODeviceMouseSerialReadPort2(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}
	if err := sys.Connect(1, 2); err != nil {
		t.Fatalf("connect port2 mouse: %v", err)
	}
	if err := sys.SetMouseState(1, false, true, 0, 0); err != nil {
		t.Fatalf("SetMouseState port2: %v", err)
	}

	io.Write(0x4016, 1)
	var got uint32
	for i := 0; i < 32; i++ {
		b := io.Read(0x4017)
		if b&0x1c != 0x1c {
			t.Fatalf("port2 mouse read %d high bits = %02X, want xx1C", i, b)
		}
		got = got<<1 | uint32(b&1)
	}
	want := uint32(0x1)<<28 | 1<<23
	if got != want {
		t.Fatalf("port2 mouse report = %08X, want %08X", got, want)
	}
}

func TestSetMouseStateRequiresMouse(t *testing.T) {
	sys := NewSystem(nil)
	if err := sys.SetMouseState(0, true, false, 1, 2); err == nil {
		t.Fatal("SetMouseState without mouse succeeded, want error")
	}
	if err := sys.Connect(0, 2); err != nil {
		t.Fatalf("connect mouse: %v", err)
	}
	if err := sys.SetMouseState(2, true, false, 1, 2); err == nil {
		t.Fatal("SetMouseState unsupported port succeeded, want error")
	}
	if err := sys.SetMouseState(0, true, true, 1, 2); err != nil {
		t.Fatalf("SetMouseState with mouse: %v", err)
	}
}

func TestSystemStateRestoresMouseConnection(t *testing.T) {
	sys := NewSystem(nil)
	if err := sys.Connect(0, 2); err != nil {
		t.Fatalf("connect mouse: %v", err)
	}
	if err := sys.SetMouseState(0, false, false, 9, 0); err != nil {
		t.Fatalf("SetMouseState: %v", err)
	}
	state, err := sys.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	if err := sys.Connect(0, 1); err != nil {
		t.Fatalf("connect controller: %v", err)
	}
	if err := sys.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if got := sys.Connected(0); got != 2 {
		t.Fatalf("Connected(0) = %d, want mouse", got)
	}

	io := &IODevice{sys: sys}
	io.Write(0x4016, 1)
	var got uint32
	for i := 0; i < 32; i++ {
		got = got<<1 | uint32(io.Read(0x4016)&1)
	}
	want := uint32(0x1)<<28 | 9
	if got != want {
		t.Fatalf("restored mouse report = %08X, want %08X", got, want)
	}
}

func TestSystemDevicesIncludesSuperScope(t *testing.T) {
	sys := NewSystem(nil)
	for port := uint(0); port <= 1; port++ {
		devs := sys.Devices(port)
		var found bool
		for _, d := range devs {
			if d.ID == 3 && d.Name == "Super Scope" {
				found = true
			}
		}
		if !found {
			t.Fatalf("port %d Devices missing Super Scope: %+v", port, devs)
		}
		ins := sys.Inputs(port, 3)
		if len(ins) != 4 {
			t.Fatalf("port %d Super Scope Inputs len=%d, want 4 (trigger/cursor/turbo/pause): %+v", port, len(ins), ins)
		}
	}
}

func TestSystemConnectSuperScopeSerialRead(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}
	if err := sys.Connect(0, 3); err != nil {
		t.Fatalf("connect super scope: %v", err)
	}
	if got := sys.Connected(0); got != 3 {
		t.Fatalf("Connected(0) = %d, want 3", got)
	}
	if err := sys.SetSuperScopeState(0, true, false, false, true); err != nil {
		t.Fatalf("SetSuperScopeState: %v", err)
	}

	// Pull the latch line; the Super Scope shifts a 32-bit report MSB-first
	// over $4016 reads. Bits 31..28 are trigger/cursor/turbo/pause.
	io.Write(0x4016, 1)
	var got uint32
	for i := 0; i < 32; i++ {
		got = got<<1 | uint32(io.Read(0x4016)&1)
	}
	const trigger = uint32(1) << 31
	const pause = uint32(1) << 28
	if got&trigger == 0 {
		t.Fatalf("super scope serial report = %08X, missing trigger bit", got)
	}
	if got&pause == 0 {
		t.Fatalf("super scope serial report = %08X, missing pause bit", got)
	}
	// Cursor and turbo are unset.
	if got&(uint32(1)<<30) != 0 {
		t.Fatalf("super scope serial report = %08X, unexpected cursor bit", got)
	}
	if got&(uint32(1)<<29) != 0 {
		t.Fatalf("super scope serial report = %08X, unexpected turbo bit", got)
	}
	// After the 32-bit report the device idles high.
	if got := io.Read(0x4016) & 1; got != 1 {
		t.Fatalf("super scope idle bit = %d, want 1", got)
	}
}

func TestSystemConnectSuperScopePort2(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}
	if err := sys.Connect(1, 3); err != nil {
		t.Fatalf("connect port2 super scope: %v", err)
	}
	if err := sys.SetSuperScopeState(1, false, true, false, false); err != nil {
		t.Fatalf("SetSuperScopeState port2: %v", err)
	}

	io.Write(0x4016, 1)
	for i := 0; i < 32; i++ {
		b := io.Read(0x4017)
		// Port 2 reads return the high bits 0x1c | bit(0).
		if b&0x1c != 0x1c {
			t.Fatalf("port2 super scope read %d high bits = %02X, want xx1C", i, b)
		}
	}
}

func TestSetSuperScopeStateRequiresSuperScope(t *testing.T) {
	sys := NewSystem(nil)
	if err := sys.SetSuperScopeState(0, true, false, false, false); err == nil {
		t.Fatal("SetSuperScopeState without super scope succeeded, want error")
	}
	if err := sys.Connect(0, 3); err != nil {
		t.Fatalf("connect super scope: %v", err)
	}
	if err := sys.SetSuperScopeState(2, true, false, false, false); err == nil {
		t.Fatal("SetSuperScopeState with unsupported port succeeded, want error")
	}
	if err := sys.SetSuperScopeState(0, true, true, true, true); err != nil {
		t.Fatalf("SetSuperScopeState with super scope: %v", err)
	}
}

func TestSystemStateRestoresSuperScopeConnection(t *testing.T) {
	sys := NewSystem(nil)
	if err := sys.Connect(0, 3); err != nil {
		t.Fatalf("connect super scope: %v", err)
	}
	if err := sys.SetSuperScopeState(0, true, false, true, false); err != nil {
		t.Fatalf("SetSuperScopeState: %v", err)
	}
	state, err := sys.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	if err := sys.Connect(0, 1); err != nil {
		t.Fatalf("connect controller: %v", err)
	}
	if err := sys.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if got := sys.Connected(0); got != 3 {
		t.Fatalf("Connected(0) = %d, want super scope (3)", got)
	}

	io := &IODevice{sys: sys}
	io.Write(0x4016, 1)
	var got uint32
	for i := 0; i < 32; i++ {
		got = got<<1 | uint32(io.Read(0x4016)&1)
	}
	if got&(uint32(1)<<31) == 0 {
		t.Fatalf("restored super scope report = %08X, missing trigger bit", got)
	}
	if got&(uint32(1)<<29) == 0 {
		t.Fatalf("restored super scope report = %08X, missing turbo bit", got)
	}
}

// TestSystemDMAtoVRAMAppliesVMAINTranslation pins the integration chain
// where GP-DMA targeting $2118/$2119 with a non-zero $2115 VMAIN
// translation/remap mode applies the PPU's vramWordAddr remap on
// every byte. The chain crosses three packages:
//
//	dma.Execute -> Bus.Write -> IODevice.Write -> PPU.WriteRegister
//	                                              -> case 0x18/0x19
//	                                              -> vramWordAddr remap
//	                                              -> VRAM[mappedIdx*2]
//
// Existing coverage exercises the two halves separately:
//   - internal/ppu/registers_test.go::TestVRAMAddressTranslation pins
//     the WriteRegister side via direct register writes.
//   - internal/dma/dma_test.go Mode 5/6/7 pattern tests pin the
//     dma.Execute side via a testBus stub that doesn't reach PPU.
//
// Neither test catches a regression that introduces DMA-side address
// caching, IODevice rewiring of $2118/$2119 away from
// PPU.WriteRegister, or VMAIN-handling drift specific to the DMA
// path. This tripwire walks the full chain end-to-end.
//
// Test parametrizes the four documented remap modes (none / 2bpp /
// 4bpp / 8bpp); the byte-to-word index for each mode is taken from
// the existing TestVRAMAddressTranslation goldens to share the same
// authoritative addresses.
func TestSystemDMAtoVRAMAppliesVMAINTranslation(t *testing.T) {
	cases := []struct {
		name       string
		mode       uint8 // bits 2-3 of VMAIN
		inAddr     uint16
		wantMapped uint16
	}{
		{"none", 0, 0x1234, 0x1234},
		{"2bpp_low_byte_0x21", 1, 0x0021, 0x0009},
		{"4bpp_0x0041", 2, 0x0041, 0x0009},
		{"8bpp_0x0081", 3, 0x0081, 0x0009},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			sys := NewSystem(nil)

			// CPU program: a single NOP at $00:8000. The NOP triggers
			// DMA ownership at the CPU bus edge and lets the deferred
			// MDMAEN transfer fire on the next opcode boundary,
			// matching the path real games take.
			rom := bus.NewRAMDevice(0x100)
			rom.Write(0x8000, 0xEA) // NOP
			sys.Bus.Map(0x008000, 0x0080FF, rom)
			sys.CPU.PB = 0x00
			sys.CPU.PC = 0x8000
			sys.CPU.P = 0x34

			// Force-blank so VRAM writes aren't dropped by the
			// active-display gate. Then set the VMAIN remap mode
			// with bit 7 = 1 so the address increments after the
			// high-byte ($2119) write, completing one full word per
			// pair of bus writes.
			sys.io.Write(0x2100, 0x80)
			vmain := uint8(0x80 | (tc.mode << 2))
			sys.io.Write(0x2115, vmain)

			// VMADDR = inAddr.
			sys.io.Write(0x2116, uint8(tc.inAddr&0xFF))
			sys.io.Write(0x2117, uint8(tc.inAddr>>8))

			// Place the source word in WRAM at $7E:0000-1.
			sys.wram.Write(0x7E0000, 0xAA)
			sys.wram.Write(0x7E0001, 0xBB)

			// Configure DMA channel 0:
			//   Control = 0x01: A->B direction, mode 1 = two-byte two-
			//                   register pattern ($2118 then $2119
			//                   alternating).
			//   Target = 0x18: destBase = $2118.
			//   SrcBank = $7E, SrcAddr = 0x0000.
			//   Size = 2: exactly one $2118/$2119 pair = one VRAM word.
			ch := &sys.DMA.Channels[0]
			ch.Control = 0x01
			ch.Target = 0x18
			ch.SrcBank = 0x7E
			ch.SrcAddr = 0x0000
			ch.Size = 2

			// Trigger MDMAEN ch0; deferred until next opcode boundary.
			sys.io.Write(0x420B, 0x01)
			if !sys.DMA.SaveState().Execution.Pending || sys.DMA.Enable != 0x01 {
				t.Fatalf("pending DMA = %02X, want 01", sys.DMA.Enable)
			}

			sys.CPU.Run()

			if sys.DMA.SaveState().Execution.Pending {
				t.Fatalf("pending DMA after NOP = %02X, want clear", sys.DMA.Enable)
			}

			byteIdx := int(tc.wantMapped) * 2
			if got := sys.PPU.VRAM[byteIdx]; got != 0xAA {
				t.Fatalf("VRAM[%04X] (low byte at mapped word %04X) = %02X, want AA "+
					"(mode=%d inAddr=%04X)", byteIdx, tc.wantMapped, got, tc.mode, tc.inAddr)
			}
			if got := sys.PPU.VRAM[byteIdx+1]; got != 0xBB {
				t.Fatalf("VRAM[%04X] (high byte at mapped word %04X) = %02X, want BB "+
					"(mode=%d inAddr=%04X)", byteIdx+1, tc.wantMapped, got, tc.mode, tc.inAddr)
			}
		})
	}
}

func TestSystemDevicesIncludesMultitap(t *testing.T) {
	sys := NewSystem(nil)
	for port := uint(0); port <= 1; port++ {
		devs := sys.Devices(port)
		var found bool
		for _, d := range devs {
			if d.ID == 4 && d.Name == "Multitap" {
				found = true
			}
		}
		if !found {
			t.Fatalf("port %d Devices missing Multitap: %+v", port, devs)
		}
		ins := sys.Inputs(port, 4)
		if len(ins) != 12 {
			t.Fatalf("port %d Multitap Inputs len=%d, want 12 (standard buttons): %+v", port, len(ins), ins)
		}
	}
}

func TestSystemConnectMultitapSerialReadSelectsSlot(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}
	if err := sys.Connect(0, 4); err != nil {
		t.Fatalf("connect multitap: %v", err)
	}
	if got := sys.Connected(0); got != 4 {
		t.Fatalf("Connected(0) = %d, want 4", got)
	}
	// Slot 0 carries B; slot 2 carries Start. Distinct buttons so the
	// select line is observable in the first serial bit (B is bit 0
	// of the standard report, Start is bit 3 -- so the first
	// post-latch read returns B for slot 0 and 0 for slot 2 until 3
	// bits have shifted).
	if err := sys.SetMultitapSubState(0, 0, emulator.StandardButtonB); err != nil {
		t.Fatalf("SetMultitapSubState slot 0: %v", err)
	}
	if err := sys.SetMultitapSubState(0, 2, emulator.StandardButtonStart); err != nil {
		t.Fatalf("SetMultitapSubState slot 2: %v", err)
	}

	// select=false -> slot 0 routes to readSerial. Latch + read bit 0:
	// B is pressed, the StandardController returns 1 for B as the first
	// MSB-first shift bit.
	if err := sys.SetMultitapSelect(0, false); err != nil {
		t.Fatalf("SetMultitapSelect false: %v", err)
	}
	io.Write(0x4016, 1)
	io.Write(0x4016, 0)
	if got := io.Read(0x4016) & 1; got != 1 {
		t.Fatalf("multitap select=false slot-0 first bit = %d, want 1 (B pressed)", got)
	}

	// select=true -> slot 2 routes to readSerial. New latch sequence;
	// slot 2 has Start pressed but B unpressed, so the first bit is 0.
	if err := sys.SetMultitapSelect(0, true); err != nil {
		t.Fatalf("SetMultitapSelect true: %v", err)
	}
	io.Write(0x4016, 1)
	io.Write(0x4016, 0)
	if got := io.Read(0x4016) & 1; got != 0 {
		t.Fatalf("multitap select=true slot-2 first bit = %d, want 0 (B not pressed)", got)
	}
}

func TestSetMultitapSubStateRequiresMultitap(t *testing.T) {
	sys := NewSystem(nil)
	if err := sys.SetMultitapSubState(0, 0, emulator.StandardButtonB); err == nil {
		t.Fatal("SetMultitapSubState without multitap succeeded, want error")
	}
	if err := sys.Connect(0, 4); err != nil {
		t.Fatalf("connect multitap: %v", err)
	}
	if err := sys.SetMultitapSubState(2, 0, emulator.StandardButtonB); err == nil {
		t.Fatal("SetMultitapSubState unsupported port succeeded, want error")
	}
	if err := sys.SetMultitapSubState(0, 4, emulator.StandardButtonB); err == nil {
		t.Fatal("SetMultitapSubState invalid slot succeeded, want error")
	}
	if err := sys.SetMultitapSubState(0, 3, emulator.StandardButtonStart); err != nil {
		t.Fatalf("SetMultitapSubState slot 3: %v", err)
	}
	if err := sys.SetMultitapSelect(0, true); err != nil {
		t.Fatalf("SetMultitapSelect: %v", err)
	}
}

func TestSetMultitapSelectRequiresMultitap(t *testing.T) {
	sys := NewSystem(nil)
	if err := sys.SetMultitapSelect(0, true); err == nil {
		t.Fatal("SetMultitapSelect without multitap succeeded, want error")
	}
	if err := sys.Connect(0, 4); err != nil {
		t.Fatalf("connect multitap: %v", err)
	}
	if err := sys.SetMultitapSelect(2, true); err == nil {
		t.Fatal("SetMultitapSelect unsupported port succeeded, want error")
	}
}

func TestSystemStateRestoresMultitapConnection(t *testing.T) {
	sys := NewSystem(nil)
	if err := sys.Connect(0, 4); err != nil {
		t.Fatalf("connect multitap: %v", err)
	}
	if err := sys.SetMultitapSubState(0, 1, emulator.StandardButtonY); err != nil {
		t.Fatalf("SetMultitapSubState slot 1: %v", err)
	}
	if err := sys.SetMultitapSelect(0, true); err != nil {
		t.Fatalf("SetMultitapSelect: %v", err)
	}
	state, err := sys.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	if err := sys.Connect(0, 1); err != nil {
		t.Fatalf("connect controller: %v", err)
	}
	if err := sys.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if got := sys.Connected(0); got != 4 {
		t.Fatalf("Connected(0) = %d, want multitap (4)", got)
	}
	if got := sys.Multitap1.Select(); !got {
		t.Fatalf("restored multitap select = %v, want true", got)
	}
	if got := sys.MultitapSub1[1].Poll(); got != emulator.StandardButtonY {
		t.Fatalf("restored multitap slot 1 state = %04X, want %04X", got, emulator.StandardButtonY)
	}
}

func TestIODeviceJoypadSerialReadPort2(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}
	if err := sys.Connect(1, 1); err != nil {
		t.Fatalf("connect port2: %v", err)
	}
	sys.Controller2.SetButton(input.ButtonB, true)

	io.Write(0x4016, 1)
	io.Write(0x4016, 0)
	if got := io.Read(0x4017) & 1; got != 1 {
		t.Fatalf("port2 first serial bit = %d, want 1", got)
	}
}

func TestIODeviceAPUWriteVisibleAtAccessEnd(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}

	sys.APU.Control = 0
	sys.APU.RAM[0x0200] = 0xE4 // MOV A, dp
	sys.APU.RAM[0x0201] = 0xF4
	sys.APU.Processor.PC = 0x0200

	sys.CPU.Cycles = sys.Bus.GetWaitStates(0x2140)
	io.Write(0x2140, 0x7B)

	if got := sys.APU.InPorts[0]; got != 0x7B {
		t.Fatalf("input at access end = %02X, want 7B", got)
	}
	if got := sys.APU.Processor.A; got != 0 {
		t.Fatalf("input instruction completed prematurely: A = %02X", got)
	}
	// The read occurs at SMP clock 5 and retires at clock 6. Allow enough
	// master time for both phases, independently of the CPU write itself.
	sys.CPU.Cycles = 64
	sys.Scheduler.Sync(sys.APU)
	if got := sys.APU.Processor.A; got != 0x7B {
		t.Fatalf("APU A = %02X, want 7B", got)
	}
}

func newBootableTestROM() []byte {
	rom := make([]byte, 0x8000)
	rom[0x7fd5] = 0x20
	rom[0x7ffc] = 0x00
	rom[0x7ffd] = 0x80
	for i := 0x0000; i < 0x0100; i++ {
		rom[i] = 0xea // NOP stream
	}
	return rom
}

func newGSUTestROM() []byte {
	rom := newBootableTestROM()
	rom[0x0000] = 0x4c // PLOT
	rom[0x0001] = 0x00 // STOP flushes the pixel cache
	rom[0x7fd6] = 0x13 // ROM + GSU coprocessor
	return rom
}

func TestLoadROMBindsGSUVRAMWriterToPPU(t *testing.T) {
	sys := NewSystem(nil)
	if err := sys.LoadROM(newGSUTestROM()); err != nil {
		t.Fatalf("LoadROM: %v", err)
	}
	if sys.gsu == nil {
		t.Fatal("GSU not attached")
	}

	sys.gsu.COLR = 0x7b
	sys.gsu.R[1] = 3
	sys.gsu.R[2] = 0
	sys.gsu.SetPC(0)
	sys.gsu.Go()
	// Run(3) absorbs the cold-reset $01 NOP step (gsu/device.go:213,
	// 6b200ed/ebce1d2 precedent) before retiring ROM[0]=PLOT then
	// ROM[1]=STOP, which triggers the pixel-cache flush this test
	// is checking for.
	sys.gsu.Run(3)

	if got := sys.PPU.VRAM[3]; got != 0x7b {
		t.Fatalf("PPU VRAM[3] = %02X, want 7B", got)
	}
	if len(sys.gsu.ShadowCommits()) != 0 {
		t.Fatal("GSU used shadow commits despite system PPU wiring")
	}
}

func TestRunFrameStepsGSUCoprocessor(t *testing.T) {
	sys := NewSystem(nil)
	if err := sys.LoadROM(newGSUTestROM()); err != nil {
		t.Fatalf("LoadROM: %v", err)
	}
	if sys.gsu == nil {
		t.Fatal("GSU not attached")
	}

	sys.Power()
	sys.gsu.COLR = 0x33
	sys.gsu.R[1] = 2
	sys.gsu.R[2] = 0
	sys.gsu.SetPC(0)
	sys.gsu.Go()
	if err := sys.RunFrame(); err != nil {
		t.Fatalf("RunFrame: %v", err)
	}

	if sys.gsu.Running() {
		t.Fatal("RunFrame did not advance GSU through STOP")
	}
	if got := sys.PPU.VRAM[2]; got != 0x33 {
		t.Fatalf("PPU VRAM[2] = %02X, want 33", got)
	}
	if len(sys.gsu.ShadowCommits()) != 0 {
		t.Fatal("GSU used shadow commits despite system PPU wiring")
	}
}

func TestLoadROMMapsGSURegisterWindow(t *testing.T) {
	sys := NewSystem(nil)
	if err := sys.LoadROM(newGSUTestROM()); err != nil {
		t.Fatalf("LoadROM: %v", err)
	}
	if sys.gsu == nil {
		t.Fatal("GSU not attached")
	}

	// The GSU register window is mapped only at banks $00-$3F and
	// $80-$BF per bsnes/sfc/coprocessor/superfx/io.cpp; banks $40-$7D
	// and $C0-$FF route through ROM/RAM, not the register file
	// (board.go:25-27 bank filter). Use $00/$80 banks so the writes
	// actually land in the register window. Same precedent as
	// 6b200ed's TestGSUCPURegisterWindow refresh.
	sys.Bus.Write(0x00_3000, 0xcd)
	sys.Bus.Write(0x00_3001, 0xab)
	if got := sys.gsu.R[0]; got != 0xabcd {
		t.Fatalf("GSU R0 through bus = %04X, want ABCD", got)
	}
	if got := sys.Bus.Read(0x80_3000); got != 0xcd {
		t.Fatalf("GSU R0 low mirror = %02X, want CD", got)
	}

	sys.Unload()
	if sys.gsu != nil {
		t.Fatal("Unload left GSU attached")
	}
	if err := sys.LoadROM(newBootableTestROM()); err != nil {
		t.Fatalf("LoadROM without GSU: %v", err)
	}
	// Assert the loaded non-GSU cartridge has no GSU coprocessor
	// surface attached. The original mixed-concern bus probe at
	// $40:3000 incidentally exercised both the GSU register window
	// and the non-GSU LoROM bus mapping; after the bank filter that
	// probe no longer pins what it intended. Use the direct
	// CoprocessorID + sys.gsu accessors instead.
	if sys.gsu != nil {
		t.Fatal("non-GSU LoadROM left sys.gsu attached")
	}
	if id := sys.cart.CoprocessorID; id == "gsu" {
		t.Fatalf("non-GSU LoadROM CoprocessorID = %q, want anything-but-gsu", id)
	}
}

func TestSystemStateRestoresGSUCoprocessor(t *testing.T) {
	sys := NewSystem(nil)
	if err := sys.LoadROM(newGSUTestROM()); err != nil {
		t.Fatalf("LoadROM: %v", err)
	}
	if sys.gsu == nil {
		t.Fatal("GSU not attached")
	}

	sys.gsu.R[1] = 5
	sys.gsu.R[2] = 0
	sys.gsu.R[3] = 0x3456
	sys.gsu.COLR = 0x44
	sys.gsu.RAM[0x10] = 0x99
	sys.gsu.Cache[0x10] = 0x66
	state, err := sys.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	sys.gsu.R[1] = 0
	sys.gsu.R[2] = 7
	sys.gsu.R[3] = 0
	sys.gsu.COLR = 0
	sys.gsu.RAM[0x10] = 0
	sys.gsu.Cache[0x10] = 0
	if err := sys.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}

	if sys.gsu.R[1] != 5 || sys.gsu.R[2] != 0 || sys.gsu.R[3] != 0x3456 {
		t.Fatalf("GSU registers after restore R1=%04X R2=%04X R3=%04X, want 0005/0000/3456",
			sys.gsu.R[1], sys.gsu.R[2], sys.gsu.R[3])
	}
	if sys.gsu.COLR != 0x44 || sys.gsu.RAM[0x10] != 0x99 {
		t.Fatalf("GSU state after restore COLR=%02X RAM[10]=%02X, want 44/99",
			sys.gsu.COLR, sys.gsu.RAM[0x10])
	}
	if sys.gsu.Cache[0x10] != 0x66 {
		t.Fatalf("GSU cache after restore = %02X, want 66", sys.gsu.Cache[0x10])
	}

	sys.gsu.SetPC(0)
	sys.gsu.Go()
	// Run(3) absorbs the cold-reset $01 NOP step (gsu/device.go:213,
	// 6b200ed precedent) before retiring ROM[0]=PLOT then ROM[1]=STOP,
	// which triggers the pixel-cache flush this test is checking for.
	sys.gsu.Run(3)
	if got := sys.PPU.VRAM[5]; got != 0x44 {
		t.Fatalf("PPU VRAM[5] after restored GSU plot = %02X, want 44", got)
	}
	if len(sys.gsu.ShadowCommits()) != 0 {
		t.Fatal("restored GSU used shadow commits despite system PPU wiring")
	}
}

// TestSystemStateRestoresGSUWithPendingBus pins that a GSU saved
// mid-execution with a non-cold prefetch pipeline byte round-trips
// through the System-level Serialize/Unserialize chain into a
// freshly constructed System.
//
// The existing TestSystemStateRestoresGSUCoprocessor covers the
// common register/RAM/cache restore path. This test extends it to
// the prefetch-pipeline surface:
//
//	d.Pipeline holds a non-default byte (post-b1d6766 STOP resets
//	it to 0x01, but mid-execution it's whatever the prior peekpipe
//	queued for the next dispatch).
//
// Pre-fix board.go boardState did not carry Pipeline; this would
// fail with the prefetch byte resetting to 0x01 on restore. The
// pending RAM/ROM buffer round-trip is already covered at the GSU-
// package level (TestRAMBufferSerializesPendingWrite +
// TestROMBufferSerializesPendingLoad in
// internal/cartridge/chips/gsu/core_test.go); this test ensures
// the System-level chain doesn't drop in-flight pipeline state.
func TestSystemStateRestoresGSUWithPendingBus(t *testing.T) {
	sys := NewSystem(nil)
	if err := sys.LoadROM(newGSUTestROM()); err != nil {
		t.Fatalf("LoadROM: %v", err)
	}
	if sys.gsu == nil {
		t.Fatal("GSU not attached")
	}

	// Stage a non-cold pipeline byte on the running GSU.
	sys.gsu.Pipeline = 0x4C // PLOT byte queued for next dispatch

	state, err := sys.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	// Restore into a fresh System with the same ROM bytes (newGSUTestROM
	// is deterministic).
	dst := NewSystem(nil)
	if err := dst.LoadROM(newGSUTestROM()); err != nil {
		t.Fatalf("dst LoadROM: %v", err)
	}
	if err := dst.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}

	if dst.gsu == nil {
		t.Fatal("restored System lost GSU attachment")
	}
	if dst.gsu.Pipeline != 0x4C {
		t.Errorf("restored Pipeline = %02X, want 4C "+
			"(bsnes processor/gsu/serialization.cpp serializes "+
			"regs.pipeline; without round-trip the prefetch byte "+
			"resets to 0x01 cold-NOP and the first post-restore "+
			"retire dispatches the wrong opcode)", dst.gsu.Pipeline)
	}
}

func TestLoadROMDSP1UsesUPDSPPassthrough(t *testing.T) {
	t.Setenv("SNES_DSP1_ROM", "")
	t.Setenv("SNES_DSP1_PROGRAM_ROM", "")
	t.Setenv("SNES_DSP1_DATA_ROM", "")

	rom := newBootableTestROM()
	rom[0x7fd5] = 0x23 // LoROM + DSP-family coprocessor.

	sys := NewSystem(nil)
	if err := sys.LoadROMWithOptions(rom, LoadROMOptions{DSPVariant: "DSP-1", DiagnosticPassthrough: true}); err != nil {
		t.Fatalf("LoadROM: %v", err)
	}
	if sys.cart.CoprocessorID != "updsp" {
		t.Fatalf("CoprocessorID = %q, want updsp", sys.cart.CoprocessorID)
	}

	if got := sys.Bus.Read(0x20_6001); got&0x80 == 0 {
		t.Fatalf("uPDSP passthrough SR read = %02X, RQM not set", got)
	}
	sys.Bus.Write(0x20_6000, 0x34)
	sys.Bus.Write(0x20_6000, 0x12)
	lo := sys.Bus.Read(0x20_6000)
	hi := sys.Bus.Read(0x20_6000)
	if lo != 0x34 || hi != 0x12 {
		t.Fatalf("uPDSP passthrough DR read pair = %02X/%02X, want 34/12", lo, hi)
	}
}

// TestLoadROMRejectsSPC7110Cart pins the SPC7110 fail-closed
// contract: LoadROM must return cartridge.ErrUnsupportedCoprocessor
// (wrapped with a human-readable message identifying the chip and
// example games) for any cart whose header declares SPC7110
// (chipset 0xF5 or 0xF9 with subtype 0x00). Per
// implementation_plan.md:494 + 509; bsnes heuristic at
// heuristics/super-famicom.cpp:300-301. The chip's ALU sub-unit is
// bounded but not game-observable in isolation; full execution
// (DCU + dataport + MCU + EpsonRTC) remains forward.
func TestLoadROMRejectsSPC7110Cart(t *testing.T) {
	for _, chipset := range []uint8{0xF5, 0xF9} {
		t.Run(fmt.Sprintf("chipset=%#02x", chipset), func(t *testing.T) {
			rom := newBootableTestROM()
			rom[0x7fd5] = 0x20
			rom[0x7fd6] = chipset
			rom[0x7fcf] = 0x00 // subtype 0x00 (SPC7110, not ST-018)

			sys := NewSystem(nil)
			err := sys.LoadROM(rom)
			if err == nil {
				t.Fatalf("LoadROM with SPC7110 cart succeeded, want ErrUnsupportedCoprocessor")
			}
			if !errors.Is(err, cartridge.ErrUnsupportedCoprocessor) {
				t.Errorf("LoadROM error = %v, want errors.Is(err, ErrUnsupportedCoprocessor)", err)
			}
			if !contains(err.Error(), "SPC7110") {
				t.Errorf("LoadROM error %q does not mention SPC7110", err.Error())
			}
		})
	}
}

func TestLoadROMAllowsCX4Cart(t *testing.T) {
	rom := newBootableTestROM()
	rom[0x7fd5] = 0xF3
	rom[0x7fd6] = 0x0B

	sys := NewSystem(nil)
	if err := sys.LoadROMWithOptions(rom, LoadROMOptions{Coprocessor: "cx4"}); err != nil {
		t.Fatalf("LoadROM with Cx4 cart: %v", err)
	}
	if sys.cart.CoprocessorID != "cx4" {
		t.Fatalf("CoprocessorID = %q, want cx4", sys.cart.CoprocessorID)
	}
}

// TestLoadROMRejectsST018Cart pins the ST-018 fail-closed contract:
// LoadROM must return cartridge.ErrUnsupportedCoprocessor (wrapped
// with a human-readable message) for any cart whose header declares
// the SETA ARM6 coprocessor (chipset type-hi 0xF + subtype 0x02).
// Per implementation_plan.md:495 + 507; bsnes heuristic at
// heuristics/super-famicom.cpp:101-104, 303.
func TestLoadROMRejectsST018Cart(t *testing.T) {
	rom := newBootableTestROM()
	rom[0x7fd5] = 0x20 // LoROM
	rom[0x7fd6] = 0xF3 // type-hi 0xF (ARM)
	rom[0x7fcf] = 0x02 // subtype 0x02 (ST-018)

	sys := NewSystem(nil)
	err := sys.LoadROM(rom)
	if err == nil {
		t.Fatalf("LoadROM with ST-018 cart succeeded, want ErrUnsupportedCoprocessor")
	}
	if !errors.Is(err, cartridge.ErrUnsupportedCoprocessor) {
		t.Errorf("LoadROM error = %v, want errors.Is(err, ErrUnsupportedCoprocessor)", err)
	}
	// The error message must be human-readable and identify the chip.
	msg := err.Error()
	if !contains(msg, "ST-018") {
		t.Errorf("LoadROM error %q does not mention ST-018", msg)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestLoadROMDSP1RejectsWrongSizedFirmwareEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsp1.rom")
	if err := os.WriteFile(path, []byte{1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SNES_DSP1_ROM", path)
	t.Setenv("SNES_DSP1_PROGRAM_ROM", "")
	t.Setenv("SNES_DSP1_DATA_ROM", "")

	rom := newBootableTestROM()
	rom[0x7fd5] = 0x23 // LoROM + DSP-family coprocessor.

	sys := NewSystem(nil)
	if err := sys.LoadROMWithOptions(rom, LoadROMOptions{DSPVariant: "DSP-1", DiagnosticPassthrough: true}); err == nil {
		t.Fatal("LoadROM with wrong-sized DSP-1 firmware succeeded, want error")
	}
}

func TestSystemStateRestoresUPDSPPassthrough(t *testing.T) {
	t.Setenv("SNES_DSP1_ROM", "")
	t.Setenv("SNES_DSP1_PROGRAM_ROM", "")
	t.Setenv("SNES_DSP1_DATA_ROM", "")

	rom := newBootableTestROM()
	rom[0x7fd5] = 0x23 // LoROM + DSP-family coprocessor.

	sys := NewSystem(nil)
	if err := sys.LoadROMWithOptions(rom, LoadROMOptions{DSPVariant: "DSP-1", DiagnosticPassthrough: true}); err != nil {
		t.Fatalf("LoadROM: %v", err)
	}
	sys.Bus.Write(0x20_6000, 0x78)
	state, err := sys.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	sys.Bus.Write(0x20_6000, 0x56)
	if err := sys.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if sys.cart.CoprocessorID != "updsp" {
		t.Fatalf("restored CoprocessorID = %q, want updsp", sys.cart.CoprocessorID)
	}

	sys.Bus.Write(0x20_6000, 0x12)
	lo := sys.Bus.Read(0x20_6000)
	hi := sys.Bus.Read(0x20_6000)
	if lo != 0x78 || hi != 0x12 {
		t.Fatalf("restored uPDSP DR read pair = %02X/%02X, want 78/12", lo, hi)
	}
}

func TestSystemCapabilitiesAndSettings(t *testing.T) {
	sys := NewSystem(nil)

	if !sys.Cap("frameskip") || !sys.Cap("runahead") || !sys.Cap("cheats") {
		t.Fatal("missing expected capabilities")
	}
	if sys.Cap("unknown") {
		t.Fatal("unknown capability unexpectedly supported")
	}
	if got := sys.FrameSkip(); got != 0 {
		t.Fatalf("default FrameSkip = %d, want 0", got)
	}
	if got := sys.RunAhead(); got {
		t.Fatal("default RunAhead = true, want false")
	}

	sys.SetFrameSkip(3)
	if got := sys.FrameSkip(); got != 3 {
		t.Fatalf("FrameSkip = %d, want 3", got)
	}
	sys.SetFrameSkip(42)
	if got := sys.FrameSkip(); got != 9 {
		t.Fatalf("FrameSkip clamp = %d, want 9", got)
	}
	sys.SetRunAhead(true)
	if !sys.RunAhead() {
		t.Fatal("RunAhead not enabled")
	}

	if !sys.Set("frameskip", uint(2)) || sys.FrameSkip() != 2 {
		t.Fatalf("Set(frameskip) failed, got %d", sys.FrameSkip())
	}
	if !sys.Set("run_ahead", false) || sys.RunAhead() {
		t.Fatal("Set(run_ahead) failed")
	}
	if sys.Set("frameskip", int(2)) {
		t.Fatal("Set(frameskip) accepted invalid type")
	}
	if sys.Set("unknown", true) {
		t.Fatal("Set(unknown) succeeded unexpectedly")
	}
	if got := sys.Get("frame_skip"); got != uint(2) {
		t.Fatalf("Get(frame_skip) = %v, want 2", got)
	}
	if got := sys.Get("runahead"); got != false {
		t.Fatalf("Get(runahead) = %v, want false", got)
	}
	if got := sys.Get("unknown"); got != nil {
		t.Fatalf("Get(unknown) = %v, want nil", got)
	}

	cheats := []Cheat{{Address: 0x7E0010, Value: 0x42, Enabled: true}}
	if !sys.Set("cheats", cheats) {
		t.Fatal("Set(cheats) failed")
	}
	gotCheats, ok := sys.Get("cheats").([]Cheat)
	if !ok || len(gotCheats) != 1 || gotCheats[0].Value != 0x42 {
		t.Fatalf("Get(cheats) = %#v", sys.Get("cheats"))
	}
	if sys.Set("cheats", "bad") {
		t.Fatal("Set(cheats) accepted invalid type")
	}
}

func TestRunAheadPreservesCoreState(t *testing.T) {
	rom := newBootableTestROM()

	base := NewSystem(nil)
	if err := base.LoadROM(rom); err != nil {
		t.Fatalf("base LoadROM: %v", err)
	}
	base.Power()
	if err := base.RunFrame(); err != nil {
		t.Fatalf("base RunFrame: %v", err)
	}

	ra := NewSystem(nil)
	if err := ra.LoadROM(rom); err != nil {
		t.Fatalf("runahead LoadROM: %v", err)
	}
	ra.Power()
	ra.SetRunAhead(true)
	if err := ra.RunFrame(); err != nil {
		t.Fatalf("runahead RunFrame: %v", err)
	}

	if ra.CPU.PC != base.CPU.PC || ra.CPU.PB != base.CPU.PB || ra.CPU.Cycles != base.CPU.Cycles {
		t.Fatalf("runahead altered CPU state: got PC=%02X:%04X cycles=%d, want PC=%02X:%04X cycles=%d",
			ra.CPU.PB, ra.CPU.PC, ra.CPU.Cycles,
			base.CPU.PB, base.CPU.PC, base.CPU.Cycles)
	}
}

func TestFrameSkipRunsAdditionalFrames(t *testing.T) {
	rom := newBootableTestROM()

	base := NewSystem(nil)
	if err := base.LoadROM(rom); err != nil {
		t.Fatalf("base LoadROM: %v", err)
	}
	base.Power()
	if err := base.RunFrame(); err != nil {
		t.Fatalf("base RunFrame: %v", err)
	}

	ff := NewSystem(nil)
	if err := ff.LoadROM(rom); err != nil {
		t.Fatalf("ff LoadROM: %v", err)
	}
	ff.Power()
	ff.SetFrameSkip(1)
	if err := ff.RunFrame(); err != nil {
		t.Fatalf("ff RunFrame: %v", err)
	}

	if ff.CPU.Cycles <= base.CPU.Cycles {
		t.Fatalf("frame skip did not advance extra cycles: base=%d ff=%d", base.CPU.Cycles, ff.CPU.Cycles)
	}
}

func TestLoadROMNormalizesCopierHeader(t *testing.T) {
	payload := newBootableTestROM()
	headered := append(make([]byte, 512), payload...)
	for i := 0; i < 512; i++ {
		headered[i] = 0xAA
	}

	plain := NewSystem(nil)
	if err := plain.LoadROM(payload); err != nil {
		t.Fatalf("plain LoadROM: %v", err)
	}
	header := NewSystem(nil)
	if err := header.LoadROM(headered); err != nil {
		t.Fatalf("headered LoadROM: %v", err)
	}
	if plain.romHash != header.romHash {
		t.Fatal("rom hash differs for headered rom")
	}

	header.Power()
	if header.CPU.PC != 0x8000 {
		t.Fatalf("headered reset vector PC = %04X, want 8000", header.CPU.PC)
	}
	if header.CPU.Cycles != 186 {
		t.Fatalf("headered reset cycles = %d, want 186", header.CPU.Cycles)
	}
	if header.CPU.S != 0x01FC {
		t.Fatalf("headered reset stack = %04X, want 01FC", header.CPU.S)
	}
}

func TestCheatApplicationAndCompare(t *testing.T) {
	rom := newBootableTestROM()
	sys := NewSystem(nil)
	if err := sys.LoadROM(rom); err != nil {
		t.Fatalf("LoadROM: %v", err)
	}
	sys.Power()

	sys.Bus.Write(0x7E0010, 0x11)
	if err := sys.SetCheats([]Cheat{
		{Address: 0x7E0010, Value: 0x99, Enabled: true},
		{Address: 0x7E0011, Value: 0x55, HasCompare: true, Compare: 0xAA, Enabled: true},
	}); err != nil {
		t.Fatalf("SetCheats: %v", err)
	}
	sys.Bus.Write(0x7E0011, 0x00)

	if err := sys.RunFrame(); err != nil {
		t.Fatalf("RunFrame: %v", err)
	}
	if got := sys.Bus.Read(0x7E0010); got != 0x99 {
		t.Fatalf("cheat write got %02X, want 99", got)
	}
	if got := sys.Bus.Read(0x7E0011); got != 0x00 {
		t.Fatalf("compare cheat wrote unexpectedly: got %02X, want 00", got)
	}

	sys.Bus.Write(0x7E0011, 0xAA)
	if err := sys.RunFrame(); err != nil {
		t.Fatalf("RunFrame with compare: %v", err)
	}
	if got := sys.Bus.Read(0x7E0011); got != 0x55 {
		t.Fatalf("compare cheat got %02X, want 55", got)
	}
}

func TestSetCheatsRejectsOutOfRangeAddress(t *testing.T) {
	sys := NewSystem(nil)
	err := sys.SetCheats([]Cheat{{Address: 0x1000000, Value: 1, Enabled: true}})
	if err == nil {
		t.Fatal("SetCheats with out-of-range address succeeded")
	}
}

func TestLoadROMRegionAffectsFrameTiming(t *testing.T) {
	ntscROM := newBootableTestROM()
	ntscROM[0x7fd9] = 0x01
	palROM := newBootableTestROM()
	palROM[0x7fd9] = 0x02

	ntsc := NewSystem(nil)
	if err := ntsc.LoadROM(ntscROM); err != nil {
		t.Fatalf("ntsc LoadROM: %v", err)
	}
	ntsc.Power()
	if err := ntsc.RunFrame(); err != nil {
		t.Fatalf("ntsc RunFrame: %v", err)
	}

	pal := NewSystem(nil)
	if err := pal.LoadROM(palROM); err != nil {
		t.Fatalf("pal LoadROM: %v", err)
	}
	pal.Power()
	if err := pal.RunFrame(); err != nil {
		t.Fatalf("pal RunFrame: %v", err)
	}

	if pal.CPU.Cycles <= ntsc.CPU.Cycles+50000 {
		t.Fatalf("PAL timing not applied: ntsc=%d pal=%d", ntsc.CPU.Cycles, pal.CPU.Cycles)
	}
	if pal.PPU.FrameCount != 1 || pal.PPU.SaveState().VPeriod < 312 {
		t.Fatalf("PAL PPU timing not applied: frame=%d vperiod=%d", pal.PPU.FrameCount, pal.PPU.SaveState().VPeriod)
	}
}

func TestIODeviceWRIOControlsHVCounterLatch(t *testing.T) {
	sys := NewSystem(nil)
	sys.Power()
	sys.CPU.Cycles = 0
	sys.PPU.Power(true) // Position both clocks at the fixture origin.

	sys.Bus.MDR = 0x5a
	if got := sys.io.Read(0x2137); got != 0x5a {
		t.Fatalf("SLHV read = %02X, want CPU MDR 5a", got)
	}
	if got := sys.io.Read(0x213f); got&0x40 == 0 {
		t.Fatalf("STAT78 after enabled SLHV = %02X, want latch bit set", got)
	}

	sys.io.Write(0x4201, 0x00)
	if got := sys.io.Read(0x4213); got != 0x00 {
		t.Fatalf("RDIO = %02X, want 00", got)
	}
	for i := 0; i < 10; i++ {
		sys.PPU.Run()
	}
	sys.Bus.MDR = 0xa5
	if got := sys.io.Read(0x2137); got != 0xa5 {
		t.Fatalf("disabled SLHV read = %02X, want CPU MDR a5", got)
	}
	if got := sys.io.Read(0x213c); got != 0x00 {
		t.Fatalf("OPHCT low after disabled SLHV = %02X, want prior latch 00", got)
	}
	if got := sys.io.Read(0x213f); got&0x40 == 0 {
		t.Fatalf("STAT78 with WRIO bit 7 clear = %02X, want forced latch bit", got)
	}
}

// TestNMIReArmDeliversHeldNMI verifies the bsnes nmiPoll semantics: when
// NMITIMEN.7 transitions 0->1 while the RDNMI flag is still latched,
// the CPU receives the held NMI immediately. Without this the game
// loses one NMI per disable/re-enable cycle.
func TestNMIReArmDeliversHeldNMI(t *testing.T) {
	sys := NewSystem(nil)
	io := &IODevice{sys: sys}

	// Latch the PPU NMI flag (simulating vblank entry).
	sys.CPU.Cycles = 225*1364 + 128
	sys.PPU.NMIFlag = true
	sys.CPU.NMIPending = false

	// 0->1 transition on $4200 d7 with NMI line held -> deliver immediately.
	io.Write(0x4200, 0x80)
	if !sys.CPU.NMIPending {
		t.Fatalf("NMIPending after rising-edge re-arm = false, want true")
	}

	// Subsequent same-value write must NOT redeliver.
	sys.CPU.NMIPending = false
	io.Write(0x4200, 0x80)
	if sys.CPU.NMIPending {
		t.Fatalf("NMIPending after non-edge write = true, want false")
	}

	// Disable then re-enable while flag still set: rising edge again.
	io.Write(0x4200, 0x00)
	sys.CPU.NMIPending = false
	io.Write(0x4200, 0x80)
	if !sys.CPU.NMIPending {
		t.Fatalf("NMIPending after disable->enable cycle = false, want true")
	}

	// RDNMI bit 7 is read-to-clear. Re-arming NMI during vblank after software
	// has read $4210 must not repeatedly redeliver from the live VBlank bit.
	io.Write(0x4200, 0x00)
	sys.CPU.Cycles = 225*1364 + 320
	sys.PPU.NMIFlag = false
	sys.CPU.NMIPending = false
	io.Write(0x4200, 0x80)
	if sys.CPU.NMIPending {
		t.Fatalf("NMIPending after vblank re-arm with RDNMI clear = true, want false")
	}

	// If the PPU NMI line is NOT held, rising edge must not deliver.
	io.Write(0x4200, 0x00)
	sys.CPU.Cycles = 0
	sys.PPU.NMIFlag = false
	sys.CPU.NMIPending = false
	io.Write(0x4200, 0x80)
	if sys.CPU.NMIPending {
		t.Fatalf("NMIPending without held flag = true, want false")
	}
}
