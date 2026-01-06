package cpu

// getEffectiveAddress resolves the target address based on the addressing mode.
// It may modify cycles if page boundaries are crossed.
func (c *CPU) getEffectiveAddress(mode AddressingMode) (uint32, bool) {
	// wrapper for 24-bit address returns
	// bool return indicates if operation was immediate (value in address)

	switch mode {
	case AddrImpl, AddrAcc:
		return 0, false

	case AddrImm:
		// Immediate mode: Return current PC.
		// Note: The instruction implementation is responsible for reading 1 or 2 bytes
		// and incrementing PC accordingly.
		return uint32(c.PB)<<16 | uint32(c.PC), true

	case AddrAbs:
		addr := c.fetchWord()
		return uint32(c.DB)<<16 | uint32(addr), false

	case AddrAbsX:
		addr := c.fetchWord()
		// Absolute, X.
		// Bank is DB.
		// 16-bit or 24-bit? 65816: DB:Addr+X.
		// If (Addr + X) overflows 16-bits, it does NOT cross to next bank in this mode.
		// Unless it's Abs Long X? No, standard Abs X wraps within bank.
		// Effective = (DB << 16) | ((addr + X) & 0xFFFF)
		// Wait: Page crossing penalty?
		// 65816 Native Mode (E=0): No page crossing penalty for indexing (except rare cases?)
		// Emulation Mode (E=1): Yes page crossing penalty?
		// Notes say: "Crosses page boundary... +1 cycle"
		// Optimization: Check page boundary
		if c.E && (addr&0xFF00) != ((addr+c.X)&0xFF00) {
			c.Cycles++
		}
		return uint32(c.DB)<<16 | (uint32(addr+c.X) & 0xFFFF), false

	case AddrAbsY:
		addr := c.fetchWord()
		if c.E && (addr&0xFF00) != ((addr+c.Y)&0xFF00) {
			c.Cycles++
		}
		return uint32(c.DB)<<16 | (uint32(addr+c.Y) & 0xFFFF), false

	case AddrLong:
		addr := c.fetchWord()
		bank := c.fetchByte()
		return uint32(bank)<<16 | uint32(addr), false

	case AddrLongX:
		addr := c.fetchWord()
		bank := c.fetchByte()
		// Long indexed implies 24-bit linear add. It CAN cross banks?
		// "24-bit linear addition" -> Yes.
		full := (uint32(bank) << 16) | uint32(addr)
		return (full + uint32(c.X)) & 0xFFFFFF, false

	case AddrDir:
		offset := uint16(c.fetchByte())
		return c.getDirectPageAddress(offset), false

	case AddrDirX:
		offset := uint16(c.fetchByte())
		return c.getDirectPageAddress(offset + c.X), false

	case AddrDirY:
		offset := uint16(c.fetchByte())
		return c.getDirectPageAddress(offset + c.Y), false

	case AddrDirInd:
		// (dp)
		offset := uint16(c.fetchByte())
		// Fetch pointer from DP safely
		ptr := c.readWordDirectPage(offset)
		return uint32(c.DB)<<16 | uint32(ptr), false

	case AddrDirIndL:
		// [dp]
		offset := uint16(c.fetchByte())
		// Read 24-bit pointer
		ptr := c.readLongDirectPage(offset)
		return ptr, false

	case AddrIndX: // (dp, X) "Indirect Indexed" (Pre-indexed)
		// Spec says (dp,X).
		// Add X to offset. Then read pointer.
		offset := uint16(c.fetchByte()) + c.X
		ptr := c.readWordDirectPage(offset)
		return uint32(c.DB)<<16 | uint32(ptr), false

	case AddrIndY: // (dp), Y "Indirect Indexed" (Post-indexed)
		offset := uint16(c.fetchByte())
		ptr := c.readWordDirectPage(offset)
		// Add Y to the pointer base
		// Always crosses pages in native mode?
		// E=1 page crossing penalty applies.
		base := uint32(c.DB)<<16 | uint32(ptr)
		final := base + uint32(c.Y)
		// TODO: Checking penalty
		return final & 0xFFFFFF, false // Masking? DB stays same. Only low 16-bits wrap?
		// 65816: (dp),Y -> "Address is formed by adding Y to the pointer."
		// "The effective address is Bank:Pointer+Y".
		// Does it wrap within bank? Yes, usually.

	case AddrSr: // (sr, S)
		// Stack Relative: Offset + S
		offset := uint16(c.fetchByte())
		return uint32(c.S + offset), false

	case AddrSrIndY: // (sr, S), Y
		// Stack Relative Indirect Indexed
		// Ptr is at S + offset.
		offset := uint16(c.fetchByte())
		ptrAddr := uint32(c.S + offset)
		// Read pointer from stack
		low := c.read(ptrAddr)
		high := c.read((ptrAddr + 1) & 0xFFFF)
		ptr := uint32(high)<<8 | uint32(low)

		// Add Y. Bank is DBR.
		return uint32(c.DB)<<16 | ((ptr + uint32(c.Y)) & 0xFFFF), false

	case AddrAbsInd: // (addr) JMP only
		// Absolute Indirect.
		// "JMP (abs)" -> "The second and third bytes... form a 16-bit address... the location of the transfer address."
		// Pointer is fetched from [PB:Operand]
		addr := c.fetchWord()
		ptrAddr := uint32(c.PB)<<16 | uint32(addr)

		// Read the pointer itself (16-bit)
		ptrLow := c.read(ptrAddr)
		ptrHigh := c.read(ptrAddr + 1) // Wraps within bank? Usually for JMP

		finalAddr := uint16(ptrHigh)<<8 | uint16(ptrLow)
		return uint32(c.PB)<<16 | uint32(finalAddr), false

	case AddrAbsIndX: // (addr, X) JMP/JSR
		// Absolute Indexed Indirect.
		// Operand + X = Pointer Address.
		// Pointer is in PB.
		base := c.fetchWord()
		ptrAddr := (uint32(c.PB) << 16) | ((uint32(base) + uint32(c.X)) & 0xFFFF)
		ptrLow := c.read(ptrAddr)

		// ptrAddr is 24-bit. +1 might cross bank?
		// "Wraps within bank" usually applies to JMP.
		// Let's assume bank wrap for safety.
		// ptrHigh logic:
		ptrHighAddr := (ptrAddr & 0xFF0000) | ((ptrAddr + 1) & 0xFFFF)
		ptrHigh := c.read(ptrHighAddr)

		final := uint16(ptrHigh)<<8 | uint16(ptrLow)
		return uint32(c.PB)<<16 | uint32(final), false

	case AddrAbsIndLong: // [addr] JML
		// Absolute Indirect Long.
		// Operand is 16-bit address in Bank 0? No, Operand is 16-bit address.
		// Pointer is at 00:Operand.
		// "The operand is a 16-bit absolute address... low byte of the long indirect address."
		// It reads 3 bytes.
		addr := c.fetchWord()
		// Pointer in Bank 0.
		ptrLow := c.read(uint32(addr))
		ptrHigh := c.read((uint32(addr) + 1) & 0xFFFF) // Wrap in bank 0
		ptrBank := c.read((uint32(addr) + 2) & 0xFFFF)

		finalAddr := uint32(ptrBank)<<16 | uint32(ptrHigh)<<8 | uint32(ptrLow)
		return finalAddr, false

	case AddrRel: // near label (8-bit)
		offset := int8(c.fetchByte())
		// Relative addressing returns the *target address* for the PC?
		// Or do we return the offset?
		// getEffectiveAddress usually returns a 24-bit linear address.
		// For branches, we want checks.
		// This helper is generic. Let's return the target address.
		// PC has already advanced past opcode and operand.
		// Target = PC + offset.
		dest := int32(c.PC) + int32(offset)
		return uint32(c.PB)<<16 | (uint32(dest) & 0xFFFF), false

	case AddrRelL: // long label (16-bit)
		offset := int16(c.fetchWord())
		dest := int32(c.PC) + int32(offset)
		return uint32(c.PB)<<16 | (uint32(dest) & 0xFFFF), false

	case AddrDirIndLIdxY: // [dp], Y
		offset := uint16(c.fetchByte())
		ptr := c.readLongDirectPage(offset)
		return (ptr + uint32(c.Y)) & 0xFFFFFF, false

	}
	panic("unhandled addressing mode")
}

