package bus

import "testing"

func TestWRAMDevicePowerOnZeroed(t *testing.T) {
	ram := NewWRAMDevice()

	for _, address := range []uint32{0x000004, 0x7E0004, 0x7F0004} {
		if got := ram.Read(address); got != 0 {
			t.Fatalf("read %06X = %02X, want 00", address, got)
		}
	}
}

func TestWRAMDeviceLowMirror(t *testing.T) {
	ram := NewWRAMDevice()
	ram.Write(0x091F41, 0xD2)

	if got := ram.Read(0x001F41); got != 0xD2 {
		t.Fatalf("low mirror read = %02X, want D2", got)
	}
	if got := ram.Read(0x7E1F41); got != 0xD2 {
		t.Fatalf("bank 7e read = %02X, want D2", got)
	}
}

func TestRAMDeviceKeepsModuloAddressing(t *testing.T) {
	ram := NewRAMDevice(0x20000)
	if got := ram.Read(0x000004); got != 0x55 {
		t.Fatalf("plain ram power-on read = %02X, want 55", got)
	}
	ram.Write(0x091F41, 0xD2)

	if got := ram.Read(0x011F41); got != 0xD2 {
		t.Fatalf("modulo read = %02X, want D2", got)
	}
	if got := ram.Read(0x001F41); got == 0xD2 {
		t.Fatalf("plain ram unexpectedly mirrored low address")
	}
}
