package dma

import "fmt"

// ExecutionState is the bus controller's continuation. CPU instruction
// continuations are separate and are not represented by this snapshot.
type ExecutionState struct {
	General, CPUWait                     bool
	Armed, Pending                       bool
	HDMAPending, HDMASetup               bool
	HDMAResetDone                        bool
	HDMAAt                               uint64
	Phase, Next, Resume, HDMAReturn      uint8
	Wait, Clocks, ClockCount             uint64
	Channel, DMAChannel, Index, DMAIndex int
	Setup                                bool
	Address, Destination                 uint32
	ReadValid, WriteValid                bool
	Value                                uint8
}

const (
	phaseIdle uint8 = iota
	phaseDispatch
	phaseFinish
	phaseGPStart
	phaseGPChannel
	phaseGPFirst
	phaseGPByte
	phaseGPAfter
	phaseHDMAStart
	phaseHDMAChannel
	phaseHDMAByte
	phaseHDMAAfter
	phaseHDMAAdvance
	phaseDescriptor
	phaseDescriptorDone
	phaseIndirectLow
	phaseIndirectLowDone
	phaseIndirectHigh
	phaseIndirectHighDone
	phaseRead
	phaseReadDone
	phaseWrite
	phaseGPAfterDecrement
	phaseHDMADispatch
	phaseHDMAReturn
	phaseRelease
	phaseHDMAAlignedReturn
)

// SetClock connects timed DMA to the host clock. wait must advance time and
// synchronize devices without executing a CPU instruction or another DMA edge.
func (d *DMA) SetClock(now func() uint64, wait func(uint64)) { d.now, d.wait = now, wait }

// Request arms general DMA at the next CPU edge; a second edge grants the bus.
func (d *DMA) Request(mask uint8) { d.Enable = mask; d.execution.Pending = mask != 0 }

// RequestHDMA records a beam event without accessing the bus. Events ahead of
// the CPU clock remain pending until that timestamp is reached.
func (d *DMA) RequestHDMA(at uint64, setup bool) {
	s := &d.execution
	s.HDMAPending, s.HDMASetup, s.HDMAAt = true, setup, at
	s.HDMAResetDone = false
}

func (d *DMA) hdmaReady() bool {
	s := &d.execution
	if !s.HDMAPending || (d.now != nil && s.HDMAAt > d.now()) {
		return false
	}
	if s.HDMASetup && !s.HDMAResetDone {
		for i := range d.Channels {
			d.Channels[i].hdmaCompleted = false
			d.Channels[i].hdmaDoTransfer = false
		}
		s.HDMAResetDone = true
	}
	for i := range d.Channels {
		if d.HDMAEnable&(1<<i) != 0 && (s.HDMASetup || !d.Channels[i].hdmaCompleted) {
			return true
		}
	}
	s.HDMAPending = false
	return false
}

// BeginEdge samples requests at the beginning of a CPU cycle. The first edge
// arms ownership; the following edge starts work before that CPU cycle runs.
// The caller must drain RunSlice while Busy before continuing its CPU cycle.
// Clock counts other than 6, 8 or 12 are ignored without changing ownership.
func (d *DMA) BeginEdge(clocks uint64) {
	if clocks != 6 && clocks != 8 && clocks != 12 {
		return
	}
	s := &d.execution
	ready := d.hdmaReady()
	if s.Phase != phaseIdle {
		return
	}
	if s.Armed {
		s.ClockCount = clocks
		s.Clocks = 0
		s.Phase = phaseDispatch
	} else if s.Pending || ready {
		s.Armed = true
	}
}

// Busy reports whether the CPU's current bus cycle is suspended.
func (d *DMA) Busy() bool { return d.execution.Phase != phaseIdle }

func (d *DMA) delay(clocks uint64, next uint8) {
	d.execution.Wait, d.execution.Phase = clocks, next
}

func (d *DMA) align(next uint8) {
	var now uint64
	if d.now != nil {
		now = d.now()
	}
	d.execution.Clocks = 0
	d.delay(8-now%8, next)
}

func (d *DMA) readPhase(address uint32, valid bool, next uint8) {
	s := &d.execution
	s.Address, s.ReadValid, s.Next = address, valid, next
	d.delay(4, phaseRead)
}

func (d *DMA) transferPhase(channel, index int, address uint32, next uint8) {
	c := &d.Channels[channel]
	s := &d.execution
	target := c.Target + uint8(ppuOffset(c.Control&7, index))
	b := uint32(0x2100) | uint32(target)
	s.Destination, s.WriteValid = b, validBPair(target, address)
	if c.Control&0x80 != 0 {
		s.Destination, s.WriteValid = address, validA(address)
		address = b
		s.ReadValid = validBPair(target, s.Destination)
	} else {
		s.ReadValid = validA(address)
	}
	s.Resume = next
	d.readPhase(address, s.ReadValid, phaseWrite)
}

