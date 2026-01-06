package bus

// RAMDevice is a read-write memory device backed by a byte slice.
type RAMDevice struct {
	data []byte
}

func NewRAMDevice(size int) *RAMDevice {
	d := &RAMDevice{data: make([]byte, size)}
	for i := range d.data {
		d.data[i] = 0x55
	}
	return d
}

func (d *RAMDevice) Read(address uint32) uint8 {
	// Determine offset.
	// Since Map calls might not adjust address, we usually expect mapped ranges.
	// However, if we map the same device to multiple ranges, we need to handle wrapping.
	// For simple RAM, we just modulo the size.
	if len(d.data) == 0 {
		return 0
	}
	return d.data[address%uint32(len(d.data))]
}

func (d *RAMDevice) Write(address uint32, value uint8) {
	if len(d.data) == 0 {
		return
	}
	offset := address % uint32(len(d.data))
	if offset >= 0x800 && offset < 0x820 { // Check first 32 bytes of OAM Buffer
		// fmt.Printf("WRAM Write [$%06X -> %05X] = %02X\n", address, offset, value)
	}
	d.data[offset] = value
}

func (d *RAMDevice) BlockRead(address uint32, length int) []byte {
	// Stub
	return nil
}
