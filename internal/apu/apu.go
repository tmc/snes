package apu

import (
	"sync"

	"github.com/tmc/snes/internal/apu/dsp"
	"github.com/tmc/snes/internal/apu/spc700"
)

// Timer represents one of the three SPC700 timers.
type Timer struct {
	Enabled bool
	Target  uint8 // 8-bit latch ($FA, $FB, $FC)
	Counter uint8 // 4-bit up-counter ($FD, $FE, $FF)

	// Internal state
	divider uint16 // Counts up to 128 (T0/T1) or 16 (T2)
	stage2  uint8  // Counts up to Target
}

type APU struct {
	// Trace observes timing events synchronously. The callback must not mutate
	// or re-enter the APU. It is not serialized in save states. Nil disables tracing.
	Trace func(TimingEvent)

	InPorts  [4]uint8 // Written by CPU, Read by APU
	OutPorts [4]uint8 // Written by APU, Read by CPU

	RAM       [65536]uint8
	IPLROM    [64]uint8 // FFC0-FFFF
	Processor *spc700.SPC700
	Timers    [3]Timer
	Control   uint8 // $F1

	DSP     *dsp.DSP
	dspAddr uint8 // $F2

	cycles    uint64
	dspCycles uint64
	pending   uint32

	deferOutPortWrites              bool
	pendingOutPortMask              uint8
	pendingOutPorts                 [4]uint8
	microOp                         apuMicroOp
	inputOp                         inputReadOp
	dummyReadWaiting, dummyReadDone bool

	audioMu     sync.Mutex
	audioBuffer []int16
	audioCount  int

	patchPortCompare bool
}

type apuMicroOp struct {
	active bool
	opcode uint8
	step   uint8
	addr   uint16
	val    uint8
}

// SampleRate is the number of stereo sample frames emitted per second.
// It follows the bsnes default clock policy.
const SampleRate = 32040

const (
	spcMachineFrequency = SampleRate * 64
	dspSampleDivider    = 64
	timer01Divider      = 256
	timer2Divider       = 32
	audioBufferSamples  = 16 * 1024
)

func NewAPU() *APU {
	apu := &APU{}
	apu.Processor = spc700.New(apu)
	apu.DSP = dsp.New()
	apu.DSP.SetRAMReader(func(addr uint16) uint8 {
		return apu.RAM[addr]
	})
	apu.DSP.SetRAMWriter(func(addr uint16, val uint8) {
		apu.RAM[addr] = val
	})
	apu.audioBuffer = make([]int16, audioBufferSamples)

	// Load IPL
	copy(apu.IPLROM[:], StandardIPLROM[:])
	apu.Control = 0x80 // Enable IPL
	// Set Reset Vector in Processor?
	// Processor Reset() fetches vector from FFFE.
	// Map handles FFFE -> IPLROM[62].
	return apu
}

// spc700.Bus Interface Implementation

func (a *APU) Read(addr uint16) uint8 {
	// Handle MMIO
	if addr >= 0x00F0 && addr <= 0x00FF {
		return a.readMMIO(addr)
	}

	// IPL ROM Mapping: Enabled if $F1 bit 7 is set
	if a.Control&0x80 != 0 && addr >= 0xFFC0 {
		return a.IPLROM[addr-0xFFC0]
	}

	return a.RAM[addr]
}

func (a *APU) Write(addr uint16, val uint8) {
	// Handle MMIO
	if addr >= 0x00F0 && addr <= 0x00FF {
		a.writeMMIO(addr, val)
	}

	// Always write to RAM (Shadow RAM under IPL/MMIO)
	a.RAM[addr] = val
}

