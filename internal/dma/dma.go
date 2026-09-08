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
	execution ExecutionState
	now       func() uint64
	wait      func(uint64)
	Bus       Bus
	Scheduler Scheduler

	Channels [8]Channel

	// $420B (MDMAEN) - General DMA Enable
	Enable uint8

	// $420C (HDMAEN) - H-DMA Enable
	HDMAEnable uint8

	// Trace, if non-nil, is called once at the start of each GP-DMA channel.
	// HDMATrace, if non-nil, is called once for each per-scanline HDMA
	// transfer. They are intended for diagnostics and should remain nil on the
	// hot path.
	Trace func(TransferTrace)

	HDMATrace func(TransferTrace)
}

type TransferTrace struct {
	Channel int
	Control uint8
	Target  uint8
	SrcBank uint8
	SrcAddr uint16
	Size    uint16
	Count   int
}

func NewDMA(bus Bus, scheduler Scheduler) *DMA {
	d := &DMA{Bus: bus, Scheduler: scheduler}
	d.Reset()
	return d
}

// Reset restores DMA registers to their power-on values.
func (d *DMA) Reset() {
	d.execution = ExecutionState{}
	d.Enable = 0
	d.HDMAEnable = 0
	for i := 0; i < 8; i++ {
		d.Channels[i] = Channel{
			Index:        i,
			Control:      0xff,
			Target:       0xff,
			SrcAddr:      0xffff,
			SrcBank:      0xff,
			Size:         0xffff,
			IndirectBank: 0xff,
			TableAddr:    0xffff,
			LineCount:    0xff,
			Unused:       0xff,
		}
	}
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
	return addr&0x40ff00 != 0x2100 && addr&0x40fe00 != 0x4000 &&
		addr&0x40ffe0 != 0x4200 && addr&0x40ff80 != 0x4300
}

