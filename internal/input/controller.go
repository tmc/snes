package input

// Controller represents a SNES controller.
type Controller interface {
	// Poll returns the current state of the controller.
	// The state is a 16-bit value where each bit corresponds to a button.
	Poll() uint16

	// Latch updates the controller latch line used by manual serial reads.
	Latch(bool)

	// ReadSerial returns the next serial data bit for manual controller reads.
	ReadSerial() uint8
}

// StandardController implements a standard SNES controller.
type StandardController struct {
	// Buttons stores the current live button state.
	// Bit 15 is B, then Y, Select, Start, Up, Down, Left, Right,
	// A, X, L, R, followed by four zero signature bits.
	Buttons uint16

	latched bool
	shift   uint16
	count   uint8
}

// State captures the observable and latched controller state.
type State struct {
	Buttons uint16
	Latched bool
	Shift   uint16
	Count   uint8
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
	buttons := c.Buttons
	if buttons&(ButtonUp|ButtonDown) == ButtonUp|ButtonDown {
		buttons &^= ButtonUp | ButtonDown
	}
	if buttons&(ButtonLeft|ButtonRight) == ButtonLeft|ButtonRight {
		buttons &^= ButtonLeft | ButtonRight
	}
	return buttons
}

// Latch updates the manual-read latch line.
func (c *StandardController) Latch(enabled bool) {
	if c.latched == enabled {
		return
	}
	c.latched = enabled
	c.count = 0
	if !enabled {
		c.shift = c.Poll()
	}
}

// ReadSerial returns the next bit from the controller shift register.
func (c *StandardController) ReadSerial() uint8 {
	if c.latched {
		return uint8(c.Poll() >> 15)
	}
	if c.count >= 16 {
		return 1
	}
	bit := uint8(c.shift >> 15)
	c.shift <<= 1
	c.count++
	return bit
}

// SetButton sets the state of a specific button.
func (c *StandardController) SetButton(button uint16, pressed bool) {
	if pressed {
		c.Buttons |= button
	} else {
		c.Buttons &^= button
	}
}

// SetState replaces the current button state.
func (c *StandardController) SetState(buttons uint16) {
	c.Buttons = buttons
}

// SaveState returns a snapshot of the controller state.
func (c *StandardController) SaveState() State {
	return State{
		Buttons: c.Buttons,
		Latched: c.latched,
		Shift:   c.shift,
		Count:   c.count,
	}
}

// LoadState restores a previously saved controller state.
func (c *StandardController) LoadState(state State) {
	c.Buttons = state.Buttons
	c.latched = state.Latched
	c.shift = state.Shift
	c.count = state.Count
}
