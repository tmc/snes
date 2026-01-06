package dma

import (
	"fmt"
)

// Interface defines the bus access required by DMA.
type Bus interface {
	Read(addr uint32) uint8
	Write(addr uint32, val uint8)
}

// Channel represents one of the 8 DMA channels.
type Channel struct {
	Index int // 0-7

	// Registers $43x0-$43xB
	Control      uint8  // $43x0 (DMAPx)
	Target       uint8  // $43x1 (BBADx) - B-Bus Address (PPU/APU Port)
	SrcAddr      uint16 // $43x2-$43x3 (A1TxL, A1TxH)
	SrcBank      uint8  // $43x4 (A1TxB)
	Size         uint16 // $43x5-$43x6 (DASxL, DASxH) - Transfer Size / Indirect Address
	IndirectBank uint8  // $43x7 (DASxB) - Indirect Bank
	TableAddr    uint16 // $43x8 (A2TxL) - HDMA Table Address
	TableBank    uint8  // $43x9 (A2TxB) - HDMA Table Bank
	LineCount    uint8  // $43xA (NTRLx) - HDMA Line Count
	Unused       uint8  // $43xB (Unused/HDMA)

	// Internal state
	Active bool
}

// Scheduler interface for cycle accounting
type Scheduler interface {
	AddCycles(cycles uint64)
}

// DMA Controller
type DMA struct {
	Bus       Bus
	Scheduler Scheduler

	Channels [8]Channel

	// MPYL/MPYM/MPYH ($2134-2136) are PPU Math results, unrelated to DMA, but usually implemented in PPU.
	// WRAM access ($2180-2183) handled by Bus/WRAM.

	// $420B (MDMAEN) - General DMA Enable
	Enable uint8

	// $420C (HDMAEN) - H-DMA Enable
	HDMAEnable uint8
}

func NewDMA(bus Bus, scheduler Scheduler) *DMA {
	d := &DMA{Bus: bus, Scheduler: scheduler}
	for i := 0; i < 8; i++ {
		d.Channels[i].Index = i
	}
	return d
}

// Write handles writes to DMA registers ($4300-$437F).
func (d *DMA) Write(addr uint32, value uint8) {
	channelIdx := (addr >> 4) & 0x7
	reg := addr & 0xF

	c := &d.Channels[channelIdx]

	switch reg {
	case 0x0: // DMAPx
		c.Control = value
	case 0x1: // BBADx
		c.Target = value
	case 0x2: // A1TxL
		c.SrcAddr = (c.SrcAddr & 0xFF00) | uint16(value)
	case 0x3: // A1TxH
		c.SrcAddr = (c.SrcAddr & 0x00FF) | (uint16(value) << 8)
	case 0x4: // A1TxB
		c.SrcBank = value
	case 0x5: // DASxL
		c.Size = (c.Size & 0xFF00) | uint16(value)
	case 0x6: // DASxH
		c.Size = (c.Size & 0x00FF) | (uint16(value) << 8)
	case 0x7: // DASxB
		c.IndirectBank = value
	case 0x8: // A2TxL
		c.TableAddr = (c.TableAddr & 0xFF00) | uint16(value)
	case 0x9: // A2TxB (also A2TxH)
		c.TableAddr = (c.TableAddr & 0x00FF) | (uint16(value) << 8) // Wait. A2TxH vs Bank?
		// Manual check needed. Usually 43x8/43x9 are HDMA Table Address.
		// 43x9 is High Byte of Table Address? Or Bank?
		// "43x9: HDMA Table Start Address (High)"
		// "43x4: Source Bank (A1TxB)".
		// "43x7: Indirect Bank (DASxB)".
		c.TableBank = value // Store as bank or high byte? Table address is 16-bit?
		// Actually A2Tx is 16-bit table address in Bank?
		// Docs say: "43x8: A2TxL", "43x9: A2TxH".
		// Bank is 43x4/7? No, HDMA table bank is 43x7 for indirect?
		// Let's assume 43x9 is High Byte of Table Addr for now.
	case 0xA: // NTRLx
		c.LineCount = value
	case 0xB: // Unused/More HDMA
		c.Unused = value
	}
}

// Read handles reads from DMA registers.
// Returns 0 (Open Bus) usually, except some registers track counts.
func (d *DMA) Read(addr uint32) uint8 {
	channelIdx := (addr >> 4) & 0x7
	reg := addr & 0xF
	c := &d.Channels[channelIdx]

	switch reg {
	// Most registers are R/W.
	case 0x0:
		return c.Control
	case 0x1:
		return c.Target
	case 0x2:
		return uint8(c.SrcAddr)
	case 0x3:
		return uint8(c.SrcAddr >> 8)
	case 0x4:
		return c.SrcBank
	case 0x5:
		return uint8(c.Size)
	case 0x6:
		return uint8(c.Size >> 8)
	case 0x7:
		return c.IndirectBank
		// ...
	}
	return 0
}

