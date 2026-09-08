package bus

const (
	WaitSlowROM = 8
	WaitFastROM = 6
	WaitWRAM    = 8
	WaitIO      = 6
	WaitJoypad  = 12
)

// InitializeWaitStates populates the bus timing table with power-on defaults.
func (b *Bus) InitializeWaitStates() {
	// bsnes sfc/cpu/memory.cpp: CPU::read and CPU::write classify the
	// address bus independently of whether a device is mapped there.
	for bank := 0; bank < 256; bank++ {
		for page := 0; page < 256; page++ {
			cycles := uint8(WaitSlowROM)
			if bank&0x40 == 0 {
				switch {
				case page >= 0x20 && page < 0x40, page >= 0x42 && page < 0x60:
					cycles = WaitIO
				case page >= 0x40 && page < 0x42:
					cycles = WaitJoypad
				}
			}
			b.waitStates[bank][page] = cycles
		}
	}
}

// SetWaitStates sets the cycle count for a range of banks and pages.
func (b *Bus) SetWaitStates(startBank, endBank, startPage, endPage uint8, cycles uint8) {
	for bank := int(startBank); bank <= int(endBank); bank++ {
		for page := int(startPage); page <= int(endPage); page++ {
			b.waitStates[bank][page] = cycles
		}
	}
}

// WriteMEMSEL updates the memory select register and refreshes FastROM timings.
// Bit 0 selects six-cycle accesses in $80-BF:8000-FFFF and $C0-FF:0000-FFFF.
func (b *Bus) WriteMEMSEL(value uint8) {
	b.MEMSEL = value
	cycles := uint8(WaitSlowROM)
	if value&1 != 0 {
		cycles = WaitFastROM
	}
	for bank := 0x80; bank <= 0xff; bank++ {
		firstPage := 0x80
		if bank >= 0xc0 {
			firstPage = 0
		}
		for page := firstPage; page <= 0xff; page++ {
			b.waitStates[bank][page] = cycles
		}
	}
}

// GetWaitStates returns the cycle cost for accessing the given address.
func (b *Bus) GetWaitStates(address uint32) uint64 {
	return uint64(b.waitStates[(address>>16)&0xff][(address>>8)&0xff])
}