func (d *DMA) startHDMA(resume uint8) bool {
	if !d.hdmaReady() {
		return false
	}
	s := &d.execution
	s.HDMAPending = false
	if d.HDMAEnable == 0 {
		return false
	}
	s.Setup, s.Channel, s.Index = s.HDMASetup, 0, 0
	// Next is used by reads; retain the GDMA continuation separately.
	s.HDMAReturn = resume
	d.delay(8, phaseHDMAStart)
	return true
}

// RunSlice executes at most budget DMA clocks, preserving a continuation after
// either half of a bus read. A read is published only after its first four
// clocks; its result is latched until the second four clocks finish.
func (d *DMA) RunSlice(budget uint64) uint64 {
	var used uint64
	s := &d.execution
	for s.Phase != phaseIdle {
		if s.Wait != 0 {
			if used == budget {
				break
			}
			n := min(s.Wait, budget-used)
			s.Wait -= n
			if !s.CPUWait {
				s.Clocks += n
			}
			used += n
			if d.wait != nil {
				d.wait(n)
			} else if d.Scheduler != nil {
				d.Scheduler.AddCycles(n)
			}
			if s.Wait != 0 {
				break
			}
		}
		switch s.Phase {
		case phaseDispatch:
			if d.hdmaReady() {
				if d.Enable == 0 && d.HDMAEnable != 0 {
					d.align(phaseHDMADispatch)
					continue
				}
				if d.startHDMA(phaseDispatch) {
					continue
				}
			}
			if s.Pending {
				s.Pending = false
				if d.Enable != 0 {
					d.align(phaseGPStart)
					continue
				}
			}
			s.Armed = false
			s.Phase = phaseIdle
		case phaseFinish:
			s.CPUWait = true
			d.delay(s.ClockCount-s.Clocks%s.ClockCount, phaseRelease)
		case phaseRelease:
			s.CPUWait = false
			s.General = false
			s.Armed = false
			s.Phase = phaseIdle
		case phaseGPStart:
			s.General = true
			s.DMAChannel = 0
			s.DMAIndex = 0
			d.delay(8, phaseGPChannel)
		case phaseGPChannel:
			if d.startHDMA(phaseGPChannel) {
				continue
			}
			for s.DMAChannel < 8 && d.Enable&(1<<s.DMAChannel) == 0 {
				s.DMAChannel++
			}
			if s.DMAChannel == 8 {
				s.Phase = phaseFinish
				continue
			}
			c := &d.Channels[s.DMAChannel]
			if d.Trace != nil {
				count := int(c.Size)
				if count == 0 {
					count = 65536
				}
				d.Trace(TransferTrace{Channel: s.DMAChannel, Control: c.Control, Target: c.Target, SrcBank: c.SrcBank, SrcAddr: c.SrcAddr, Size: c.Size, Count: count})
			}
			s.DMAIndex = 0
			d.delay(8, phaseGPFirst)
		case phaseGPFirst:
			if d.startHDMA(phaseGPByte) {
				continue
			}
			s.Phase = phaseGPByte
		case phaseGPByte:
			if d.Enable&(1<<s.DMAChannel) == 0 {
				s.DMAChannel++
				s.Phase = phaseGPChannel
				continue
			}
			c := &d.Channels[s.DMAChannel]
			d.transferPhase(s.DMAChannel, s.DMAIndex, uint32(c.SrcBank)<<16|uint32(c.SrcAddr), phaseGPAfter)
		case phaseGPAfter:
			c := &d.Channels[s.DMAChannel]
			if c.Control&8 == 0 {
				if c.Control&16 == 0 {
					c.SrcAddr++
				} else {
					c.SrcAddr--
				}
			}
			s.DMAIndex++
			// The reference samples HDMA before decrementing transferSize. On
			// cancellation the short-circuit leaves that register undecremented.
			if d.startHDMA(phaseGPAfterDecrement) {
				continue
			}
			s.Phase = phaseGPAfterDecrement
		case phaseGPAfterDecrement:
			c := &d.Channels[s.DMAChannel]
			if d.Enable&(1<<s.DMAChannel) != 0 {
				c.Size--
				c.hdmaIndirectAddr = c.Size
			}
			if d.Enable&(1<<s.DMAChannel) == 0 || c.Size == 0 {
				d.Enable &^= 1 << s.DMAChannel
				s.DMAChannel++
				s.Phase = phaseGPChannel
			} else {
				s.Phase = phaseGPByte
			}
		case phaseHDMADispatch:
			if !d.startHDMA(phaseFinish) {
				s.Phase = phaseFinish
			}
		case phaseHDMAReturn:
			if d.Enable == 0 && s.General {
				s.CPUWait = true
				d.delay(s.ClockCount-s.Clocks%s.ClockCount, phaseHDMAAlignedReturn)
			} else if d.Enable == 0 && s.HDMAReturn == phaseDispatch {
				s.Pending = false
				s.Phase = phaseFinish
			} else {
				s.Phase = s.HDMAReturn
			}
		case phaseHDMAAlignedReturn:
			s.CPUWait = false
			s.Phase = s.HDMAReturn
		case phaseHDMAStart:
			s.Channel = 0
			s.Index = 0
			if s.Setup {
				for i := range d.Channels {
					c := &d.Channels[i]
					c.Active = d.HDMAEnable&(1<<i) != 0
				}
			}
			s.Phase = phaseHDMAChannel
		case phaseHDMAChannel:
			for s.Channel < 8 && (d.HDMAEnable&(1<<s.Channel) == 0 || (!s.Setup && d.Channels[s.Channel].hdmaCompleted)) {
				s.Channel++
			}
			if s.Channel == 8 {
				if s.Setup {
					s.Phase = phaseHDMAReturn
				} else {
					s.Channel = 0
					s.Phase = phaseHDMAAdvance
				}
				continue
			}
			c := &d.Channels[s.Channel]
			d.Enable &^= 1 << s.Channel
			if s.Setup {
				c.Active = true
				c.hdmaAddr = c.SrcAddr
				c.TableAddr = c.SrcAddr
				c.hdmaLines = 0
				c.LineCount = 0
				s.Phase = phaseDescriptor
				continue
			}
			if !c.hdmaDoTransfer {
				s.Channel++
				continue
			}
			s.Index = 0
			if d.HDMATrace != nil {
				bank, addr := c.SrcBank, c.hdmaAddr
				if c.Control&0x40 != 0 {
					bank, addr = c.IndirectBank, c.hdmaIndirectAddr
				}
				d.HDMATrace(TransferTrace{Channel: s.Channel, Control: c.Control, Target: c.Target, SrcBank: bank, SrcAddr: addr, Size: c.Size, Count: hdmaTransferLength(c.Control)})
			}
			s.Phase = phaseHDMAByte
		case phaseHDMAByte:
			c := &d.Channels[s.Channel]
			var addr uint32
			if c.Control&0x40 != 0 {
				addr = uint32(c.IndirectBank)<<16 | uint32(c.hdmaIndirectAddr)
				c.hdmaIndirectAddr++
				c.Size = c.hdmaIndirectAddr
			} else {
				addr = uint32(c.SrcBank)<<16 | uint32(c.hdmaAddr)
				c.hdmaAddr++
				c.TableAddr = c.hdmaAddr
			}
			d.transferPhase(s.Channel, s.Index, addr, phaseHDMAAfter)
		case phaseHDMAAfter:
			s.Index++
			if s.Index < hdmaTransferLength(d.Channels[s.Channel].Control) {
				s.Phase = phaseHDMAByte
			} else {
				s.Channel++
				s.Phase = phaseHDMAChannel
			}
		case phaseHDMAAdvance:
			for s.Channel < 8 && (d.HDMAEnable&(1<<s.Channel) == 0 || d.Channels[s.Channel].hdmaCompleted) {
				s.Channel++
			}
			if s.Channel == 8 {
				s.Phase = phaseHDMAReturn
				continue
			}
			c := &d.Channels[s.Channel]
			c.LineCount--
			c.hdmaLines--
			c.hdmaRepeat = c.LineCount&0x80 != 0
			c.hdmaDoTransfer = c.hdmaRepeat
			s.Phase = phaseDescriptor
		case phaseDescriptor:
			c := &d.Channels[s.Channel]
			addr := uint32(c.SrcBank)<<16 | uint32(c.hdmaAddr)
			d.readPhase(addr, validA(addr), phaseDescriptorDone)
		case phaseDescriptorDone:
			c := &d.Channels[s.Channel]
			if c.hdmaLines != 0 {
				d.nextHDMAChannel()
				continue
			}
			c.hdmaAddr++
			c.TableAddr = c.hdmaAddr
			c.LineCount = s.Value
			c.hdmaCompleted = s.Value == 0
			c.Active = !c.hdmaCompleted
			c.hdmaDoTransfer = !c.hdmaCompleted
			c.hdmaRepeat = s.Value&0x80 != 0
			c.hdmaLines = int(s.Value & 0x7f)
			if s.Value != 0 && c.hdmaLines == 0 {
				c.hdmaLines = 128
			}
			if c.Control&0x40 != 0 {
				s.Phase = phaseIndirectLow
			} else {
				d.nextHDMAChannel()
			}
		case phaseIndirectLow, phaseIndirectHigh:
			c := &d.Channels[s.Channel]
			addr := uint32(c.SrcBank)<<16 | uint32(c.hdmaAddr)
			c.hdmaAddr++
			c.TableAddr = c.hdmaAddr
			next := uint8(phaseIndirectLowDone)
			if s.Phase == phaseIndirectHigh {
				next = phaseIndirectHighDone
			}
			d.readPhase(addr, validA(addr), next)
		case phaseIndirectLowDone:
			c := &d.Channels[s.Channel]
			c.hdmaIndirectAddr = uint16(s.Value) << 8
			c.Size = c.hdmaIndirectAddr
			if c.hdmaCompleted && d.hdmaFinished(s.Channel) {
				d.nextHDMAChannel()
			} else {
				s.Phase = phaseIndirectHigh
			}
		case phaseIndirectHighDone:
			c := &d.Channels[s.Channel]
			c.hdmaIndirectAddr = c.hdmaIndirectAddr>>8 | uint16(s.Value)<<8
			c.Size = c.hdmaIndirectAddr
			d.nextHDMAChannel()
		case phaseRead:
			s.Value = 0
			if s.ReadValid {
				s.Value = d.Bus.Read(s.Address)
			}
			d.delay(4, phaseReadDone)
		case phaseReadDone:
			s.Phase = s.Next
		case phaseWrite:
			if s.WriteValid {
				d.Bus.Write(s.Destination, s.Value)
			}
			s.Phase = s.Resume
		}
	}
	return used
}

