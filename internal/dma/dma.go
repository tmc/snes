package dma

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
	TableAddr    uint16 // $43x8-$43x9 (A2AxL/A2AxH) - HDMA Table Address
	LineCount    uint8  // $43xA (NTRLx) - Current HDMA line counter byte
	Unused       uint8  // $43xB

	// Internal HDMA state.
	Active           bool
	hdmaAddr         uint16
	hdmaIndirectAddr uint16
	hdmaLines        int
	hdmaRepeat       bool
	hdmaDoTransfer   bool
	hdmaCompleted    bool
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

func ppuOffset(mode uint8, index int) uint32 {
	switch mode & 0x07 {
	case 0:
		return 0
	case 1:
		return uint32(index & 1)
	case 2:
		return 0
	case 3:
		if (index & 2) != 0 {
			return 1
		}
		return 0
	case 4:
		return uint32(index & 3)
	case 5:
		return uint32(index & 1)
	case 6:
		return 0
	case 7:
		if (index & 2) != 0 {
			return 1
		}
		return 0
	default:
		return 0
	}
}

func hdmaTransferLength(mode uint8) int {
	switch mode & 0x07 {
	case 0:
		return 1
	case 1, 2, 6:
		return 2
	case 3, 4, 5, 7:
		return 4
	default:
		return 1
	}
}

func validA(addr uint32) bool {
	bank := (addr >> 16) & 0xFF
	offset := addr & 0xFFFF
	if !((bank <= 0x3F) || (bank >= 0x80 && bank <= 0xBF)) {
		return true
	}
	return !((offset >= 0x2100 && offset <= 0x21FF) || (offset >= 0x4000 && offset <= 0x43FF))
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
	case 0x8: // A2AxL
		c.TableAddr = (c.TableAddr & 0xFF00) | uint16(value)
	case 0x9: // A2AxH
		c.TableAddr = (c.TableAddr & 0x00FF) | (uint16(value) << 8)
	case 0xA: // NTRLx
		c.LineCount = value
	case 0xB:
		c.Unused = value
	}
}

// Read handles reads from DMA registers.
func (d *DMA) Read(addr uint32) uint8 {
	channelIdx := (addr >> 4) & 0x7
	reg := addr & 0xF
	c := &d.Channels[channelIdx]

	switch reg {
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
	case 0x8:
		return uint8(c.TableAddr)
	case 0x9:
		return uint8(c.TableAddr >> 8)
	case 0xA:
		return c.LineCount
	case 0xB:
		return c.Unused
	default:
		return 0
	}
}

// Trigger ($420B Write)
func (d *DMA) Trigger(value uint8) {
	d.Enable = value
	if value != 0 && d.Scheduler != nil {
		d.Scheduler.AddCycles(8)
	}
	for i := 0; i < 8; i++ {
		if (value & (1 << i)) != 0 {
			if d.Scheduler != nil {
				d.Scheduler.AddCycles(8)
			}
			d.Execute(i)
		}
	}
	if value != 0 && d.Scheduler != nil {
		d.Scheduler.AddCycles(8)
	}
	d.Enable = 0 // General DMA is one-shot.
}

// Execute performs the DMA transfer for a channel.
func (d *DMA) Execute(channel int) {
	c := &d.Channels[channel]

	direction := (c.Control & 0x80) != 0 // 0: A->B, 1: B->A
	fixed := (c.Control & 0x08) != 0
	decrement := (c.Control & 0x10) != 0
	transferMode := c.Control & 0x07

	destBase := 0x2100 | uint32(c.Target)

	count := int(c.Size)
	if count == 0 {
		count = 0x10000 // 0 means 64KB
	}

	for n := 0; n < count; n++ {
		destAddr := destBase + ppuOffset(transferMode, n)
		srcAddr := uint32(c.SrcBank)<<16 | uint32(c.SrcAddr)

		if !validA(srcAddr) {
			// DMA cannot use MMIO/B-bus mirrors as the A-bus endpoint. The
			// address still steps and timing still elapses.
		} else if !direction {
			val := d.Bus.Read(srcAddr)
			d.Bus.Write(destAddr, val)
		} else {
			val := d.Bus.Read(destAddr)
			d.Bus.Write(srcAddr, val)
		}

		if !fixed {
			if decrement {
				c.SrcAddr--
			} else {
				c.SrcAddr++
			}
		}

		if d.Scheduler != nil {
			d.Scheduler.AddCycles(8)
		}
	}

	c.Size = 0
}

