package gsu

// GSU opcode implementation. The dispatch loop is intentionally a simple
// switch: the instruction set is small and branch-prediction-friendly, and
// bsnes itself uses a similar switch layout (sfc/coprocessor/superfx/).
//
// Prefix bytes (ALT1/ALT2/ALT3, TO, FROM, WITH) only mutate the SFR or
// target-register fields on the Device; they do not advance any other
// state. The next non-prefix byte consumes the prefix and clears it.

// peekpipe returns the byte currently held in the prefetch pipeline
// (the opcode that retires this step) and refills the pipeline by
// reading at the current R15 without incrementing. It also clears
// r15Modified, mirroring bsnes/sfc/coprocessor/superfx/memory.cpp:73-79
// (regs.r[15].modified = false). Used at instruction-dispatch entry.
func (d *Device) peekpipe() uint8 {
	b := d.Pipeline
	d.Pipeline = d.readOpcode(d.R[15])
	d.r15Modified = false
	return b
}

// fetch8 returns the byte currently held in the pipeline (the next
// operand byte the handler is about to consume) and refills the
// pipeline at the *next* address (pre-increment R15). Mirrors bsnes
// pipe() at sfc/coprocessor/superfx/memory.cpp:80-85. The pipeline
// model means: each pipe() returns the byte that was prefetched on
// the prior step (or the prior pipe() call), then loads the byte
// that follows. Branches/jumps that subsequently overwrite R15 do
// NOT flush the pipeline; the byte loaded here will be the stale
// byte executed at the start of the next step.
func (d *Device) fetch8() uint8 {
	b := d.Pipeline
	d.R[15]++
	d.Pipeline = d.readOpcode(d.R[15])
	d.r15Modified = false
	return b
}

// fetch16 reads a little-endian 16-bit immediate via two fetch8/pipe
// calls (matching bsnes IWT/IBT operand-fetch order).
func (d *Device) fetch16() uint16 {
	lo := uint16(d.fetch8())
	hi := uint16(d.fetch8())
	return lo | hi<<8
}

// markR15Modified records that a handler explicitly wrote R15 by means
// other than the auto-post-increment in stepOne. Branches/JMP/LJMP/
// LOOP/IWT R15 set this, and stepOne consults it to decide whether to
// post-increment. The pipeline byte is NOT touched here; that's the
// whole point of the SuperFX pipeline model — the stale byte already
// loaded into Pipeline retires on the next step.
func (d *Device) markR15Modified() { d.r15Modified = true }

func (d *Device) readOpcode(addr uint16) uint8 {
	offset := uint16(addr - d.CBR)
	if offset < 512 {
		line := offset >> 4
		if !d.cacheValid[line] {
			dp := offset & 0xFFF0
			sp := ((uint32(d.PBR) << 16) | uint32((d.CBR+dp)&0xFFF0))
			for i := uint16(0); i < 16; i++ {
				d.stepBusWait()
				d.Cache[dp+i] = d.romAt(sp + uint32(i))
			}
			d.cacheValid[line] = true
		} else {
			d.stepCacheWait()
		}
		return d.Cache[offset]
	}
	d.stepBusWait()
	return d.romAt((uint32(d.PBR) << 16) | uint32(addr))
}

func (d *Device) nextOpcodeFetchCycles() uint64 {
	return d.opcodeFetchCycles(d.R[15])
}

func (d *Device) opcodeFetchCycles(addr uint16) uint64 {
	offset := uint16(addr - d.CBR)
	if offset >= 512 {
		return d.busWaitCycles()
	}
	if d.cacheValid[offset>>4] {
		if d.CLSR&1 != 0 {
			return 1
		}
		return 2
	}
	return 16 * d.busWaitCycles()
}

func (d *Device) busWaitCycles() uint64 {
	if d.CLSR&1 != 0 {
		return 5
	}
	return 6
}

func (d *Device) stepBusWait() {
	d.advanceCycles(d.busWaitCycles())
}

func (d *Device) stepCacheWait() {
	if d.CLSR&1 != 0 {
		d.advanceCycles(1)
		return
	}
	d.advanceCycles(2)
}

func (d *Device) advanceCycles(n uint64) {
	d.cycles += n
	if d.romPending {
		if n < d.romDelay {
			d.romDelay -= n
		} else {
			d.romDelay = 0
			d.commitROMBuffer()
		}
	}
	if d.ramPending {
		if n < d.ramDelay {
			d.ramDelay -= n
		} else {
			d.ramDelay = 0
			d.commitRAMBuffer()
		}
	}
	if d.CycleHook != nil {
		d.CycleHook(n, d.cycles)
	}
}

func (d *Device) flushCache() {
	clear(d.cacheValid[:])
	clear(d.Cache[:])
}