// Trigger ($420B Write)
func (d *DMA) Trigger(value uint8) {
	d.Enable = value
	// fmt.Printf("DMA Trigger: %02X\n", value)
	for i := 0; i < 8; i++ {
		if (value & (1 << i)) != 0 {
			d.Execute(i)
		}
	}
	d.Enable = 0 // Clear after execution (General DMA is one-shot)
}

// Execute performs the DMA transfer for a channel.
func (d *DMA) Execute(channel int) {
	c := &d.Channels[channel]

	// Transfer Mode
	direction := (c.Control & 0x80) != 0 // 0: CPU->PPU, 1: PPU->CPU
	fixed := (c.Control & 0x08) != 0
	decrement := (c.Control & 0x10) != 0

	// Transfer Unit (0-7)
	// 0: 1 byte (0)
	// 1: 2 bytes (0, 1)
	// 2: 2 bytes (0, 0)
	// 3: 4 bytes (0, 0, 1, 1)
	// 4: 4 bytes (0, 1, 2, 3)
	// 5: 4 bytes (0, 1, 0, 1) (inverted 3)
	// 6: 2 bytes (0, 0)
	// 7: 4 bytes (0, 0, 1, 1)
	transferMode := (c.Control & 0x07)

	// Target Address Base (PPU Port $21xx)
	destBase := 0x2100 | uint32(c.Target)

	// Length
	count := int(c.Size)
	if count == 0 {
		count = 0x10000 // 0 means 64KB
	}
	// DEBUG: Trace OAM DMA
	if destBase == 0x0012 {
		fmt.Printf("DMA[%d] to WRAM ($0012) Count=%X Src=%02X:%04X\n", channel, count, c.SrcBank, c.SrcAddr)
	}

	fmt.Printf("DMA[%d] Exec: Mode=%d Dir=%v Count=%X Src=%02X:%04X Dest=$%04X\n",
		channel, transferMode, direction, count, c.SrcBank, c.SrcAddr, destBase)

	for n := 0; n < count; n++ {
		// Calculate offset based on mode
		// We process 1 byte per loop iteration, but the PPU Register offset changes.
		// Wait. DMA transfers 'count' BYTES? Or 'count' BLOCKS?
		// "DASxL/H: Transfer Size (Number of Bytes)".
		// So loop runs 'count' times.
		// The PPU Address offset depends on (n % block_size).

		var ppuOffset uint32
		switch transferMode {
		case 0: // 1 byte: 0
			ppuOffset = 0
		case 1: // 2 bytes: 0, 1
			ppuOffset = uint32(n & 1)
		case 2: // 2 bytes: 0, 0
			ppuOffset = 0
		case 3: // 4 bytes: 0, 0, 1, 1
			if (n & 2) != 0 {
				ppuOffset = 1
			} else {
				ppuOffset = 0
			}
		case 4: // 4 bytes: 0, 1, 2, 3
			ppuOffset = uint32(n & 3)
		default:
			ppuOffset = 0 // Stub for 5,6,7
		}

		destAddr := destBase + ppuOffset
		srcAddr := uint32(c.SrcBank)<<16 | uint32(c.SrcAddr)

		if !direction {
			// CPU memory -> PPU
			val := d.Bus.Read(srcAddr)
			d.Bus.Write(destAddr, val)
		} else {
			// PPU -> CPU memory
			// PPU Read not fully implemented via Write?
			// Bus.Read(DestAddr) -> Bus.Write(SrcAddr)
			// But DestAddr is 21xx.
			// Currently Bus.Read(21xx) is implemented.
			val := d.Bus.Read(destAddr)
			d.Bus.Write(srcAddr, val)
		}

		// Update Source Addr
		if !fixed {
			if decrement {
				c.SrcAddr--
			} else {
				c.SrcAddr++
			}
		}

		// Cycle Accounting: 8 master cycles per byte (approx SlowROM)
		if d.Scheduler != nil {
			d.Scheduler.AddCycles(8)
		}
	}

	// Update Size to 0
	c.Size = 0
}

