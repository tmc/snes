package gsu

// SFR bit positions on the status/flag register.
//
// Reference: bsnes sfc/coprocessor/superfx/gsu.hpp.
const (
	SFRZ    = 1 << 1  // zero result
	SFRCY   = 1 << 2  // carry
	SFRS    = 1 << 3  // sign
	SFROV   = 1 << 4  // overflow
	SFRG    = 1 << 5  // go (cpu running)
	SFRR    = 1 << 6  // rom-read in progress
	SFRALT1 = 1 << 8  // prefix ALT1 (cleared after next op)
	SFRALT2 = 1 << 9  // prefix ALT2 (cleared after next op)
	SFRIL   = 1 << 10 // immediate low latched
	SFRIH   = 1 << 11 // immediate high latched
	SFRB    = 1 << 12 // WITH-prefix active (cleared after next op)
	SFRIRQ  = 1 << 15 // irq raised
)

// altMask is the set of SFR bits that must clear automatically after an
// instruction has consumed the corresponding prefix.
const altMask = SFRALT1 | SFRALT2 | SFRB

// AltMode is the decoded prefix mode for the next instruction.
type AltMode uint8

const (
	AltNone AltMode = iota
	Alt1
	Alt2
	Alt3 // Alt1 | Alt2 combined
)

// alt returns the currently latched ALT prefix mode.
func (d *Device) alt() AltMode {
	has1 := d.SFR&SFRALT1 != 0
	has2 := d.SFR&SFRALT2 != 0
	switch {
	case has1 && has2:
		return Alt3
	case has1:
		return Alt1
	case has2:
		return Alt2
	default:
		return AltNone
	}
}

// consumePrefixes clears prefix bits that live for exactly one instruction.
// The returned AltMode reflects the state *before* clearing.
func (d *Device) consumePrefixes() AltMode {
	mode := d.alt()
	d.SFR &^= altMask
	d.withPrefix = false
	d.toPrefix = false
	d.fromPrefix = false
	d.withReg = 0
	return mode
}

// setZN latches zero and sign flags from a 16-bit result.
func (d *Device) setZN(v uint16) {
	if v == 0 {
		d.SFR |= SFRZ
	} else {
		d.SFR &^= SFRZ
	}
	if v&0x8000 != 0 {
		d.SFR |= SFRS
	} else {
		d.SFR &^= SFRS
	}
}

// setByteZN latches zero and sign flags from an 8-bit result stored in a
// 16-bit register.
func (d *Device) setByteZN(v uint16) {
	if v&0x00FF == 0 {
		d.SFR |= SFRZ
	} else {
		d.SFR &^= SFRZ
	}
	if v&0x0080 != 0 {
		d.SFR |= SFRS
	} else {
		d.SFR &^= SFRS
	}
}

// setCarry sets or clears the carry flag.
func (d *Device) setCarry(c bool) {
	if c {
		d.SFR |= SFRCY
	} else {
		d.SFR &^= SFRCY
	}
}

// setOverflow sets or clears the signed-overflow flag.
func (d *Device) setOverflow(v bool) {
	if v {
		d.SFR |= SFROV
	} else {
		d.SFR &^= SFROV
	}
}

// carryIn returns 0 or 1 depending on the current carry flag.
func (d *Device) carryIn() uint16 {
	if d.SFR&SFRCY != 0 {
		return 1
	}
	return 0
}
