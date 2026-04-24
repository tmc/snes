package updsp

// execLD implements the LD-class instruction.
//
// Encoding (24-bit opcode, class bits 23-22 = 11):
//
//	bits 21-6: 16-bit immediate
//	bits 3-0:  destination register selector
//
// bsnes upd96050/instructions.cpp documents the destination table; DSP-1
// programs write to TR, DP, RP, K, L, and DR.
func (c *Core) execLD(word uint32) {
	imm := uint16((word >> 6) & 0xFFFF)
	dst := uint8(word & 0x0F)
	c.writeReg(dst, imm)
}

// execJP implements the JP-class instruction. The field layout matches the
// bsnes uPD96050 core (processor/upd96050/instructions.cpp execJP):
//
//	brch = (opcode >> 13) & 0x1FF  // 9-bit branch/condition selector
//	na   = (opcode >>  2) & 0x7FF  // 11-bit next-address field
//	bank = (opcode >>  0) & 0x3    // 2-bit bank select
//
// The target PC is composed from the current PC's bank bit (PC bit 13 preserved),
// the branch-encoded bank bits, and na. For L-prefixed opcodes the bank bit is
// forced low; for H-prefixed it is forced high.
func (c *Core) execJP(word uint32) {
	brch := uint16((word >> 13) & 0x1FF)
	na := uint16((word >> 2) & 0x7FF)
	bank := uint16(word & 0x3)

	// jp holds the composed 14-bit target address (our PRG is 2048 words, so
	// only the low 11 bits matter in practice, but we track bit 13 for
	// LJMP/HJMP parity).
	jp := (c.PC & 0x2000) | (bank << 11) | na

	switch brch {
	case 0x000:
		// JMPSO: jump to address held in SO (serial-out register).
		c.PC = c.SO & 0x07FF
		return

	case 0x080: // JNCA: branch if flag A.C == 0
		if !c.FA.C {
			c.PC = jp & 0x07FF
		}
	case 0x082: // JCA
		if c.FA.C {
			c.PC = jp & 0x07FF
		}
	case 0x084: // JNCB
		if !c.FB.C {
			c.PC = jp & 0x07FF
		}
	case 0x086: // JCB
		if c.FB.C {
			c.PC = jp & 0x07FF
		}

	case 0x088: // JNZA
		if !c.FA.Z {
			c.PC = jp & 0x07FF
		}
	case 0x08a: // JZA
		if c.FA.Z {
			c.PC = jp & 0x07FF
		}
	case 0x08c: // JNZB
		if !c.FB.Z {
			c.PC = jp & 0x07FF
		}
	case 0x08e: // JZB
		if c.FB.Z {
			c.PC = jp & 0x07FF
		}

	case 0x090: // JNOVA0
		if !c.FA.OV0 {
			c.PC = jp & 0x07FF
		}
	case 0x092: // JOVA0
		if c.FA.OV0 {
			c.PC = jp & 0x07FF
		}
	case 0x094: // JNOVB0
		if !c.FB.OV0 {
			c.PC = jp & 0x07FF
		}
	case 0x096: // JOVB0
		if c.FB.OV0 {
			c.PC = jp & 0x07FF
		}

	case 0x098: // JNOVA1
		if !c.FA.OV1 {
			c.PC = jp & 0x07FF
		}
	case 0x09a: // JOVA1
		if c.FA.OV1 {
			c.PC = jp & 0x07FF
		}
	case 0x09c: // JNOVB1
		if !c.FB.OV1 {
			c.PC = jp & 0x07FF
		}
	case 0x09e: // JOVB1
		if c.FB.OV1 {
			c.PC = jp & 0x07FF
		}

	case 0x0a0: // JNSA0
		if !c.FA.S0 {
			c.PC = jp & 0x07FF
		}
	case 0x0a2: // JSA0
		if c.FA.S0 {
			c.PC = jp & 0x07FF
		}
	case 0x0a4: // JNSB0
		if !c.FB.S0 {
			c.PC = jp & 0x07FF
		}
	case 0x0a6: // JSB0
		if c.FB.S0 {
			c.PC = jp & 0x07FF
		}

	case 0x0a8: // JNSA1
		if !c.FA.S1 {
			c.PC = jp & 0x07FF
		}
	case 0x0aa: // JSA1
		if c.FA.S1 {
			c.PC = jp & 0x07FF
		}
	case 0x0ac: // JNSB1
		if !c.FB.S1 {
			c.PC = jp & 0x07FF
		}
	case 0x0ae: // JSB1
		if c.FB.S1 {
			c.PC = jp & 0x07FF
		}

	case 0x0b0: // JDPL0: branch if DP low nibble == 0
		if (c.DP & 0x0F) == 0x00 {
			c.PC = jp & 0x07FF
		}
	case 0x0b1: // JDPLN0
		if (c.DP & 0x0F) != 0x00 {
			c.PC = jp & 0x07FF
		}
	case 0x0b2: // JDPLF: branch if DP low nibble == 0xF
		if (c.DP & 0x0F) == 0x0F {
			c.PC = jp & 0x07FF
		}
	case 0x0b3: // JDPLNF
		if (c.DP & 0x0F) != 0x0F {
			c.PC = jp & 0x07FF
		}

	// Serial ack tests: SNES DSP programs never drive SIACK/SOACK; always 0.
	case 0x0b4: // JNSIAK
		c.PC = jp & 0x07FF
	case 0x0b6: // JSIAK
		// never taken
	case 0x0b8: // JNSOAK
		c.PC = jp & 0x07FF
	case 0x0ba: // JSOAK
		// never taken

	case 0x0bc: // JNRQM: branch if SR.RQM == 0
		if c.SR&srRQM == 0 {
			c.PC = jp & 0x07FF
		}
	case 0x0be: // JRQM
		if c.SR&srRQM != 0 {
			c.PC = jp & 0x07FF
		}

	case 0x100: // LJMP: unconditional jump, force bank bit 13 low
		c.PC = (jp &^ 0x2000) & 0x07FF
	case 0x101: // HJMP: unconditional jump, force bank bit 13 high
		c.PC = (jp | 0x2000) & 0x07FF

	case 0x140: // LCALL
		c.pushStack((jp &^ 0x2000) & 0x07FF)
	case 0x141: // HCALL
		c.pushStack((jp | 0x2000) & 0x07FF)
	}
}

