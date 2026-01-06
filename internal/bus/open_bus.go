package bus

// OpenBusDevice represents unmapped memory.
// It returns the current MDR (Memory Data Register) value when read.
type OpenBusDevice struct {
	bus *Bus
}

func NewOpenBusDevice(bus *Bus) *OpenBusDevice {
	return &OpenBusDevice{bus: bus}
}

func (d *OpenBusDevice) Read(address uint32) uint8 {
	return d.bus.MDR
}

func (d *OpenBusDevice) Write(address uint32, value uint8) {
	// Writes to open bus do nothing but update MDR (which is handled by Bus.Write)
}

func (d *OpenBusDevice) BlockRead(address uint32, length int) []byte {
	// Return a repeating sequence of MDR
	data := make([]byte, length)
	for i := range data {
		data[i] = d.bus.MDR
	}
	return data
}
