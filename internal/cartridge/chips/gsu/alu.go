package gsu

// executeAddFamily handles ADD/ADC/ADDi/ADCi as a single family that
// shares opcode slots 0x50..0x5F. The "register index" encoded in the low
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

// executeSubFamily covers 0x60..0x6F. Mirrors executeAddFamily but for
// subtraction. In bsnes, 0x6C corresponds to SUB R12 / SBC R12 / SUBi 12 /
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

// executeByteMultFamily handles 0x80..0x8F.
//
//	none: MULT  Rn   signed 8x8
//	ALT1: UMULT Rn   unsigned 8x8
//	ALT2: MULT  imm4 signed 8x8
//	ALT3: UMULT imm4 unsigned 8x8
//
// Per bsnes/processor/gsu/instructions.cpp:228, MULT/UMULT in slow
// multiply mode (CFGR.MS0 = 0) pays 1 cycle if CLSR (fast clock) is
// set or 2 cycles otherwise. In fast multiply mode (CFGR.MS0 = 1)
// the multiply is free.
func (d *Device) executeByteMultFamily(n uint8, mode AltMode) {
	rs := d.R[d.srcReg()]
	var rhs uint16
	switch mode {
	case Alt2, Alt3:
		rhs = uint16(n)
	default:
		rhs = d.R[n]
	}
	var out uint16
	if mode == Alt1 || mode == Alt3 {
		out = uint16(uint8(rs)) * uint16(uint8(rhs))
	} else {
		out = uint16(int16(int8(rs)) * int16(int8(rhs)))
	}
	d.writeReg(d.dstReg(), out)
	if d.CFGR&CFGRMS0 == 0 {
		if d.CLSR&1 != 0 {
			d.advanceCycles(1)
		} else {
			d.advanceCycles(2)
		}
	}
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
		take = ((d.SFR & SFRS) != 0) == ((d.SFR & SFROV) != 0)
	case 0x07:
		take = ((d.SFR & SFRS) != 0) != ((d.SFR & SFROV) != 0)
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
		d.markR15Modified()
	}
}

// executeStoreFamily covers 0x30..0x3B.
//
//	none: STW (Rn) — RAM[Rn] = low byte of Rs, RAM[Rn^1] = high byte
//	ALT1: STB (Rn) — RAM[Rn] = low byte of Rs
func (d *Device) executeStoreFamily(n uint8, mode AltMode) {
	addr := uint32(d.RAMBR)<<16 | uint32(d.R[n])
	d.RAMAddr = d.R[n]
	v := d.R[d.srcReg()]
	d.writeRAMBuffer(uint16(addr), uint8(v))
	if mode != Alt1 && mode != Alt3 {
		d.writeRAMBuffer(d.R[n]^1, uint8(v>>8))
	}
}

// executeLoadFamily covers 0x40..0x4B.
//
//	none: LDW (Rn) — Rd = RAM[Rn] | RAM[Rn^1]<<8
//	ALT1: LDB (Rn) — Rd = RAM[Rn]
func (d *Device) executeLoadFamily(n uint8, mode AltMode) {
	addr := uint32(d.RAMBR)<<16 | uint32(d.R[n])
	d.RAMAddr = d.R[n]
	v := uint16(d.ramRead(addr))
	if mode != Alt1 && mode != Alt3 {
		v |= uint16(d.ramRead((uint32(d.RAMBR)<<16)|uint32(d.R[n]^1))) << 8
	}
	d.setReg(d.dstReg(), v)
}

// executeMult encodes the FMULT / LMULT split that design_doc.md §5 flags
// as a quirk: both live at opcode 0x9F, differentiated only by the ALT1
// prefix.
//
//	none: FMULT — (R6 * R0) signed 16*16 → top 16 bits into Rd
//	ALT1: LMULT — (R6 * R0) signed 16*16 → low 16 bits to R4, high 16 bits to R[dst]
//
// Per bsnes/processor/gsu/instructions.cpp:304, FMULT and LMULT both
// pay step((CFGR.MS0 ? 3 : 7) * (CLSR ? 1 : 2)). The penalty is
// always non-zero (no free path), unlike MULT which is free in fast
// multiply mode.
func (d *Device) executeMult(mode AltMode) {
	d.executeMultResult(mode)
	d.advanceCycles(d.multWaitCycles())
}

func (d *Device) executeMultResult(mode AltMode) {
	a := int32(int16(d.R[6]))
	b := int32(int16(d.R[d.srcReg()]))
	prod := int64(a) * int64(b)
	switch mode {
	case Alt1: // LMULT
		// low 16 → R4, full high goes to destination (R[dst]).
		d.R[4] = uint16(uint32(prod) & 0xFFFF)
		hi := uint16(uint32(prod>>16) & 0xFFFF)
		d.writeReg(d.dstReg(), hi)
		d.setCarry(uint32(prod)&0x0000_8000 != 0)
	default: // FMULT
		// top 16 bits of 32-bit signed product.
		top := uint16(uint32(prod>>16) & 0xFFFF)
		d.writeReg(d.dstReg(), top)
		d.setCarry(uint32(prod)&0x0000_8000 != 0)
	}
}

