package apu

import "testing"

func TestControlClearsInputPortsOnBit4AndBit5(t *testing.T) {
	a := NewAPU()
	a.InPorts = [4]uint8{0x11, 0x22, 0x33, 0x44}

	a.Write(0x00F1, 0x10)
	if a.InPorts[0] != 0 || a.InPorts[1] != 0 {
		t.Fatalf("ports 0/1 not cleared: %02X %02X", a.InPorts[0], a.InPorts[1])
	}
	if a.InPorts[2] != 0x33 || a.InPorts[3] != 0x44 {
		t.Fatalf("ports 2/3 changed unexpectedly: %02X %02X", a.InPorts[2], a.InPorts[3])
	}

	a.InPorts = [4]uint8{0x55, 0x66, 0x77, 0x88}
	a.Write(0x00F1, 0x20)
	if a.InPorts[2] != 0 || a.InPorts[3] != 0 {
		t.Fatalf("ports 2/3 not cleared: %02X %02X", a.InPorts[2], a.InPorts[3])
	}
	if a.InPorts[0] != 0x55 || a.InPorts[1] != 0x66 {
		t.Fatalf("ports 0/1 changed unexpectedly: %02X %02X", a.InPorts[0], a.InPorts[1])
	}
}

func TestBootHandshakePorts(t *testing.T) {
	a := NewAPU()
	a.Power(true)

	if got := a.ReadPort(0); got != 0x00 {
		t.Fatalf("port0 initial value = %02X, want 00", got)
	}
	if got := a.ReadPort(1); got != 0x00 {
		t.Fatalf("port1 initial value = %02X, want 00", got)
	}

	// CPU->APU port writes should only update InPorts.
	a.WritePort(0, 0xCC)
	if a.InPorts[0] != 0xCC {
		t.Fatalf("in port0 = %02X, want CC", a.InPorts[0])
	}
	if got := a.ReadPort(0); got != 0x00 {
		t.Fatalf("port0 should reflect OutPorts only, got %02X", got)
	}

	// APU-side MMIO writes should be visible on CPU port reads.
	a.Write(0x00F4, 0xAA)
	a.Write(0x00F5, 0xBB)
	if got := a.ReadPort(0); got != 0xAA {
		t.Fatalf("port0 after apu write = %02X, want AA", got)
	}
	if got := a.ReadPort(1); got != 0xBB {
		t.Fatalf("port1 after apu write = %02X, want BB", got)
	}
}