// validBPair reports whether a (B-bus address, A-bus address) pair is a
// valid GP-DMA endpoint pair. Mirrors bsnes sfc/cpu/dma.cpp:119-129:
// transfers from WRAM to WRAM are invalid when the B-bus target is $80
// (i.e. $2180 WMDATA) and the A-bus source is in WRAM ($7E/$7F) or in
// the low/high mirror window at banks $00..$3F or $80..$BF, offsets
// $0000-$1FFF. When invalid, the bus write is suppressed but the
// caller must still elapse cycles and step addresses.
func validBPair(addrB uint8, addrA uint32) bool {
	if addrB != 0x80 {
		return true
	}
	bank := (addrA >> 16) & 0xFF
	offset := addrA & 0xFFFF
	// Canonical WRAM banks $7E and $7F.
	if bank == 0x7E || bank == 0x7F {
		return false
	}
	// Low-bank mirror at $00..$3F:$0000-$1FFF and high-bank mirror at
	// $80..$BF:$0000-$1FFF.
	if (bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) && offset <= 0x1FFF {
		return false
	}
	return true
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
		c.hdmaIndirectAddr = c.Size
	case 0x6: // DASxH
		c.Size = (c.Size & 0x00FF) | (uint16(value) << 8)
		c.hdmaIndirectAddr = c.Size
	case 0x7: // DASxB
		c.IndirectBank = value
	case 0x8: // A2AxL
		c.TableAddr = (c.TableAddr & 0xFF00) | uint16(value)
		c.hdmaAddr = c.TableAddr
	case 0x9: // A2AxH
		c.TableAddr = (c.TableAddr & 0x00FF) | (uint16(value) << 8)
		c.hdmaAddr = c.TableAddr
	case 0xA: // NTRLx
		c.LineCount = value
		c.hdmaLines = int(value & 0x7f)
		if c.hdmaLines == 0 {
			c.hdmaLines = 128
		}
		c.hdmaRepeat = value&0x80 != 0
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
	trace := TransferTrace{
		Channel: channel,
		Control: c.Control,
		Target:  c.Target,
		SrcBank: c.SrcBank,
		SrcAddr: c.SrcAddr,
		Size:    c.Size,
	}

	direction := (c.Control & 0x80) != 0 // 0: A->B, 1: B->A
	fixed := (c.Control & 0x08) != 0
	decrement := (c.Control & 0x10) != 0
	transferMode := c.Control & 0x07

	destBase := 0x2100 | uint32(c.Target)

	count := int(c.Size)
	if count == 0 {
		count = 0x10000 // 0 means 64KB
	}
	trace.Count = count
	if d.Trace != nil {
		d.Trace(trace)
	}

	for n := 0; n < count; n++ {
		destAddr := destBase + ppuOffset(transferMode, n)
		srcAddr := uint32(c.SrcBank)<<16 | uint32(c.SrcAddr)

		if !validA(srcAddr) {
			// DMA cannot use MMIO/B-bus mirrors as the A-bus endpoint. The
			// address still steps and timing still elapses.
		} else if !direction {
			val := d.Bus.Read(srcAddr)
			// bsnes sfc/cpu/dma.cpp:119-129: WRAM-to-WRAM via the
			// $2180 WMDATA port is hardware-invalid; the readA still
			// happens, but the writeB is suppressed. Address stepping
			// and cycle accumulation continue unconditionally.
			if validBPair(c.Target, srcAddr) {
				d.Bus.Write(destAddr, val)
			}
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
	c.hdmaIndirectAddr = 0
}

func (d *DMA) readHDMATable(c *Channel) uint8 {
	addr := uint32(c.SrcBank)<<16 | uint32(c.hdmaAddr)
	if !validA(addr) {
		return 0
	}
	return d.Bus.Read(addr)
}

func (d *DMA) loadHDMAEntry(channel int) {
	c := &d.Channels[channel]
	// hdmaReload performs the A-bus read even when the line counter does
	// not need reloading. In that case neither the byte nor pointer changes.
	line := d.readHDMATable(c)
	if c.hdmaLines != 0 {
		return
	}
	c.hdmaAddr++
	c.TableAddr = c.hdmaAddr
	c.LineCount = line
	c.hdmaCompleted = line == 0
	c.hdmaDoTransfer = line != 0
	c.hdmaRepeat = line&0x80 != 0
	c.hdmaLines = int(line & 0x7f)
	if line != 0 && c.hdmaLines == 0 {
		c.hdmaLines = 128
	}
	if c.Control&0x40 != 0 {
		low := d.readHDMATable(c)
		c.hdmaAddr++
		c.TableAddr = c.hdmaAddr
		// On a terminating last active channel the reference performs only
		// the first indirect read, leaving that byte in the high register.
		c.hdmaIndirectAddr = uint16(low) << 8
		c.Size = c.hdmaIndirectAddr
		if c.hdmaCompleted && d.hdmaFinished(channel) {
			return
		}
		high := d.readHDMATable(c)
		c.hdmaAddr++
		c.TableAddr = c.hdmaAddr
		c.hdmaIndirectAddr = uint16(low) | uint16(high)<<8
		c.Size = c.hdmaIndirectAddr
	}
}

func (d *DMA) hdmaFinished(channel int) bool {
	for i := channel + 1; i < len(d.Channels); i++ {
		if d.HDMAEnable&(1<<i) != 0 && !d.Channels[i].hdmaCompleted {
			return false
		}
	}
	return true
}

func (d *DMA) doHDMATransfer(channel int) {
	c := &d.Channels[channel]
	transferMode := c.Control & 0x07
	indirect := (c.Control & 0x40) != 0
	bytes := hdmaTransferLength(transferMode)
	trace := TransferTrace{
		Channel: channel,
		Control: c.Control,
		Target:  c.Target,
		Size:    c.Size,
		Count:   bytes,
	}
	if indirect {
		trace.SrcBank = c.IndirectBank
		trace.SrcAddr = c.hdmaIndirectAddr
	} else {
		trace.SrcBank = c.SrcBank
		trace.SrcAddr = c.hdmaAddr
	}
	if d.HDMATrace != nil {
		d.HDMATrace(trace)
	}

	for n := 0; n < bytes; n++ {
		target := c.Target + uint8(ppuOffset(transferMode, n))
		destAddr := uint32(0x2100) | uint32(target)

		var srcAddr uint32
		if indirect {
			srcAddr = uint32(c.IndirectBank)<<16 | uint32(c.hdmaIndirectAddr)
			c.hdmaIndirectAddr++
			c.Size = c.hdmaIndirectAddr
		} else {
			srcAddr = uint32(c.SrcBank)<<16 | uint32(c.hdmaAddr)
			c.hdmaAddr++
			c.TableAddr = c.hdmaAddr
		}
		if c.Control&0x80 == 0 {
			var value uint8
			if validA(srcAddr) {
				value = d.Bus.Read(srcAddr)
			}
			if validBPair(target, srcAddr) {
				d.Bus.Write(destAddr, value)
			}
		} else {
			var value uint8
			if validBPair(target, srcAddr) {
				value = d.Bus.Read(destAddr)
			}
			if validA(srcAddr) {
				d.Bus.Write(srcAddr, value)
			}
		}
	}
}

// ExecuteHDMA runs one scanline of HDMA.
func (d *DMA) ExecuteHDMA() {
	if d.HDMAEnable == 0 {
		return
	}
	// The reference transfers all channels before advancing any line counter
	// or fetching the next descriptors. Bus-visible order matters here.
	for i := range d.Channels {
		c := &d.Channels[i]
		if d.HDMAEnable&(1<<i) != 0 && c.Active && !c.hdmaCompleted && c.hdmaDoTransfer {
			d.doHDMATransfer(i)
		}
	}
	for i := range d.Channels {
		c := &d.Channels[i]
		if d.HDMAEnable&(1<<i) == 0 || !c.Active || c.hdmaCompleted {
			continue
		}
		c.LineCount--
		c.hdmaLines--
		c.hdmaRepeat = c.LineCount&0x80 != 0
		c.hdmaDoTransfer = c.hdmaRepeat
		d.loadHDMAEntry(i)
		if c.hdmaCompleted {
			c.Active = false
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
		// bsnes cpu/dma.cpp:146 hdmaSetup: hdmaAddress = sourceAddress.
		// CPU configures the HDMA table by writing $43x2/3 (SrcAddr);
		// $43x8/9 (TableAddr) reflect the running pointer and are
		// reseeded here, not used as the frame-start source.
		c.hdmaAddr = c.SrcAddr
		c.TableAddr = c.SrcAddr
		c.hdmaIndirectAddr = c.Size
		c.hdmaLines = 0
		c.hdmaCompleted = false
		c.hdmaDoTransfer = false
		c.hdmaRepeat = false
	}
	// Initialize every channel before loading any descriptor: an indirect
	// terminator must know whether a later channel remains enabled.
	for i := range d.Channels {
		if d.HDMAEnable&(1<<i) != 0 {
			d.loadHDMAEntry(i)
			if d.Channels[i].hdmaCompleted {
				d.Channels[i].Active = false
			}
		}
	}
}