func (d *Device) readCache(addr uint16) uint8 {
	return d.Cache[(addr+d.CBR)&0x01FF]
}

func (d *Device) writeCache(addr uint16, v uint8) {
	off := (addr + d.CBR) & 0x01FF
	d.Cache[off] = v
	if off&0x000F == 0x000F {
		d.cacheValid[off>>4] = true
	}
}

// srcReg returns the register index to read from for the next instruction.
// With FROM / WITH / MOVE this is overridden; otherwise it is the opcode's
// implicit nibble (0..15).
func (d *Device) srcReg() uint8 {
	if d.withPrefix || d.fromPrefix || d.toPrefix {
		return d.SREG
	}
	return 0 // Accumulator defaults to R0
}

// dstReg returns the register index for the result of an ALU op.
func (d *Device) dstReg() uint8 {
	if d.toPrefix || d.withPrefix {
		return d.DREG
	}
	return 0 // Default target: R0
}

// writeReg stores v into the destination register and latches flags. Also
// consumes the prefix bits that modify the next opcode.
func (d *Device) writeReg(idx uint8, v uint16) {
	d.setReg(idx, v)
	d.setZN(v)
}

func (d *Device) setReg(idx uint8, v uint16) {
	idx &= 0x0F
	d.R[idx] = v
	if idx == 14 {
		d.updateROMBuffer()
	}
	if idx == 15 {
		// Mirrors bsnes regs.r[15].modified being set by the Reg
		// proxy on any assignment. stepOne's post-instruction ++
		// is suppressed when this is set, and the prefetched
		// pipeline byte is retained (NOT flushed), so the next
		// step retires the stale byte.
		d.r15Modified = true
	}
}

