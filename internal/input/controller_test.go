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
