package bus

import (
	"fmt"
)

// Initial definitions for Wait State timings
const (
	WaitSlowROM = 8
	WaitFastROM = 6
	WaitWRAM    = 8
	WaitIO      = 6
	WaitJoypad  = 12
)

// InitializeWaitStates populates the wait state table with power-on defaults.
func (b *Bus) InitializeWaitStates() {
	// 1. Fill everything with SlowROM (8 cycles) as a safe default
	for bank := 0; bank < 256; bank++ {
		for page := 0; page < 256; page++ {
			b.waitStates[bank][page] = WaitSlowROM
		}
	}

	// 2. WRAM (Banks 7E-7F) is always 8 cycles
	// (Already set by default, but being explicit)
	b.SetWaitStates(0x7E, 0x7F, 0x00, 0xFF, WaitWRAM)

	// 3. I/O Registers (Banks 00-3F & 80-BF, Pages 21, 40-43)
	// Usually 6 cycles, with some exceptions.
	// Mirroring: $2100-$21FF, $4000-$43FF exist in Banks 00-3F and 80-BF.
	// We handle the specific 4016/4017 override below.
	ioPages := []uint32{0x21, 0x40, 0x41, 0x42, 0x43}
	for i := 0; i < 2; i++ {
		baseBank := uint32(0x00)
		if i == 1 {
			baseBank = 0x80
		}
		for bankOffset := uint32(0); bankOffset <= 0x3F; bankOffset++ {
			bank := baseBank + bankOffset
			for _, page := range ioPages {
				b.waitStates[bank][page] = WaitIO
			}
		}
	}

	// 4. Joypad I/O ($4016, $4017) - 12 cycles
	// These are in Page $40. Since our resolution is Page-level (256 bytes),
	// but the exception is only for 2 bytes, we have a granularity mismatch.
	// Discussion:
	// A strictly page-based lookup returns 6 for Page $40.
	// The CPU or Bus must check for specific address overrides if granularity is too coarse.
	// OPTION: We assume the Page Table lookup is "Base Wait States" and `GetWaitStates` handles exceptions.
	// HOWEVER, since we want speed, maybe we mark Page $40 as "Special" (e.g. 0 or 12) to trigger a check?
	// OR: We just accept that accessing $4000-$4015 gets 12 cycles too?
	// Notes often say "Internal I/O (21xx, 40xx..)" is 6.
	// Let's stick to 6 for the page, and handle 4016/17 in GetWaitStates check?
	// Actually, 4016/4017 are very infrequent compared to code execution.
	// Let's implement the override in GetWaitStates.
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
// $420D: bit 0 = 0 (SlowROM), 1 (FastROM).
func (b *Bus) WriteMEMSEL(value uint8) {
	b.MEMSEL = value
	fast := (value & 1) != 0
	fmt.Printf("DEBUG: WriteMEMSEL val=%02X FastROM=%v\n", value, fast)

	cycles := uint8(WaitSlowROM)
	if fast {
		cycles = WaitFastROM
	}

	// Update Banks $80-$FF to the new speed.
	// NOTE: This applies to Cartridge ROM areas.
	// WRAM and I/O mirrors in $80-$BF might need protection if they were overwritten?
	// Our Map logic separates device mapping from timing.
	// But initialization order matters.
	// initializeWaitStates() set defaults.
	// Banks 80-FF include mirrors of I/O (80-BF).
	// We should only update the ROM pages.
	// Typically ROM is mapped to where it's mapped.
	// For simplicity, we assume generic "Cartridge ROM" behavior for the "Rest" of the pages.
	// But wait, $80-$BF mirrors 00-3F.
	// So $80-$BF Low RAM/IO regions are NOT affected by FastROM bit. Only the upper half $8000+?
	// Correct: FastROM only applies to Banks $80-$FF, Addresses $8000-$FFFF.
	// (Pages $80-$FF within those banks).

	for bank := 0x80; bank <= 0xFF; bank++ {
		// FastROM area is typically the upper 32KB ($8000-$FFFF) of these banks.
		// Pages 0x80 - 0xFF.
		for page := 0x80; page <= 0xFF; page++ {
			b.waitStates[bank][page] = cycles
		}
	}
}

// GetWaitStates returns the cycle cost for accessing the given address.
func (b *Bus) GetWaitStates(address uint32) uint64 {
	bank := (address >> 16) & 0xFF
	page := (address >> 8) & 0xFF

	// 1. Check for specific overrides (Joypad)
	// $4016, $4017 (in any bank that maps 40xx? usually 00-3F, 80-BF)
	// We can cheat and just check the low 16 bits if we know it's I/O space.
	if page == 0x40 {
		offset := address & 0xFFFF
		if offset == 0x4016 || offset == 0x4017 {
			return uint64(WaitJoypad)
		}
	}

	// 2. Table Lookup
	return uint64(b.waitStates[bank][page])
}
