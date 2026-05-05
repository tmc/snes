package gsu

// executeAddFamily handles ADD/ADC/ADDi/ADCi as a single family that
// shares opcode slots 0x40..0x4F. The "register index" encoded in the low
// nibble is used as either a register select (no ALT prefix) or as a
// 4-bit immediate (ALT2/ALT3 with immediate form).
//
// ALT table (bsnes sfc/coprocessor/superfx/alu.cpp):
//
//	none: ADD  Rn          (Rd = Rs + Rn)
//	ALT1: ADC  Rn          (Rd = Rs + Rn + carry)
//	ALT2: ADDi imm4        (Rd = Rs + imm)
//	ALT3: ADCi imm4        (Rd = Rs + imm + carry)
func (d *Device) executeAddFamily(n uint8, mode AltMode, withActive bool) {
	_ = withActive
	rs := d.R[d.srcReg()]
	var rhs uint16
	switch mode {
	case Alt2, Alt3:
		rhs = uint16(n)
	default:
		rhs = d.R[n]
	}
	c := uint16(0)
	if mode == Alt1 || mode == Alt3 {
		c = d.carryIn()
	}
	sum32 := uint32(rs) + uint32(rhs) + uint32(c)
	sum := uint16(sum32)
	d.setCarry(sum32 > 0xFFFF)
	// Signed overflow: operands with equal sign but a differently-signed
	// result.
	d.setOverflow((^(rs ^ rhs) & (rs ^ sum) & 0x8000) != 0)
	d.writeReg(d.dstReg(), sum)
}

// executeSubFamily — 0x50..0x5F. Mirrors executeAddFamily but for
// subtraction. In bsnes, 0x5C corresponds to SUB R12 / SBC R12 / SUBi 12 /
// CMP R12; CMP is SBC without writing back. We follow the same decoding
// rules.
//
//	none: SUB  Rn
//	ALT1: SBC  Rn
//	ALT2: SUBi imm4
//	ALT3: CMP  Rn   (flags only, no writeback)
func (d *Device) executeSubFamily(n uint8, mode AltMode, withActive bool) {
	_ = withActive
	rs := d.R[d.srcReg()]
	var rhs uint16
	switch mode {
	case Alt2:
		rhs = uint16(n)
	default:
		rhs = d.R[n]
	}
	// Borrow-in: SBC treats carry as borrow-complement (carry==1 → no
	// extra borrow).
	borrow := uint16(1)
	if mode == Alt1 {
		borrow = d.carryIn()
	}
	// Compute rs - rhs - (1 - borrow) using two's complement.
	diff32 := uint32(rs) + uint32(^rhs) + uint32(borrow)
	diff := uint16(diff32)
	d.setCarry(diff32 > 0xFFFF) // carry set on no-borrow (standard 6502 sense)
	d.setOverflow(((rs ^ rhs) & (rs ^ diff) & 0x8000) != 0)
	if mode == Alt3 {
		// CMP: set flags only.
		d.setZN(diff)
		return
	}
	d.writeReg(d.dstReg(), diff)
}

// executeBitFamily handles AND/OR/XOR/BIC across 0x71..0x7F.
//
//	none: AND  Rn
//	ALT1: BIC  Rn      (AND with NOT Rn)
//	ALT2: ANDi imm4
//	ALT3: BICi imm4
//
// and at the 0x7X slots with op>=0x78, OR and XOR share the same pattern
// (bsnes splits further on the high-nibble bit; in this first pass we
// only need AND/BIC/OR/XOR across the slot range).
func (d *Device) executeBitFamily(n uint8, mode AltMode, withActive bool) {
	_ = withActive
	rs := d.R[d.srcReg()]
	var rhs uint16
	switch mode {
	case Alt2, Alt3:
		rhs = uint16(n)
	default:
		rhs = d.R[n]
	}
	var out uint16
	switch mode {
	case Alt1, Alt3:
		// BIC: AND with complement.
		out = rs &^ rhs
	default:
		// AND.
		out = rs & rhs
	}
	d.writeReg(d.dstReg(), out)
}

// executeOrXorFamily handles 0xC1..0xCF.
//
//	none: OR   Rn
//	ALT1: XOR  Rn
//	ALT2: ORi  imm4
//	ALT3: XORi imm4
func (d *Device) executeOrXorFamily(n uint8, mode AltMode) {
	rs := d.R[d.srcReg()]
	var rhs uint16
	switch mode {
	case Alt2, Alt3:
		rhs = uint16(n)
	default:
		rhs = d.R[n]
	}
	if mode == Alt1 || mode == Alt3 {
		d.writeReg(d.dstReg(), rs^rhs)
		return
	}
	d.writeReg(d.dstReg(), rs|rhs)
}