func (a *APU) readMMIO(addr uint16) uint8 {
	switch addr {
	case 0x00F0, 0x00F1:
		return 0 // Write-only? Or returns 0.
	case 0x00F2: // DSP Addr
		return a.dspAddr
	case 0x00F3: // DSP Data
		return a.DSP.Read(a.dspAddr)
	case 0x00F4, 0x00F5, 0x00F6, 0x00F7:
		value := a.InPorts[addr-0xf4]
		a.trace("input-read", addr-0xf4, value, 0, 0)
		return value
	case 0x00FD:
		val := a.Timers[0].Counter
		a.Timers[0].Counter = 0 // Reset on read
		return val
	case 0x00FE:
		val := a.Timers[1].Counter
		a.Timers[1].Counter = 0
		return val
	case 0x00FF:
		val := a.Timers[2].Counter
		a.Timers[2].Counter = 0
		return val
	}
	return a.RAM[addr]
}

func (a *APU) writeMMIO(addr uint16, val uint8) {
	switch addr {
	case 0x00F1: // Control
		a.Control = val

		// Bit 4/5 clear CPU->APU ports 0/1 and 2/3 whenever written set.
		if val&0x10 != 0 {
			a.InPorts[0] = 0
			a.InPorts[1] = 0
		}
		if val&0x20 != 0 {
			a.InPorts[2] = 0
			a.InPorts[3] = 0
		}

		// Bit 0-2: Enable Timers. Enabling a timer resets its internal state.
		for i := 0; i < 3; i++ {
			nextEnabled := val&(1<<uint(i)) != 0
			if !a.Timers[i].Enabled && nextEnabled {
				a.Timers[i].stage2 = 0
				a.Timers[i].Counter = 0
			}
			a.Timers[i].Enabled = nextEnabled
		}

	case 0x00F2: // DSP Addr
		a.dspAddr = val
	case 0x00F3: // DSP Data
		if a.dspAddr == 0x4C && a.dspCycles == dspSampleDivider-2 {
			a.DSP.WriteLateKON(val)
		} else {
			a.DSP.Write(a.dspAddr, val)
		}
		a.trace("dsp-write", uint16(a.dspAddr), val, 0, 0)

	case 0x00FA:
		a.Timers[0].Target = val
	case 0x00FB:
		a.Timers[1].Target = val
	case 0x00FC:
		a.Timers[2].Target = val

	case 0x00F4:
		// DEBUG: Trace APU output to properties
		// fmt.Printf("APU WriteMMIO P0: %02X (PC=%04X)\n", val, a.Processor.PC)
		a.writeOutPort(0, val)
	case 0x00F5:
		a.writeOutPort(1, val)
	case 0x00F6:
		// DEBUG: Trace APU write to P2
		// fmt.Printf("APU WriteMMIO P2: %02X (PC=%04X)\n", val, a.Processor.PC)
		a.writeOutPort(2, val)
	case 0x00F7:
		a.writeOutPort(3, val)
	}
}

func (a *APU) writeOutPort(index uint8, val uint8) {
	if a.deferOutPortWrites {
		a.pendingOutPortMask |= 1 << index
		a.pendingOutPorts[index] = val
		return
	}
	a.OutPorts[index] = val
	a.trace("output-port", uint16(index), val, 0, 0)
}

func (a *APU) flushOutPortWrites() {
	for i := 0; i < 4; i++ {
		if a.pendingOutPortMask&(1<<uint(i)) != 0 {
			a.OutPorts[i] = a.pendingOutPorts[i]
			a.trace("output-port", uint16(i), a.OutPorts[i], 0, 0)
		}
	}
	a.pendingOutPortMask = 0
}

// Scheduler Thread Interface
func (a *APU) Run() {
	a.resumePortAssignment()
	a.resumeInputRead()
	a.runCycle()
	a.resumePortAssignment()
	a.resumeInputRead()
}

