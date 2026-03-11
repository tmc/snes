package input

// Controller represents a SNES controller.
type Controller interface {
	// Poll returns the current state of the controller.
	// The state is a 16-bit value where each bit corresponds to a button.
	Poll() uint16
}

// StandardController implements a standard SNES controller.
type StandardController struct {
	// Button state (0=Pressed, 1=Released for hardware, but we'll store logic high = pressed usually, then invert for serial)
	// Actually, SNES serial protocol shifts out data.
	// Standard Controller:
	// B, Y, Select, Start, Up, Down, Left, Right, A, X, L, R, (4 unused/1s)

	// We'll store the raw button state suitable for shifting.
	// 1 = Pressed? No, usually 0=Pressed in register/bus logic often, but let's check docs.
	// $4218/$4219/Auto-Joypad: 1=Pressed.
	// Serial Latch: 1=Pressed?
	// Let's stick to: internal state 1 = Pressed.
	Buttons uint16
}

const (
	ButtonB      = 1 << 15
	ButtonY      = 1 << 14
	ButtonSelect = 1 << 13
	ButtonStart  = 1 << 12
	ButtonUp     = 1 << 11
	ButtonDown   = 1 << 10
	ButtonLeft   = 1 << 9
	ButtonRight  = 1 << 8
	ButtonA      = 1 << 7
	ButtonX      = 1 << 6
	ButtonL      = 1 << 5
	ButtonR      = 1 << 4
)

func NewStandardController() *StandardController {
	return &StandardController{}
}

func (c *StandardController) Poll() uint16 {
	return c.Buttons
}

// SetButton sets the state of a specific button.
func (c *StandardController) SetButton(button uint16, pressed bool) {
	if pressed {
		c.Buttons |= button
	} else {
		c.Buttons &^= button
	}
}