func (d *DMA) loadHDMAEntry(c *Channel) {
	addr := uint32(c.SrcBank)<<16 | uint32(c.hdmaAddr)
	line := d.Bus.Read(addr)
	c.hdmaAddr++
	c.LineCount = line

	if line == 0 {
		c.hdmaCompleted = true
		c.hdmaDoTransfer = false
		c.hdmaLines = 0
		return
	}

	c.hdmaCompleted = false
	c.hdmaRepeat = (line & 0x80) != 0
	c.hdmaLines = int(line & 0x7F)
	if c.hdmaLines == 0 {
		c.hdmaLines = 128
	}
	c.hdmaDoTransfer = true

	if (c.Control & 0x40) != 0 {
		low := d.Bus.Read(uint32(c.SrcBank)<<16 | uint32(c.hdmaAddr))
		c.hdmaAddr++
		high := d.Bus.Read(uint32(c.SrcBank)<<16 | uint32(c.hdmaAddr))
		c.hdmaAddr++
		c.hdmaIndirectAddr = uint16(low) | (uint16(high) << 8)
	}
}

func (d *DMA) doHDMATransfer(channel int) {
	c := &d.Channels[channel]
	transferMode := c.Control & 0x07
	indirect := (c.Control & 0x40) != 0
	destBase := 0x2100 | uint32(c.Target)
	bytes := hdmaTransferLength(transferMode)

	for n := 0; n < bytes; n++ {
		destAddr := destBase + ppuOffset(transferMode, n)

		var srcAddr uint32
		if indirect {
			srcAddr = uint32(c.IndirectBank)<<16 | uint32(c.hdmaIndirectAddr)
			c.hdmaIndirectAddr++
		} else {
			srcAddr = uint32(c.SrcBank)<<16 | uint32(c.hdmaAddr)
			c.hdmaAddr++
		}

		val := d.Bus.Read(srcAddr)
		d.Bus.Write(destAddr, val)
	}
}

// ExecuteHDMA runs one scanline of HDMA.
func (d *DMA) ExecuteHDMA() {
	if d.HDMAEnable == 0 {
		return
	}

	for i := 0; i < 8; i++ {
		mask := uint8(1 << i)
		if (d.HDMAEnable & mask) == 0 {
			continue
		}

		c := &d.Channels[i]
		if !c.Active {
			continue
		}
		if c.hdmaCompleted {
			c.Active = false
			continue
		}

		if c.hdmaDoTransfer {
			d.doHDMATransfer(i)
		}

		c.hdmaLines--
		if c.hdmaLines <= 0 {
			d.loadHDMAEntry(c)
			if c.hdmaCompleted {
				c.Active = false
				continue
			}
		} else {
			c.hdmaDoTransfer = c.hdmaRepeat
			if c.hdmaRepeat {
				c.LineCount = 0x80 | uint8(c.hdmaLines&0x7F)
			} else {
				c.LineCount = uint8(c.hdmaLines & 0x7F)
			}
		}
	}
}

// ResetHDMA initializes HDMA channels at frame start.
func (d *DMA) ResetHDMA() {
	for i := 0; i < 8; i++ {
		c := &d.Channels[i]
		mask := uint8(1 << i)
		if (d.HDMAEnable & mask) == 0 {
			c.Active = false
			c.hdmaCompleted = true
			c.hdmaDoTransfer = false
			c.hdmaLines = 0
			continue
		}

		c.Active = true
		c.hdmaAddr = c.TableAddr
		c.hdmaIndirectAddr = c.Size
		c.hdmaLines = 0
		c.hdmaCompleted = false
		c.hdmaDoTransfer = false
		c.hdmaRepeat = false
		d.loadHDMAEntry(c)
	}
}