func (a *APU) runCycle() {
	// One invocation advances one SMP input clock. Ordinary opcode
	// cycles consume two clocks. DSP/timers advance during the wait, before
	// the bus operation completing that wait.
	a.cycles++
	a.dspCycles++
	if a.dspCycles >= dspSampleDivider {
		a.dspCycles -= dspSampleDivider
		l, r := a.DSP.Sample()
		a.appendAudio(l, r)
		a.trace("sample", 0, 0, l, r)
	}
	a.TickTimers(1)
	if a.inputOp.active {
		a.advanceInputRead()
	} else if a.cycles%2 != 0 && a.portDummyReadPhase() {
		a.dummyReadWaiting = true
	} else if a.cycles%2 == 0 {
		if a.microOp.active {
			a.runMicroOp()
		} else if a.pending == 0 {
			a.retireInstruction()
		} else {
			a.pending--
		}
		if a.pending == 0 {
			a.flushOutPortWrites()
		}
	}
}

func (a *APU) runMicroOp() {
	switch a.microOp.opcode {
	case 0xC4, 0xCB, 0xD8:
		a.runMicroOpMOVDirectRegister()
	case 0xE4:
		a.runMicroOpMOVADirect()
	case 0x8F:
		a.runMicroOpMOVDirectImmediate()
	default:
		a.microOp = apuMicroOp{}
	}
}

func (a *APU) runMicroOpMOVADirect() {
	p := a.Processor
	switch a.microOp.step {
	case 1:
		addr := uint16(a.Read(p.PC))
		p.PC++
		if p.P {
			addr |= 0x100
		}
		a.microOp.addr = addr
		a.microOp.step = 2
	case 2:
		p.A = a.Read(a.microOp.addr)
		p.SetZN(p.A)
		p.Cycles += 3
		p.ArmPendingPortLoadA(a.microOp.addr)
		a.microOp = apuMicroOp{}
		a.pending = 0
	}
}

func (a *APU) runMicroOpMOVDirectImmediate() {
	p := a.Processor
	switch a.microOp.step {
	case 1:
		a.microOp.val = a.Read(p.PC)
		p.PC++
		a.microOp.step = 2
	case 2:
		addr := uint16(a.Read(p.PC))
		p.PC++
		if p.P {
			addr |= 0x100
		}
		a.microOp.addr = addr
		a.microOp.step = 3
	case 3:
		if !a.dummyReadDone {
			_ = a.Read(a.microOp.addr)
		}
		a.dummyReadDone = false
		a.microOp.step = 4
	case 4:
		if isOutputPort(a.microOp.addr) {
			a.RAM[a.microOp.addr] = a.microOp.val
			a.microOp.step = 5
			return
		}
		a.Write(a.microOp.addr, a.microOp.val)
		p.Cycles += 5
		a.microOp = apuMicroOp{}
		a.pending = 0
	}
}

// retireInstruction steps the SPC700 by exactly one opcode and programs
// a.pending with the remaining cycles before the NEXT opcode retires. Since
// this opcode consumes (delta) cycles total and "retires" on the cycle this
// function is called, we owe (delta-1) more cycles of idle time before the
// next retirement.
func (a *APU) retireInstruction() {
	opcode := a.peekInstruction(a.Processor.PC)
	if a.startInputRead(opcode) {
		return
	}
	var operand uint16
	switch opcode {
	case 0xc4, 0xcb, 0xd8:
		operand = 1
	}
	if !a.Processor.Stopped && !a.Processor.P && operand != 0 && isOutputPort(uint16(a.peekInstruction(a.Processor.PC+operand))) {
		a.Processor.ClearPendingPortOperation()
		a.Processor.PC++
		a.microOp = apuMicroOp{active: true, opcode: opcode, step: 1}
		a.pending = 0
		return
	}
	if !a.Processor.Stopped && a.Processor.PC != 0 && a.peekInstruction(a.Processor.PC) == 0xE4 && a.peekInstruction(a.Processor.PC+1) == 0xFD {
		a.Processor.ClearPendingPortOperation()
		a.Processor.PC++
		a.microOp = apuMicroOp{active: true, opcode: 0xE4, step: 1}
		a.pending = 0
		return
	}
	// MOV dp, #imm stores on its fifth cycle (fetch, fetch, fetch, dummy
	// read, write), so DSP, timer and port writes land when bsnes's do.
	if !a.Processor.Stopped && opcode == 0x8F {
		a.Processor.ClearPendingPortOperation()
		a.Processor.PC++
		a.microOp = apuMicroOp{active: true, opcode: 0x8F, step: 1}
		a.pending = 0
		return
	}
	startCycles := a.Processor.Cycles
	a.deferOutPortWrites = true
	a.Processor.Step()
	a.deferOutPortWrites = false
	delta := a.Processor.Cycles - startCycles
	if delta == 0 {
		delta = 2
		a.Processor.Cycles += delta
	}
	if delta >= 1 {
		a.pending = uint32(delta - 1)
	}
}