func (d *Device) multWaitCycles() uint64 {
	var msFactor uint64 = 7
	if d.CFGR&CFGRMS0 != 0 {
		msFactor = 3
	}
	var clsrFactor uint64 = 2
	if d.CLSR&1 != 0 {
		clsrFactor = 1
	}
	return msFactor * clsrFactor
}

// executeIBTFamily covers 0xA0..0xAF.
//
//	none: IBT Rn, imm8 — Rn = sign-extended imm8
//	ALT1: LMS Rn, imm8 — Rn = RAM[imm8*2] word (LSB first)
//	ALT2: SMS Rn, imm8 — RAM[imm8*2] = Rn (LSB first)
func (d *Device) executeIBTFamily(n uint8, mode AltMode) {
	switch mode {
	case Alt1, Alt3: // LMS
		imm := d.fetch8()
		d.RAMAddr = uint16(imm) << 1
		addr := uint32(d.RAMBR)<<16 | uint32(d.RAMAddr)
		lo := d.ramRead(addr)
		hi := d.ramRead(addr + 1)
		d.setReg(n, uint16(lo)|uint16(hi)<<8)
	case Alt2: // SMS
		imm := d.fetch8()
		d.RAMAddr = uint16(imm) << 1
		addr := uint32(d.RAMBR)<<16 | uint32(d.RAMAddr)
		d.writeRAMBuffer(uint16(addr), uint8(d.R[n]))
		d.writeRAMBuffer(d.RAMAddr+1, uint8(d.R[n]>>8))
	default: // IBT
		imm := d.fetch8()
		v := uint16(int16(int8(imm)))
		d.setReg(n, v)
	}
}

// executeGetC covers 0xDF.
//
//	none, ALT1: GETC — COLR = ROM[ROMBR:R14]
//	ALT2:       RAMB — RAMBR = Rs & 1
//	ALT3:       ROMB — ROMBR = Rs & 0x7f
func (d *Device) executeGetC(mode AltMode) {
	switch mode {
	case Alt2:
		d.syncRAMBuffer()
		d.RAMBR = uint8(d.R[d.srcReg()] & 1)
	case Alt3:
		d.syncROMBuffer()
		d.ROMBR = uint8(d.R[d.srcReg()] & 0x7F)
	default:
		d.COLR = d.color(d.romRead())
	}
}

// executeGetB covers 0xEF ROM byte access.
//
//	none: GETB  — Rd = ROM[ROMBR:R14]
//	ALT1: GETBH — Rd = ROM[ROMBR:R14]<<8 | byte(Rs)
//	ALT2: GETBL — Rd = Rs&0xff00 | ROM[ROMBR:R14]
//	ALT3: GETBS — Rd = sign_extend(ROM[ROMBR:R14])
func (d *Device) executeGetB(mode AltMode) {
	b := uint16(d.romRead())
	var v uint16
	switch mode {
	case Alt1:
		v = b<<8 | (d.R[d.srcReg()] & 0x00FF)
	case Alt2:
		v = (d.R[d.srcReg()] & 0xFF00) | b
	case Alt3:
		v = uint16(int16(int8(b)))
	default:
		v = b
	}
	d.setReg(d.dstReg(), v)
}

// executeIWTFamily covers 0xF0..0xFF.
//
//	none: IWT Rn,#xx — Rn = little-endian immediate word
//	ALT1: LM  Rn,(xx) — Rn = RAM[xx] | RAM[xx^1]<<8
//	ALT2: SM  (xx),Rn — RAM[xx] = low byte, RAM[xx^1] = high byte
//	ALT3: LM  Rn,(xx)
func (d *Device) executeIWTFamily(n uint8, mode AltMode) {
	switch mode {
	case Alt1, Alt3: // LM
		d.RAMAddr = d.fetch16()
		addr := uint32(d.RAMBR)<<16 | uint32(d.RAMAddr)
		lo := d.ramRead(addr)
		hi := d.ramRead((uint32(d.RAMBR) << 16) | uint32(uint16(addr)^1))
		d.setReg(n, uint16(lo)|uint16(hi)<<8)
	case Alt2: // SM
		d.RAMAddr = d.fetch16()
		addr := uint32(d.RAMBR)<<16 | uint32(d.RAMAddr)
		d.writeRAMBuffer(uint16(addr), uint8(d.R[n]))
		d.writeRAMBuffer(uint16(addr)^1, uint8(d.R[n]>>8))
	default:
		d.setReg(n, d.fetch16())
	}
}