// evalCondition tests a branch predicate against the core. It is kept as a
// standalone helper so the decode table can be exercised by unit tests
// independently of PC-target composition.
//
// cond is the 9-bit brch field (opcode >> 13), matching the bsnes uPD96050
// switch(brch) table. Unknown codes return false so a spurious branch opcode
// never silently diverts PC.
func evalCondition(cond uint16, c *Core) bool {
	switch cond {
	case 0x080: // JNCA
		return !c.FA.C
	case 0x082: // JCA
		return c.FA.C
	case 0x084: // JNCB
		return !c.FB.C
	case 0x086: // JCB
		return c.FB.C

	case 0x088: // JNZA
		return !c.FA.Z
	case 0x08a: // JZA
		return c.FA.Z
	case 0x08c: // JNZB
		return !c.FB.Z
	case 0x08e: // JZB
		return c.FB.Z

	case 0x090: // JNOVA0
		return !c.FA.OV0
	case 0x092: // JOVA0
		return c.FA.OV0
	case 0x094: // JNOVB0
		return !c.FB.OV0
	case 0x096: // JOVB0
		return c.FB.OV0

	case 0x098: // JNOVA1
		return !c.FA.OV1
	case 0x09a: // JOVA1
		return c.FA.OV1
	case 0x09c: // JNOVB1
		return !c.FB.OV1
	case 0x09e: // JOVB1
		return c.FB.OV1

	case 0x0a0: // JNSA0
		return !c.FA.S0
	case 0x0a2: // JSA0
		return c.FA.S0
	case 0x0a4: // JNSB0
		return !c.FB.S0
	case 0x0a6: // JSB0
		return c.FB.S0

	case 0x0a8: // JNSA1
		return !c.FA.S1
	case 0x0aa: // JSA1
		return c.FA.S1
	case 0x0ac: // JNSB1
		return !c.FB.S1
	case 0x0ae: // JSB1
		return c.FB.S1

	case 0x0b0: // JDPL0
		return c.DP&0x0F == 0x00
	case 0x0b1: // JDPLN0
		return c.DP&0x0F != 0x00
	case 0x0b2: // JDPLF
		return c.DP&0x0F == 0x0F
	case 0x0b3: // JDPLNF
		return c.DP&0x0F != 0x0F

	case 0x0b4: // JNSIAK — SIACK stays 0 on SNES boards
		return true
	case 0x0b6: // JSIAK
		return false
	case 0x0b8: // JNSOAK
		return true
	case 0x0ba: // JSOAK
		return false

	case 0x0bc: // JNRQM
		return c.SR&srRQM == 0
	case 0x0be: // JRQM
		return c.SR&srRQM != 0

	case 0x100, 0x101, 0x140, 0x141: // LJMP/HJMP/LCALL/HCALL — unconditional
		return true
	case 0x000: // JMPSO — unconditional
		return true
	}
	return false
}

