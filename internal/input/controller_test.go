package input

import "testing"

func TestStandardController_SetButton(t *testing.T) {
	c := NewStandardController()

	if c.Poll() != 0 {
		t.Errorf("expected 0, got %04X", c.Poll())
	}

	c.SetButton(ButtonA, true)
	if c.Poll() != ButtonA {
		t.Errorf("expected %04X, got %04X", ButtonA, c.Poll())
	}

	c.SetButton(ButtonB, true)
	if c.Poll() != (ButtonA | ButtonB) {
		t.Errorf("expected %04X, got %04X", ButtonA|ButtonB, c.Poll())
	}

	c.SetButton(ButtonA, false)
	if c.Poll() != ButtonB {
		t.Errorf("expected %04X, got %04X", ButtonB, c.Poll())
	}
}

func TestStandardController_ReadSerial(t *testing.T) {
	c := NewStandardController()
	c.SetButton(ButtonB, true)
	c.SetButton(ButtonStart, true)
	c.SetButton(ButtonR, true)

	c.Latch(true)
	if got := c.ReadSerial(); got != 1 {
		t.Fatalf("latched read = %d, want 1", got)
	}

	c.Latch(false)

	want := []uint8{
		1,          // B
		0,          // Y
		0,          // Select
		1,          // Start
		0,          // Up
		0,          // Down
		0,          // Left
		0,          // Right
		0,          // A
		0,          // X
		0,          // L
		1,          // R
		0, 0, 0, 0, // Signature
		1, 1, // Reads after the signature return 1
	}
	for i, want := range want {
		if got := c.ReadSerial(); got != want {
			t.Fatalf("read %d = %d, want %d", i, got, want)
		}
	}
}

func TestStandardController_PollFiltersOpposingDirections(t *testing.T) {
	c := NewStandardController()
	c.SetButton(ButtonUp, true)
	c.SetButton(ButtonDown, true)
	c.SetButton(ButtonLeft, true)
	c.SetButton(ButtonRight, true)
	c.SetButton(ButtonA, true)

	got := c.Poll()
	want := uint16(ButtonA)
	if got != want {
		t.Fatalf("poll = %04X, want %04X", got, want)
	}
}