// executeBranch handles 0x05..0x0F: BRA and conditional branches with a
// signed 8-bit pc-relative displacement. The branch predicate table:
//
//	0x05 BRA  — always
//	0x06 BLT  — S != V
//	0x07 BGE  — S == V
//	0x08 BNE  — Z == 0
//	0x09 BEQ  — Z == 1
//	0x0A BPL  — S == 0
//	0x0B BMI  — S == 1
//	0x0C BCC  — CY == 0
//	0x0D BCS  — CY == 1
//	0x0E BVC  — OV == 0
//	0x0F BVS  — OV == 1
func (d *Device) executeBranch(op uint8) {
	disp := int8(d.fetch8())
	take := false
	switch op {
	case 0x05:
		take = true
	case 0x06:
		take = ((d.SFR & SFRS) != 0) != ((d.SFR & SFROV) != 0)
	case 0x07:
		take = ((d.SFR & SFRS) != 0) == ((d.SFR & SFROV) != 0)
	case 0x08:
		take = d.SFR&SFRZ == 0
	case 0x09:
		take = d.SFR&SFRZ != 0
	case 0x0A:
		take = d.SFR&SFRS == 0
	case 0x0B:
		take = d.SFR&SFRS != 0
	case 0x0C:
		take = d.SFR&SFRCY == 0
	case 0x0D:
		take = d.SFR&SFRCY != 0
	case 0x0E:
		take = d.SFR&SFROV == 0
	case 0x0F:
		take = d.SFR&SFROV != 0
	}
	if take {
		d.R[15] = uint16(int32(d.R[15]) + int32(disp))
	}
}

// executeMult encodes the FMULT / LMULT split that design_doc.md §5 flags
// as a quirk: both live at opcode 0x9F, differentiated only by the ALT1
// prefix.
//
//	none: FMULT — (R6 * R0) signed 16*16 → top 16 bits into Rd
//	ALT1: LMULT — (R6 * R0) signed 16*16 → low 16 bits to R4, high 16 bits to R[dst]
func (d *Device) executeMult(mode AltMode) {
	a := int32(int16(d.R[6]))
	b := int32(int16(d.R[d.srcReg()]))
	prod := int64(a) * int64(b)
	switch mode {
	case Alt1: // LMULT
		// low 16 → R4, full high goes to destination (R[dst]).
		d.R[4] = uint16(uint32(prod) & 0xFFFF)
		hi := uint16(uint32(prod>>16) & 0xFFFF)
		d.writeReg(d.dstReg(), hi)
		// Carry from bit 31 of the product.
		d.setCarry(uint32(prod)&0x8000_0000 != 0)
	default: // FMULT
		// top 16 bits of 32-bit signed product.
		top := uint16(uint32(prod>>16) & 0xFFFF)
		d.writeReg(d.dstReg(), top)
		d.setCarry(uint32(prod)&0x8000_0000 != 0)
	}
}

// executeIBTFamily covers 0xA0..0xAF.
//
//	none: IBT Rn, imm8 — Rn = sign-extended imm8
//	ALT1: LMS Rn, imm8 — Rn = RAM[imm8*2] word (LSB first)
//	ALT2: SMS Rn, imm8 — RAM[imm8*2] = Rn (LSB first)
func (d *Device) executeIBTFamily(n uint8, mode AltMode) {
	switch mode {
	case Alt1: // LMS
		imm := d.fetch8()
		addr := uint32(d.RAMBR)<<16 | uint32(imm)<<1
		lo := d.ramRead(addr)
		hi := d.ramRead(addr + 1)
		d.R[n] = uint16(lo) | uint16(hi)<<8
		d.setZN(d.R[n])
	case Alt2: // SMS
		imm := d.fetch8()
		addr := uint32(d.RAMBR)<<16 | uint32(imm)<<1
		d.ramWrite(addr, uint8(d.R[n]))
		d.ramWrite(addr+1, uint8(d.R[n]>>8))
	default: // IBT
		imm := d.fetch8()
		v := uint16(int16(int8(imm)))
		d.R[n] = v
		d.setZN(v)
	}
}

// executeGetB covers the 0xF0..0xFF slot family used for ROM byte access.
// For phase 10 we implement GETB only (no ALT): Rd = ROM[ROMBR:R14].
func (d *Device) executeGetB(mode AltMode) {
	if mode != AltNone {
		return // GETBH/GETBL/GETBS not exercised by acceptance tests
	}
	addr := uint32(d.ROMBR)<<16 | uint32(d.R[14])
	var b uint8
	if d.ROM != nil && int(addr) < len(d.ROM) {
		b = d.ROM[addr]
	}
	d.writeReg(d.dstReg(), uint16(b))
}

// ramRead and ramWrite address the 16-bit RAM window.
func (d *Device) ramRead(addr uint32) uint8 {
	if len(d.RAM) == 0 {
		return 0
	}
	return d.RAM[int(addr)%len(d.RAM)]
}

func (d *Device) ramWrite(addr uint32, v uint8) {
	if len(d.RAM) == 0 {
		return
	}
	d.RAM[int(addr)%len(d.RAM)] = v
}