// stepOne executes a single instruction. It returns false if SFR.G is
// clear at entry, signaling the caller to stop the catch-up loop.
func (d *Device) stepOne() bool {
	if !d.Running() {
		return false
	}

	// SuperFX pipeline model (bsnes/sfc/coprocessor/superfx/superfx.cpp:25-50):
	//   preR15  = R15 at entry
	//   opcode  = peekpipe()  // returns regs.pipeline (set on PRIOR step),
	//                            then refills regs.pipeline = readOpcode(R15)
	//   instruction(opcode)
	//   if regs.r[15].modified: clear modified
	//   else: R15++
	//
	// The opcode that retires THIS step is the byte that was prefetched
	// during the prior step (or cold-reset $01). The byte at R15 just
	// got loaded into Pipeline by peekpipe and will retire NEXT step —
	// unless an operand fetch (pipe()) or an R15 jump moves things first.
	pbr, pc := d.PBR, d.R[15]
	if d.TraceHookEx != nil {
		// Pre-peekpipe snapshot: cycles BEFORE this step's prefetch
		// refill. op is the byte already in the pipeline (= the byte
		// peekpipe is about to return).
		d.TraceHookEx(TracePhasePrePeek, pbr, pc, d.Pipeline, d.cycles)
	}
	op := d.peekpipe()
	if d.TraceHook != nil {
		d.TraceHook(pbr, pc, op)
	}
	if d.TraceHookEx != nil {
		// Post-peekpipe snapshot: cycles include this step's prefetch
		// refill (cache-hit ~2cy or cache-miss ~96cy fill). The handler
		// has not yet run.
		d.TraceHookEx(TracePhasePostPeek, pbr, pc, op, d.cycles)
	}

	// Apply the post-step R15 advance regardless of which dispatch path
	// (including prefix early-returns) below taken. Mirrors bsnes
	// SuperFX::main()'s unconditional post-instruction block:
	//   if(regs.r[15].modified) regs.r[15].modified = false;
	//   else regs.r[15]++;
	// Deferred so prefix cases that `return true` early still get this.
	defer func() {
		if d.r15Modified {
			d.r15Modified = false
		} else {
			d.R[15]++
		}
	}()

	// Prefix handling. Prefix bytes are not real instructions; they set
	// state and return immediately so the *next* byte is dispatched.
	switch {
	case op == 0x3D: // ALT1
		d.SFR = (d.SFR &^ (SFRALT1 | SFRALT2 | SFRB)) | SFRALT1
		return true
	case op == 0x3E: // ALT2
		d.SFR = (d.SFR &^ (SFRALT1 | SFRALT2 | SFRB)) | SFRALT2
		return true
	case op == 0x3F: // ALT3
		d.SFR = (d.SFR &^ SFRB) | SFRALT1 | SFRALT2
		return true
	case op >= 0xB0 && op <= 0xBF: // FROM Rn
		if d.withPrefix {
			d.setReg(d.dstReg(), d.R[op&0x0F])
			d.setOverflow(d.R[d.dstReg()]&0x0080 != 0)
			d.setZN(d.R[d.dstReg()])
			d.consumePrefixes()
			return true
		}
		d.SREG = op & 0x0F
		d.fromPrefix = true
		return true
	case op >= 0x10 && op <= 0x1F: // TO Rn
		if d.withPrefix {
			d.setReg(op&0x0F, d.R[d.srcReg()])
			d.consumePrefixes()
			return true
		}
		d.DREG = op & 0x0F
		d.toPrefix = true
		return true
	case op >= 0x20 && op <= 0x2F: // WITH Rn (Rn = Rn op Rs; SREG=DREG=n)
		d.SREG = op & 0x0F
		d.DREG = op & 0x0F
		d.withPrefix = true
		d.withReg = op & 0x0F
		d.SFR |= SFRB
		return true
	}

	// Non-prefix opcode. Remember the decoded prefixes for this
	// instruction before clearing them.
	mode := d.alt()
	withActive := d.withPrefix

	switch {
	case op == 0x00: // STOP
		d.Stop()
		d.resetPrefixes()
	case op == 0x01: // NOP
	case op == 0x02: // CACHE
		cbr := d.R[15] & 0xFFF0
		if d.CBR != cbr {
			d.CBR = cbr
			d.flushCache()
		}
	case op == 0x03: // LSR
		s := d.R[d.srcReg()]
		d.setCarry(s&1 != 0)
		d.writeReg(d.dstReg(), s>>1)
	case op == 0x04: // ROL
		s := d.R[d.srcReg()]
		c := d.carryIn()
		d.setCarry(s&0x8000 != 0)
		d.writeReg(d.dstReg(), s<<1|c)
	case op == 0x3C: // LOOP — R12 is counter, R13 is branch target
		d.R[12]--
		d.setZN(d.R[12])
		if d.R[12] != 0 {
			d.R[15] = d.R[13]
			d.markR15Modified()
		}
	case op >= 0x05 && op <= 0x0F: // BRA/Bcc with 8-bit signed offset
		d.executeBranch(op)
	case op >= 0x30 && op <= 0x3B: // STW/STB
		d.executeStoreFamily(op&0x0F, mode)
	case op >= 0x40 && op <= 0x4B: // LDW/LDB
		d.executeLoadFamily(op&0x0F, mode)
	case op >= 0x50 && op <= 0x5F: // ADD Rn (or ADC if ALT1, ADDi imm if ALT2, ADCi imm if ALT3)
		d.executeAddFamily(op&0x0F, mode, withActive)
	case op >= 0x60 && op <= 0x6F: // SUB Rn / SBC / SUBi / CMP
		d.executeSubFamily(op&0x0F, mode, withActive)
	case op == 0x70: // MERGE
		hi := d.R[7] & 0xFF00
		lo := (d.R[8] & 0xFF00) >> 8
		v := hi | lo
		d.setReg(d.dstReg(), v)
		d.setOverflow(v&0xC0C0 != 0)
		if v&0x8080 != 0 {
			d.SFR |= SFRS
		} else {
			d.SFR &^= SFRS
		}
		d.setCarry(v&0xE0E0 != 0)
		if v&0xF0F0 != 0 {
			d.SFR |= SFRZ
		} else {
			d.SFR &^= SFRZ
		}
	case op >= 0x71 && op <= 0x7F: // AND Rn / OR Rn via ALT / BIC / XOR
		d.executeBitFamily(op&0x0F, mode, withActive)
	case op >= 0x80 && op <= 0x8F: // MULT/UMULT
		d.executeByteMultFamily(op&0x0F, mode)
	case op == 0x90: // SBK — store source word through the RAM write buffer
		v := d.R[d.srcReg()]
		d.writeRAMBuffer(d.RAMAddr^0, uint8(v))
		d.writeRAMBuffer(d.RAMAddr^1, uint8(v>>8))
	case op >= 0x91 && op <= 0x94: // LINK — R11 = R15 + n
		n := uint16(op - 0x90)
		d.R[11] = d.R[15] + n
	case op == 0x95: // SEX — sign-extend R0 from byte to word
		v := d.R[d.srcReg()] & 0x00FF
		if v&0x80 != 0 {
			v |= 0xFF00
		}
		d.writeReg(d.dstReg(), v)
	case op == 0x96: // ASR / DIV2 if ALT1
		s := d.R[d.srcReg()]
		d.setCarry(s&1 != 0)
		if mode == Alt1 {
			// DIV2: signed /2, rounds toward negative infinity except
			// -1 which rounds to 0 (bsnes plot/alu.cpp).
			ss := int16(s)
			if ss == -1 {
				d.writeReg(d.dstReg(), 0)
			} else {
				d.writeReg(d.dstReg(), uint16(ss>>1))
			}
		} else {
			// ASR: arithmetic right shift by 1.
			d.writeReg(d.dstReg(), uint16(int16(s)>>1))
		}
	case op == 0x97: // ROR
		c := d.carryIn()
		s := d.R[d.srcReg()]
		d.setCarry(s&1 != 0)
		d.writeReg(d.dstReg(), (s>>1)|(c<<15))
	case op >= 0x98 && op <= 0x9D: // JMP/LJMP Rn (98-9D) -- PC = Rn
		// Per ares ares/component/processor/gsu/instruction.cpp:74,
		// the op6(0x98, JMP_LJMP) macro dispatches with the LOW NIBBLE
		// of the opcode (`(n4)opcode`), not `opcode - 0x98`. So $9B
		// targets R[0xB]=R[11], not R[3]. Same in
		// bsnes/processor/gsu/instruction.cpp. Pre-Goal-51 Go used
		// op-0x98 which mapped $98..$9D to R0..R5; that produced
		// wrong jump targets.
		n := op & 0x0F
		if mode == Alt1 {
			d.PBR = uint8(d.R[n] & 0x007F)
			d.R[15] = d.R[d.srcReg()]
			d.CBR = d.R[15] & 0xFFF0
			d.flushCache()
		} else {
			d.R[15] = d.R[n]
		}
		d.markR15Modified()
	case op == 0x9E: // LOB — low byte of Rs, zero-extend
		v := d.R[d.srcReg()] & 0x00FF
		d.setReg(d.dstReg(), v)
		d.setByteZN(v)
	case op == 0x9F: // FMULT / LMULT (ALT1)
		d.executeMult(mode)
	case op >= 0xA0 && op <= 0xAF: // IBT imm / LMS (ALT1) / SMS (ALT2)
		d.executeIBTFamily(op&0x0F, mode)
	case op >= 0xC0 && op <= 0xCF: // HIB / OR / XOR
		if op == 0xC0 { // HIB
			v := (d.R[d.srcReg()] & 0xFF00) >> 8
			d.setReg(d.dstReg(), v)
			d.setByteZN(v)
		} else if op >= 0xC1 {
			d.executeOrXorFamily(op&0x0F, mode)
		}
	case op >= 0xD0 && op <= 0xDE: // INC Rn
		n := op & 0x0F
		d.setReg(n, d.R[n]+1)
		d.setZN(d.R[n])
	case op == 0xDF: // GETC/RAMB/ROMB
		d.executeGetC(mode)
	case op >= 0xE0 && op <= 0xEE: // DEC Rn
		n := op & 0x0F
		d.setReg(n, d.R[n]-1)
		d.setZN(d.R[n])
	case op == 0xEF: // GETB/GETBH/GETBL/GETBS (ALT variants)
		d.executeGetB(mode)
	case op >= 0xF0 && op <= 0xFF: // IWT/LM/SM
		d.executeIWTFamily(op&0x0F, mode)
	}

	// Dedicated pixel and single-slot operations.
	switch op {
	case 0x2C: // PLOT (no ALT) / RPIX (ALT1)
		// 0x2C falls in the WITH-prefix range above, so it's unreachable
		// here if we entered via that prefix. The executable PLOT/RPIX
		// opcode is 0x4C below.
	case 0x4D: // SWAP
		v := d.R[d.srcReg()]
		d.writeReg(d.dstReg(), v>>8|v<<8)
	case 0x4E: // COLOR / CMODE (ALT1)
		if mode == Alt1 {
			d.POR = uint8(d.R[d.srcReg()])
		} else {
			d.COLR = d.color(uint8(d.R[d.srcReg()]))
		}
	case 0x4F: // NOT
		d.writeReg(d.dstReg(), ^d.R[d.srcReg()])
	case 0x4C: // PLOT / RPIX (ALT1)
		if mode == Alt1 {
			// RPIX: return pixel at (R1,R2).
			d.writeReg(d.dstReg(), uint16(d.rpix(d.R[1], d.R[2])))
		} else {
			// PLOT: plot current colour at (R1,R2); increment R1.
			d.plot(d.R[1], d.R[2], d.COLR)
			d.R[1]++
		}
	}

	// Consume prefix state for the instruction just executed.
	d.consumePrefixes()
	// Post-step R15 advance is applied by the deferred block at top.
	return true
}

// Run executes up to n instructions or until SFR.G clears.
func (d *Device) Run(n int) int {
	executed := 0
	for executed < n && d.Running() {
		if d.stepSlice.Active {
			if d.advanceStepSliceFrame(^uint64(0)) == 0 {
				break
			}
			executed++
			continue
		}
		if !d.stepOne() {
			break
		}
		executed++
	}
	return executed
}
