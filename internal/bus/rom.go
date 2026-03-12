package bus

// ROMDevice is a read-only memory device backed by a byte slice.
type ROMDevice struct {
	data []byte
}

func NewROMDevice(data []byte) *ROMDevice {
	return &ROMDevice{data: data}
}

func (d *ROMDevice) Read(address uint32) uint8 {
	// ROM is usually mapped with an offset, but here we assume the slice
	// exactly matches the mapped range or starts at 0 relative to the slice.
	// However, Map maps a range.
	// If we map 8000-FFFF to a 32KB slice, the read address will be 8000-FFFF.
	// We need to mask it or handle offsets.
	// Simplest approach: The slice represents the linear content.
	// But `Read` receives the full address.

	// For now, let's assume the calling glue handles offset or we just mask.
	// In MapToBus, we are mapping chunks.
	// If we map bank 00, 8000-FFFF, we passed a 32KB slice.
	// A read at 00:8000 should access slice[0].

	// A generic ROM implementation might span multiple banks.
	// But here we are instantiating small devices per chunk.
	// So we should just use address modulo length?
	// Or simpler: (address & mask).

	// Let's use generic length check.
	// If the slice is 32KB, we mask lower 15 bits?
	// 32KB = 0x8000.

	if len(d.data) == 0 {
		return 0
	}

	// We assume the device is mapped such that 'address' falls within its logic.
	// Since we pass 32KB chunks mapped to 32KB windows,
	// we can just use `address % len`.
	return d.data[address%uint32(len(d.data))]
}

func (d *ROMDevice) Write(address uint32, value uint8) {
	// Read-only
}

func (d *ROMDevice) BlockRead(address uint32, length int) []byte {
	if length <= 0 {
		return nil
	}
	buf := make([]byte, length)
	for i := range buf {
		buf[i] = d.Read(address + uint32(i))
	}
	return buf
}