// ramRead and ramWrite address the 16-bit RAM window. They are the
// shared bus primitive matching bsnes/sfc/coprocessor/superfx/memory.cpp:20-26
// SuperFX::read for the $60-7F:0000-FFFF window: when SCMR.RAN is set
// (GSU owns RAM), the access is 0 cycles beyond any pending sync. The
// `while(!regs.scmr.ran) step(6)` ownership-spin in bsnes is only paid
// when the bus is contested. Callers that need the per-byte 6 cy step
// matching bsnes pixel-path semantics (core.cpp:64-68 rpix, core.cpp:88-100
// flushPixelCache) must call stepBusWait() explicitly before each access.
func (d *Device) ramRead(addr uint32) uint8 {
	d.syncRAMBuffer()
	if len(d.RAM) == 0 {
		if d.RAMReadHook != nil {
			d.RAMReadHook(addr, 0)
		}
		return 0
	}
	v := d.RAM[int(addr)%len(d.RAM)]
	if d.RAMReadHook != nil {
		d.RAMReadHook(addr, v)
	}
	return v
}

func (d *Device) ramWrite(addr uint32, v uint8) {
	d.syncRAMBuffer()
	if len(d.RAM) == 0 {
		return
	}
	d.RAM[int(addr)%len(d.RAM)] = v
}

func (d *Device) writeRAMBuffer(addr uint16, v uint8) {
	d.writeRAMBufferBank(d.RAMBR, addr, v)
}

func (d *Device) writeRAMBufferBank(bank uint8, addr uint16, v uint8) {
	d.syncRAMBuffer()
	d.ramPending = true
	d.ramDelay = d.busWaitCycles()
	d.ramBank = bank
	d.ramAddr = addr
	d.ramData = v
}

func (d *Device) syncRAMBuffer() {
	if d.ramPending {
		d.advanceCycles(d.ramDelay)
	}
}

func (d *Device) commitRAMBuffer() {
	if !d.ramPending {
		return
	}
	d.ramPending = false
	if len(d.RAM) == 0 {
		return
	}
	addr := uint32(d.ramBank)<<16 | uint32(d.ramAddr)
	d.RAM[int(addr)%len(d.RAM)] = d.ramData
}

// romRead is the shared bus primitive matching bsnes' SuperFX::read for
// the GSU's ROM windows ($00-3F LoROM-fold and $40-5F linear, see
// memory.cpp:1-30). When SCMR.RON is set (GSU owns ROM), the access is
// 0 cycles beyond any pending romcl sync. The unbuffered fallback path
// (when no prior R14 write triggered updateROMBuffer) returns the byte
// without an extra bus-wait, matching bsnes which always routes ROM
// reads through readROMBuffer / read() (timing.cpp:25-28 + memory.cpp:1-9).
func (d *Device) romRead() uint8 {
	if d.romPending {
		d.syncROMBuffer()
		if d.ROMReadHook != nil {
			d.ROMReadHook(uint32(d.ROMBR)<<16|uint32(d.R[14]), d.romData)
		}
		return d.romData
	}
	addr := uint32(d.ROMBR)<<16 | uint32(d.R[14])
	v := d.romAt(addr)
	if d.ROMReadHook != nil {
		d.ROMReadHook(addr, v)
	}
	return v
}

func (d *Device) updateROMBuffer() {
	d.SFR |= SFRR
	d.romPending = true
	d.romDelay = d.busWaitCycles()
}

func (d *Device) syncROMBuffer() {
	if d.romPending {
		d.advanceCycles(d.romDelay)
	}
}

func (d *Device) commitROMBuffer() {
	if !d.romPending {
		return
	}
	d.romPending = false
	d.SFR &^= SFRR
	addr := uint32(d.ROMBR)<<16 | uint32(d.R[14])
	d.romData = d.romAt(addr)
}

func (d *Device) romAt(addr uint32) uint8 {
	// Per bsnes/sfc/coprocessor/superfx/memory.cpp:1-30 SuperFX::read,
	// the GSU's opcode/data bus has three windows:
	//   $00-3F:0000-FFFF -> ROM, ((bank<<15)|(off & $7FFF)) & romMask
	//   $40-5F:0000-FFFF -> ROM, addr & romMask
	//   $60-7F:0000-FFFF -> shared RAM, addr & ramMask
	// The third window is reachable via PBR (opcode fetch in
	// readOpcode, memory.cpp:60-70) or ROMBR (GETB-family data fetch);
	// both registers are masked & 0x7F so $60-$7F is in range.
	switch {
	case addr&0xC00000 == 0x000000:
		addr = ((addr & 0x3F0000) >> 1) | (addr & 0x7FFF)
	case addr&0xE00000 == 0x400000:
		addr = addr & 0x3FFFFF
	case addr&0xE00000 == 0x600000:
		if len(d.RAM) == 0 {
			return 0
		}
		return d.RAM[int(addr)%len(d.RAM)]
	}
	if d.ROM == nil || int(addr) >= len(d.ROM) {
		return 0
	}
	return d.ROM[addr]
}