// movRegToReg copies a DSP register from src to dst. This is used by OP-class
// instructions that encode an implicit MOV alongside the ALU op. DSP-1 games
// use this heavily to shuffle arguments between K/L/A/B and DR.
func (c *Core) movRegToReg(src, dst uint8) {
	val := c.readReg(src)
	c.writeReg(dst, val)
}

// readReg returns the 16-bit value of the named register.
func (c *Core) readReg(r uint8) uint16 {
	switch r {
	case regA:
		return uint16(c.A)
	case regB:
		return uint16(c.B)
	case regTR:
		return c.TR
	case regDP:
		return uint16(c.DP)
	case regRP:
		return c.RP
	case regRO:
		return c.DROM[c.RP&0x3FF]
	case regSGN:
		// Signed-magnitude sign helper: returns +/-0x7FFF based on A's sign.
		if c.A&0x8000 != 0 {
			return 0x8000
		}
		return 0x7FFF
	case regDR:
		return c.DR
	case regDRNF:
		return c.DR
	case regSR:
		return c.SR
	case regSIM:
		return c.SI
	case regSIL:
		return c.SI
	case regK:
		return uint16(c.K)
	case regL:
		return uint16(c.L)
	case regMEM:
		return c.DRAM[c.DP]
	}
	return 0
}

// writeReg writes a 16-bit value into the named register.
func (c *Core) writeReg(r uint8, v uint16) {
	switch r {
	case regNON:
	case regA:
		c.A = (c.A & 0xFF0000) | uint32(v)
	case regB:
		c.B = (c.B & 0xFF0000) | uint32(v)
	case regTR:
		c.TR = v
	case regDP:
		c.DP = uint8(v)
	case regRP:
		c.RP = v & 0x3FF
	case regDR:
		c.DR = v
		// When the DSP program writes DR, it is signalling "I have a
		// result". Set RQM so the CPU sees the data ready on the next
		// SR poll.
		c.SR |= srRQM
	case regDRNF:
		// "no-flag" DR write - same data, no RQM change.
		c.DR = v
	case regSR:
		// Writes from the DSP side can update user flags, but not the
		// CPU-side handshake bits.
		c.SR = (c.SR & (srRQM | srDRC | srDRS)) | (v &^ (srRQM | srDRC | srDRS))
	case regSO:
		c.SO = v
	case regK:
		c.K = int16(v)
	case regL:
		c.L = int16(v)
	case regMEM:
		c.DRAM[c.DP] = v
	}
}

// regSO is an alias for SO used by writeReg. SO shares the mux slot with SIM.
const regSO = regSIM
