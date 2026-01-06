package bus

import (
	"testing"
)

type MockDevice struct {
	data map[uint32]uint8
}

func NewMockDevice() *MockDevice {
	return &MockDevice{data: make(map[uint32]uint8)}
}

func (m *MockDevice) Read(address uint32) uint8 {
	return m.data[address]
}
func (m *MockDevice) Write(address uint32, value uint8) {
	m.data[address] = value
}
func (m *MockDevice) BlockRead(address uint32, length int) []byte {
	return nil
}

func TestBus_MapAndRead(t *testing.T) {
	b := NewBus()
	dev := NewMockDevice()

	// Map Bank 00, Page 10 ($001000 - $0010FF)
	b.Map(0x001000, 0x0010FF, dev)

	// Write value to device directly
	dev.Write(0x001050, 0x42)

	// Read via Bus
	val := b.Read(0x001050)
	if val != 0x42 {
		t.Errorf("Expected 0x42, got 0x%X", val)
	}

	// Verify MDR update
	if b.MDR != 0x42 {
		t.Errorf("MDR not updated. Expected 0x42, got 0x%X", b.MDR)
	}
}

func TestBus_OpenBus(t *testing.T) {
	b := NewBus()
	// Unmapped read
	// Should be 0 initially or previous MDR
	b.MDR = 0xAA
	val := b.Read(0x000000)
	if val != 0xAA {
		t.Errorf("OpenBus should return MDR. Expected 0xAA, got 0x%X", val)
	}
}

func TestBus_WaitStates(t *testing.T) {
	b := NewBus()

	tests := []struct {
		name string
		addr uint32
		want uint64
	}{
		{"WRAM Low", 0x7E0000, 8},
		{"WRAM High", 0x7F0000, 8},
		{"SlowROM (Bank 80)", 0x808000, 8}, // Default is SlowROM
		{"I/O", 0x002100, 6},
		{"Joypad", 0x004016, 12}, // Override
		{"Joypad", 0x004017, 12},
	}

	for _, tt := range tests {
		got := b.GetWaitStates(tt.addr)
		if got != tt.want {
			t.Errorf("%s: at %06X expected %d, got %d", tt.name, tt.addr, tt.want, got)
		}
	}
}

func TestBus_MEMSEL(t *testing.T) {
	b := NewBus()

	// Default SlowROM
	if ws := b.GetWaitStates(0x808000); ws != 8 {
		t.Errorf("Default should be SlowROM (8). Got %d", ws)
	}

	// Enable FastROM
	b.WriteMEMSEL(1)
	if ws := b.GetWaitStates(0x808000); ws != 6 {
		t.Errorf("FastROM should be 6. Got %d", ws)
	}

	// Disable FastROM
	b.WriteMEMSEL(0)
	if ws := b.GetWaitStates(0x808000); ws != 8 {
		t.Errorf("SlowROM should be 8. Got %d", ws)
	}
}
