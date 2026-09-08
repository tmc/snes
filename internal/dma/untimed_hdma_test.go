package dma

// untimedHDMA is the former immediate HDMA model, retained only as an
// independent bus-order/state oracle for the timed continuation engine.
// It does not call RequestHDMA, BeginEdge or RunSlice. Address/mode helpers
// are shared; source-literal address matrix tests independently pin those.
type untimedHDMA struct{ *DMA }

func (d *untimedHDMA) readHDMATable(c *Channel) uint8 {
	addr := uint32(c.SrcBank)<<16 | uint32(c.hdmaAddr)
	if !validA(addr) {
		return 0
	}
	return d.Bus.Read(addr)
}

func (d *untimedHDMA) loadHDMAEntry(channel int) {
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

func (d *untimedHDMA) doHDMATransfer(channel int) {
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

// scanline runs one scanline of the untimed reference model.
func (d *untimedHDMA) scanline() {
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

// setup initializes the untimed model at frame start.
func (d *untimedHDMA) setup() {
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

func (d *untimedHDMA) hdmaFinished(channel int) bool {
	for i := channel + 1; i < len(d.Channels); i++ {
		if d.HDMAEnable&(1<<i) != 0 && !d.Channels[i].hdmaCompleted {
			return false
		}
	}
	return true
}