func (d *DMA) nextHDMAChannel() {
	d.execution.Channel++
	if d.execution.Setup {
		d.execution.Phase = phaseHDMAChannel
	} else {
		d.execution.Phase = phaseHDMAAdvance
	}
}

// ValidateExecution rejects malformed controller continuations before restore.
func ValidateExecution(s ExecutionState) error {
	invalid := func() error { return fmt.Errorf("invalid DMA execution state") }
	if s.Phase > phaseHDMAAlignedReturn || s.Channel < 0 || s.Channel > 8 || s.DMAChannel < 0 || s.DMAChannel > 8 || s.Index < 0 || s.Index > 4 || s.DMAIndex < 0 || s.DMAIndex > 65536 || s.Wait > 12 || s.Address > 0xffffff || s.Destination > 0xffffff {
		return invalid()
	}
	if s.Phase == phaseIdle && (s.Wait != 0 || s.General || s.CPUWait) {
		return invalid()
	}
	if s.Phase != phaseIdle && s.ClockCount != 6 && s.ClockCount != 8 && s.ClockCount != 12 {
		return invalid()
	}
	switch s.Next {
	case 0, phaseWrite, phaseDescriptorDone, phaseIndirectLowDone, phaseIndirectHighDone:
	default:
		return invalid()
	}
	switch s.Resume {
	case 0, phaseGPAfter, phaseHDMAAfter:
	default:
		return invalid()
	}
	switch s.HDMAReturn {
	case 0, phaseFinish, phaseDispatch, phaseGPChannel, phaseGPByte, phaseGPAfterDecrement:
	default:
		return invalid()
	}
	if s.CPUWait != (s.Phase == phaseRelease || s.Phase == phaseHDMAAlignedReturn) {
		return invalid()
	}
	phase := s.Phase
	if phase == phaseRead || phase == phaseReadDone {
		if s.Next == 0 {
			return invalid()
		}
		phase = s.Next
	}
	if phase == phaseWrite {
		if s.Resume == 0 {
			return invalid()
		}
		phase = s.Resume
	}
	hdma := false
	switch phase {
	case phaseHDMAStart, phaseHDMAChannel, phaseHDMAAdvance, phaseHDMAReturn, phaseHDMAAlignedReturn:
		hdma = true
	case phaseHDMAByte, phaseHDMAAfter, phaseDescriptor, phaseDescriptorDone, phaseIndirectLow, phaseIndirectLowDone, phaseIndirectHigh, phaseIndirectHighDone:
		hdma = true
		if s.Channel >= 8 {
			return invalid()
		}
		if (phase == phaseHDMAByte || phase == phaseHDMAAfter) && s.Index >= 4 {
			return invalid()
		}
	case phaseGPFirst, phaseGPByte, phaseGPAfter, phaseGPAfterDecrement:
		if s.DMAChannel >= 8 {
			return invalid()
		}
	}
	if hdma {
		if s.HDMAReturn == 0 {
			return invalid()
		}
		switch s.HDMAReturn {
		case phaseGPChannel, phaseGPByte, phaseGPAfterDecrement:
			if !s.General || ((s.HDMAReturn == phaseGPByte || s.HDMAReturn == phaseGPAfterDecrement) && s.DMAChannel >= 8) {
				return invalid()
			}
		}
	}
	return nil
}