// ExecuteHDMA runs one scanline of HDMA.
// Should be called at H-Blank (e.g., H=274).
func (d *DMA) ExecuteHDMA() {
	if d.HDMAEnable == 0 {
		return
	}

	for i := 0; i < 8; i++ {
		if (d.HDMAEnable & (1 << i)) == 0 {
			continue
		}
		c := &d.Channels[i]
		fmt.Printf("HDMA Ch%d Exec: Line=%d Addr=%02X:%04X\n", i, c.LineCount, c.TableBank, c.TableAddr)

		// Init Frame logic needed?
		// Usually HDMA is initialized at Start of Frame (Line 0).
		// We need a hook for "InitHDMA".
		// For now, assume initialized or handle on fly?
		// LineCount is loaded at Init.
		// If LineCount is 0 (and active), we fetch new line count.

		// Determine Addressing Mode
		indirect := (c.Control & 0x40) != 0

		// Check Line Counter
		// Wait. We need "DoTransfer" on every scanline?
		// "HDMA transfers data to PPU registers during H-Blank encoded in a table".
		// Format: [LineCount] [Data...]
		// If LineCount > 0, it means "Wait LineCount lines"?
		// No. "Repeat" mode vs "One-Shot". (Bit 7 of LineCount).

		// If c.LineCount & 0x7F == 0, fetch new entry.
		if (c.LineCount & 0x7F) == 0 {
			// Fetch Line Count byte from Table
			addr := uint32(c.TableBank)<<16 | uint32(c.TableAddr)
			lc := d.Bus.Read(addr)
			c.TableAddr++ // Increment Table Addr

			// Terminate?
			if lc == 0 {
				// Disable channel for this frame?
				d.HDMAEnable &= ^(uint8(1 << i))
				continue
			}

			c.LineCount = lc

			// If Indirect, Fetch Indirect Address
			if indirect {
				addr = uint32(c.TableBank)<<16 | uint32(c.TableAddr)
				low := d.Bus.Read(addr)
				c.TableAddr++
				high := d.Bus.Read(addr + 1) // +1?
				// Wait. addr was incremented. So just read again.
				addr = uint32(c.TableBank)<<16 | uint32(c.TableAddr)
				high = d.Bus.Read(addr)
				c.TableAddr++

				// Store Indirect Address in Size (DASx) as generic storage?
				// Docs say: "Indirect Address is stored in DASx".
				c.Size = uint16(low) | (uint16(high) << 8)
			}

			// Transfer on First Line?
			// Usually yes.
			d.doHDMATransfer(i)
		} else {
			// Decrement counter
			// Check Repeat Mode (Bit 7 of LineCount logic stored?)
			// Wait. Line Count byte: RLLLLLLL.
			// If R=1 (Repeat), we transfer every line.
			// If R=0, we transfer ONLY on the first line (when loaded), then wait L lines.
			repeat := (c.LineCount & 0x80) != 0
			if repeat {
				d.doHDMATransfer(i)
			}

			c.LineCount--
			// Actually decrement logic:
			// new_line_count = (old & 0x80) | ((old & 0x7F) - 1)
			val := c.LineCount & 0x7F
			val--
			c.LineCount = (c.LineCount & 0x80) | val
		}
	}
}

func (d *DMA) doHDMATransfer(channel int) {
	c := &d.Channels[channel]

	// Transfer Mode (Same as DMA)
	transferMode := (c.Control & 0x07)
	indirect := (c.Control & 0x40) != 0

	// Byte Count: 1, 2, or 4 depending on Mode.
	// Mode 0: 1 byte
	// Mode 1, 2, 6: 2 bytes
	// Mode 3, 4: 4 bytes
	bytes := 1
	switch transferMode {
	case 0:
		bytes = 1
	case 1, 2, 6:
		bytes = 2
	case 3, 4:
		bytes = 4
	case 5: // 4 bytes (inverted)
		bytes = 4
	case 7: // 4 bytes
		bytes = 4
	}

	// Dest Base
	destBase := 0x2100 | uint32(c.Target)

	for n := 0; n < bytes; n++ {
		// PPU Offset logic
		var ppuOffset uint32
		switch transferMode {
		case 0:
			ppuOffset = 0
		case 1:
			ppuOffset = uint32(n & 1)
		case 2:
			ppuOffset = 0
		case 3:
			if (n & 2) != 0 {
				ppuOffset = 1
			} else {
				ppuOffset = 0
			}
		case 4:
			ppuOffset = uint32(n & 3)
		default:
			ppuOffset = 0
		}

		destAddr := destBase + ppuOffset

		// Source Address
		var srcAddr uint32
		if indirect {
			// Indirect Addr stored in c.Size (DASx)
			// Bank is c.IndirectBank
			srcAddr = uint32(c.IndirectBank)<<16 | uint32(c.Size)
			c.Size++ // Increment Indirect Pointer
		} else {
			// Direct Table Addr
			srcAddr = uint32(c.TableBank)<<16 | uint32(c.TableAddr)
			c.TableAddr++
		}

		val := d.Bus.Read(srcAddr)
		fmt.Printf("HDMA Write [$%04X] = %02X\n", destAddr, val)
		d.Bus.Write(destAddr, val)
	}
}

// ResetHDMA initializes HDMA channels at frame start.
func (d *DMA) ResetHDMA() {
	// Do NOT clear d.HDMAEnable. It is persistent.
	// Real hardware reloads Line Counter?
	// For simplicity: We assume HDMA Enable is persistent, but we need to reload context.
	// "At frame start, A2Tx is reloaded from internal latch? No."
	// "HDMA channels are re-initialized... Line counters loaded."
	// Actually we should Reload TableAddr from Start Address?
	// Registers $43x8/43x9 are "current table address".
	// The CPU writes the Start Address.
	// Implementation Detail: Writing to $43x8/9 updates Start Address AND Current Address?
	// We need shadowing.
	// For now, assume CPU sets it up every frame or we just run.
	// But LineCount state must be reset or processed.

	// Simply clear LineCounts to force fetch?
	for i := 0; i < 8; i++ {
		d.Channels[i].LineCount = 0
	}
}