func (a *APU) TickTimers(cycles uint64) {
	// Timers are driven from the SMP machine-cycle clock.
	// Timer 0/1 stage 0 wraps every 256 clocks and Timer 2 every 32.

	// Helper to advanced timer
	tick := func(idx int, dividerTarget uint16) {
		t := &a.Timers[idx]
		if !t.Enabled {
			return
		}

		totalDivider := uint64(t.divider) + cycles
		ticks := totalDivider / uint64(dividerTarget)
		t.divider = uint16(totalDivider % uint64(dividerTarget))

		if ticks > 0 {
			total := uint64(t.stage2) + ticks
			target := uint64(t.Target)
			if target == 0 {
				target = 256
			}

			countIncrements := total / target
			t.stage2 = uint8(total % target)

			t.Counter = (t.Counter + uint8(countIncrements&0x0F)) & 0x0F
		}
	}

	tick(0, timer01Divider)
	tick(1, timer01Divider)
	tick(2, timer2Divider)
}

func (a *APU) tickTimerStage2(idx int) {
	t := &a.Timers[idx]
	t.stage2++
	if t.stage2 == t.Target {
		t.stage2 = 0
		t.Counter = (t.Counter + 1) & 0x0F
	}
}

func (a *APU) GetCycles() uint64 { return a.cycles }
func (a *APU) ResetCycles() {
	a.cycles = 0
	a.Processor.Cycles = 0
	a.pending = 0
	a.microOp = apuMicroOp{}
	a.dspCycles = 0
	a.inputOp = inputReadOp{}
	a.dummyReadWaiting = false
	a.dummyReadDone = false
}
func (a *APU) Frequency() uint64 { return spcMachineFrequency }

func (a *APU) appendAudio(left, right int16) {
	a.audioMu.Lock()
	defer a.audioMu.Unlock()

	if a.audioCount+2 > len(a.audioBuffer) {
		return
	}
	a.audioBuffer[a.audioCount] = left
	a.audioBuffer[a.audioCount+1] = right
	a.audioCount += 2
}

// DrainAudio copies available stereo samples into dst and returns the count.
func (a *APU) DrainAudio(dst []int16) int {
	a.audioMu.Lock()
	defer a.audioMu.Unlock()

	n := copy(dst, a.audioBuffer[:a.audioCount])
	copy(a.audioBuffer, a.audioBuffer[n:a.audioCount])
	a.audioCount -= n
	return n
}

// Port Access (from CPU/System)
func (a *APU) ReadPort(address uint32) uint8 {
	idx := address & 0x3
	return a.OutPorts[idx]
}
func (a *APU) WritePort(index uint32, value uint8) {
	idx := index & 3
	a.InPorts[idx] = value
	a.trace("input-port", uint16(idx), value, 0, 0)
	if a.patchPortCompare {
		a.Processor.PatchPortWrite(0x00F4+uint16(idx), value)
	}
}

// SetPortComparePatch enables the IPL port-compare timing compatibility path.
func (a *APU) SetPortComparePatch(enabled bool) {
	a.patchPortCompare = enabled
}

