package bus

import "testing"

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
	ram.Write(0x091F41, 0xD2)

	if got := ram.Read(0x011F41); got != 0xD2 {
		t.Fatalf("modulo read = %02X, want D2", got)
	}
	if got := ram.Read(0x001F41); got == 0xD2 {
		t.Fatalf("plain ram unexpectedly mirrored low address")
	}
}
