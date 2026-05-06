package bus

import "fmt"

// RAMDevice is a read-write memory device backed by a byte slice.
type RAMDevice struct {
	data []byte
	wram bool
}

func NewRAMDevice(size int) *RAMDevice {
	d := &RAMDevice{data: make([]byte, size)}
	for i := range d.data {
		d.data[i] = 0x55
	}
	return d
}

// NewWRAMDevice returns a RAM device with SNES internal WRAM mirror addressing.
func NewWRAMDevice() *RAMDevice {
	return &RAMDevice{
		data: make([]byte, 128*1024),
		wram: true,
	}
}

func (d *RAMDevice) offset(address uint32) uint32 {
	if !d.wram {
		return address % uint32(len(d.data))
	}
	switch bank := address >> 16; {
	case bank == 0x7E || bank == 0x7F:
		return address & 0x1FFFF
	case address&0xFFFF < 0x2000:
		return address & 0x1FFF
	}
	return address % uint32(len(d.data))
}

func (d *RAMDevice) Read(address uint32) uint8 {
	if len(d.data) == 0 {
		return 0
	}
	return d.data[d.offset(address)]
}

func (d *RAMDevice) Write(address uint32, value uint8) {
	if len(d.data) == 0 {
		return
	}
	offset := d.offset(address)
	if offset >= 0x800 && offset < 0x820 { // Check first 32 bytes of OAM Buffer
		// fmt.Printf("WRAM Write [$%06X -> %05X] = %02X\n", address, offset, value)
	}
	d.data[offset] = value
}

func (d *RAMDevice) BlockRead(address uint32, length int) []byte {
	if length <= 0 {
		return nil
	}
	buf := make([]byte, length)
	for i := range buf {
		buf[i] = d.Read(address + uint32(i))
	}
	return buf
}

// Data returns a copy of the RAM contents.
func (d *RAMDevice) Data() []byte {
	return append([]byte(nil), d.data...)
}

// LoadData replaces the RAM contents with data.
func (d *RAMDevice) LoadData(data []byte) error {
	if len(data) != len(d.data) {
		return fmt.Errorf("ram size mismatch: got %d bytes, want %d", len(data), len(d.data))
	}
	copy(d.data, data)
	return nil
}
