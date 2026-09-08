package apu

// inputReadOp retains the IPL-used direct read family at SMP clock precision.
// Phase counts SMP input clocks from opcode start; SPC Cycles counts opcode
// cycles. Waiting suspends before reading a CPU port, after the first half
// of its bus cycle. The second half cannot execute before the read resumes.
type inputReadOp struct {
	active                bool
	opcode, phase         uint8
	addr                  uint16
	immediate, low, value uint8
	waiting               bool
}

func (a *APU) startInputRead(opcode byte) bool {
	p := a.Processor
	if p.Stopped || p.P {
		return false
	}
	operand := uint16(1)
	switch opcode {
	case 0xe4, 0xeb, 0x7e, 0xba:
	case 0x78:
		operand = 2
	default:
		return false
	}
	addr := uint16(a.peekInstruction(p.PC + operand))
	if !isOutputPort(addr) || opcode == 0xba && addr == 0xf7 {
		return false
	}
	p.ClearPendingPortOperation()
	p.PC++
	a.inputOp = inputReadOp{active: true, opcode: opcode, phase: 2}
	return true
}

func (a *APU) advanceInputRead() {
	m := &a.inputOp
	m.phase++
	p := a.Processor
	switch m.phase {
	case 4:
		v := a.Read(p.PC)
		p.PC++
		if m.opcode == 0x78 {
			m.immediate = v
		} else {
			m.addr = uint16(v)
		}
	case 5:
		if m.opcode != 0x78 {
			m.waiting = true
		}
	case 6:
		switch m.opcode {
		case 0x78:
			m.addr = uint16(a.Read(p.PC))
			p.PC++
		case 0xba:
			m.low = m.value
		case 0xe4:
			p.A = m.value
			p.SetZN(p.A)
			a.finishInputRead(3)
		case 0xeb:
			p.Y = m.value
			p.SetZN(p.Y)
			a.finishInputRead(3)
		case 0x7e:
			a.compareInput(p.Y, m.value)
			a.finishInputRead(3)
		}
	case 7:
		if m.opcode == 0x78 {
			m.waiting = true
		}
	case 8:
		if m.opcode == 0x78 {
			a.compareInput(m.value, m.immediate)
		}
	case 9:
		if m.opcode == 0xba {
			m.addr++
			m.waiting = true
		}
	case 10:
		if m.opcode == 0xba {
			p.A = m.low
			p.Y = m.value
			p.SetZN16(uint16(p.A) | uint16(p.Y)<<8)
		}
		a.finishInputRead(5)
	}
}

func (a *APU) resumeInputRead() {
	if a.dummyReadWaiting {
		_ = a.Read(a.microOp.addr)
		a.dummyReadWaiting = false
		a.dummyReadDone = true
	}
	if !a.inputOp.waiting {
		return
	}
	a.inputOp.value = a.Read(a.inputOp.addr)
	a.inputOp.waiting = false
}

func (a *APU) compareInput(left, right uint8) {
	a.Processor.C = left >= right
	a.Processor.SetZN(left - right)
}

func (a *APU) finishInputRead(cycles uint64) {
	a.Processor.Cycles += cycles
	a.inputOp = inputReadOp{}
}

func (a *APU) portDummyReadPhase() bool {
	m := a.microOp
	return m.active && isOutputPort(m.addr) && (m.opcode == 0x8f && m.step == 3 || (m.opcode == 0xc4 || m.opcode == 0xcb || m.opcode == 0xd8) && m.step == 2)
}