// Helpers for Direct Page nuances

func (c *CPU) getDirectPageAddress(offset uint16) uint32 {
	// If DL != 0, +1 Cycle penalty
	if (c.D & 0xFF) != 0 {
		c.Cycles++
	}

	if c.E && (c.D&0xFF) == 0 {
		// Emulation Mode & DL=0: Wrap within Page (00-FF)
		// addr = (D & 0xFF00) | ((D + offset) & 0x00FF)
		// Since D is aligned to page (D&0xFF==0), D is just DH<<8.
		// And offset wraps.
		return uint32((c.D & 0xFF00) | (offset & 0x00FF))
	}

	// Native Mode or E=1 but D not page aligned:
	// Linear addition, wraps at 16-bits (Bank 0)
	return uint32((c.D + offset) & 0xFFFF)
}

func (c *CPU) readWordDirectPage(offset uint16) uint16 {
	addr := c.getDirectPageAddress(offset)
	low := c.read(addr)

	// For the high byte, we need to handle wrapping correctly.
	// If E=1 and DL=0, we wrap in page 0.
	// i.e., fetching pointer from $00FF reads $00FF and $0000.
	var addrHigh uint32
	if c.E && (c.D&0xFF) == 0 {
		offsetHigh := (offset + 1) & 0x00FF
		addrHigh = uint32((c.D & 0xFF00) | offsetHigh)
	} else {
		addrHigh = uint32((c.D + offset + 1) & 0xFFFF)
	}

	high := c.read(addrHigh)
	return uint16(high)<<8 | uint16(low)
}

func (c *CPU) readLongDirectPage(offset uint16) uint32 {
	// Similar to readWord but 3 bytes.
	// Wrapping logic applies to each byte step.
	// This is slightly tedious, maybe generalize?
	w := c.readWordDirectPage(offset)

	// 3rd byte (Bank)
	var addrBank uint32
	if c.E && (c.D&0xFF) == 0 {
		offsetBank := (offset + 2) & 0x00FF
		addrBank = uint32((c.D & 0xFF00) | offsetBank)
	} else {
		addrBank = uint32((c.D + offset + 2) & 0xFFFF)
	}
	b := c.read(addrBank)
	return uint32(b)<<16 | uint32(w)
}

// fetchWord reads the next word at PC and increments PC by 2.
// It performs two fetches.
func (c *CPU) fetchWord() uint16 {
	low := c.fetchByte()
	high := c.fetchByte()
	return uint16(high)<<8 | uint16(low)
}