func (a *APU) Power(reset bool) {
	a.Processor.Reset()
	// Initialize InPorts to 00 (Cpu starts with 00 or writes 00)
	a.InPorts[0] = 0x00
	a.InPorts[1] = 0x00
	a.InPorts[2] = 0x00
	a.InPorts[3] = 0x00

	a.OutPorts[0] = 0x00
	a.OutPorts[1] = 0x00
	a.OutPorts[2] = 0x00
	a.OutPorts[3] = 0x00

	a.audioMu.Lock()
	a.audioCount = 0
	a.audioMu.Unlock()
	a.pending = 0
	a.microOp = apuMicroOp{}
	a.inputOp = inputReadOp{}
	a.dummyReadWaiting = false
	a.dummyReadDone = false
}

// Standard SNES IPL ROM (64 bytes).
// This is the stock boot ROM sequence used by commercial software handshake logic.
var StandardIPLROM = [64]uint8{
	0xCD, 0xEF, 0xBD, 0xE8, 0x00, 0xC6, 0x1D, 0xD0,
	0xFC, 0x8F, 0xAA, 0xF4, 0x8F, 0xBB, 0xF5, 0x78,
	0xCC, 0xF4, 0xD0, 0xFB, 0x2F, 0x19, 0xEB, 0xF4,
	0xD0, 0xFC, 0x7E, 0xF4, 0xD0, 0x0B, 0xE4, 0xF5,
	0xCB, 0xF4, 0xD7, 0x00, 0xFC, 0xD0, 0xF3, 0xAB,
	0x01, 0x10, 0xEF, 0x7E, 0xF4, 0x10, 0xEB, 0xBA,
	0xF6, 0xDA, 0x00, 0xBA, 0xF4, 0xC4, 0xF4, 0xDD,
	0x5D, 0xD0, 0xDB, 0x1F, 0x00, 0x00, 0xC0, 0xFF,
}

func isOutputPort(addr uint16) bool { return addr >= 0xf4 && addr <= 0xf7 }

// MOV dp,r: opcode fetch, operand fetch, dummy read, then write. The last
// cycle updates shadow RAM before synchronizeCPU can suspend assignment.
func (a *APU) runMicroOpMOVDirectRegister() {
	switch a.microOp.step {
	case 1:
		a.microOp.addr = uint16(a.Read(a.Processor.PC))
		a.Processor.ClearPendingPortOperation()
		a.Processor.PC++
		a.microOp.step = 2
	case 2:
		if !a.dummyReadDone {
			_ = a.Read(a.microOp.addr)
		}
		a.dummyReadDone = false
		a.microOp.step = 3
	case 3:
		switch a.microOp.opcode {
		case 0xc4:
			a.microOp.val = a.Processor.A
		case 0xcb:
			a.microOp.val = a.Processor.Y
		case 0xd8:
			a.microOp.val = a.Processor.X
		}
		a.RAM[a.microOp.addr] = a.microOp.val
		a.microOp.step = 4
	}
}

func (a *APU) portAssignmentPending() bool {
	return a.microOp.active && isOutputPort(a.microOp.addr) &&
		((a.microOp.opcode == 0xc4 || a.microOp.opcode == 0xcb || a.microOp.opcode == 0xd8) && a.microOp.step == 4 || a.microOp.opcode == 0x8f && a.microOp.step == 5)
}

// Assignment consumes no additional SPC cycle. PC, address and value remain
// in the micro-op until this continuation runs, including equal-byte writes.
func (a *APU) resumePortAssignment() {
	if !a.portAssignmentPending() {
		return
	}
	a.OutPorts[a.microOp.addr-0xf4] = a.microOp.val
	a.trace("output-port", a.microOp.addr-0xf4, a.microOp.val, 0, 0)
	a.Processor.Cycles += uint64(a.microOp.step)
	a.microOp = apuMicroOp{}
}

// Dispatch lookahead must not consume MMIO reads. Executing an instruction
// or operand from MMIO stays on the ordinary Step path.
func (a *APU) peekInstruction(addr uint16) byte {
	if addr >= 0xf0 && addr <= 0xff {
		return 0
	}
	if addr >= 0xffc0 && a.Control&0x80 != 0 {
		return a.IPLROM[addr-0xffc0]
	}
	return a.RAM[addr]
}
