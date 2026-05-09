package dma

// ChannelHDMAState captures the per-channel HDMA-internal phase state
// that the gob-mediated systemState round-trip would otherwise drop.
// Channel itself holds these fields under unexported names because
// they are runtime-only from the bus' POV; this exported snapshot is
// the serialization shape used by SaveState/LoadState so a save mid-
// frame resumes with the running HDMA pointer/line/repeat/transfer/
// completed phase intact.
type ChannelHDMAState struct {
	HDMAAddr         uint16
	HDMAIndirectAddr uint16
	HDMALines        int
	HDMARepeat       bool
	HDMADoTransfer   bool
	HDMACompleted    bool
}

// DMAState captures the serializable DMA controller state.
type DMAState struct {
	Channels   [8]Channel
	HDMA       [8]ChannelHDMAState
	Enable     uint8
	HDMAEnable uint8
}

// SaveState returns a snapshot of the DMA controller state.
func (d *DMA) SaveState() DMAState {
	state := DMAState{
		Channels:   d.Channels,
		Enable:     d.Enable,
		HDMAEnable: d.HDMAEnable,
	}
	for i := range d.Channels {
		c := &d.Channels[i]
		state.HDMA[i] = ChannelHDMAState{
			HDMAAddr:         c.hdmaAddr,
			HDMAIndirectAddr: c.hdmaIndirectAddr,
			HDMALines:        c.hdmaLines,
			HDMARepeat:       c.hdmaRepeat,
			HDMADoTransfer:   c.hdmaDoTransfer,
			HDMACompleted:    c.hdmaCompleted,
		}
	}
	return state
}

// LoadState restores a previously saved DMA controller state.
func (d *DMA) LoadState(state DMAState) {
	d.Channels = state.Channels
	d.Enable = state.Enable
	d.HDMAEnable = state.HDMAEnable
	for i := range d.Channels {
		c := &d.Channels[i]
		s := state.HDMA[i]
		c.hdmaAddr = s.HDMAAddr
		c.hdmaIndirectAddr = s.HDMAIndirectAddr
		c.hdmaLines = s.HDMALines
		c.hdmaRepeat = s.HDMARepeat
		c.hdmaDoTransfer = s.HDMADoTransfer
		c.hdmaCompleted = s.HDMACompleted
	}
}
