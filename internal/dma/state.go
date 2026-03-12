package dma

// DMAState captures the serializable DMA controller state.
type DMAState struct {
	Channels   [8]Channel
	Enable     uint8
	HDMAEnable uint8
}

// SaveState returns a snapshot of the DMA controller state.
func (d *DMA) SaveState() DMAState {
	return DMAState{
		Channels:   d.Channels,
		Enable:     d.Enable,
		HDMAEnable: d.HDMAEnable,
	}
}

// LoadState restores a previously saved DMA controller state.
func (d *DMA) LoadState(state DMAState) {
	d.Channels = state.Channels
	d.Enable = state.Enable
	d.HDMAEnable = state.HDMAEnable
}
