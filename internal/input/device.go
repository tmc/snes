package input

// Device is the minimum contract a controller must satisfy to be driven by
// the $4016/$4017 serial read protocol.
//
// StandardController, Mouse, SuperScope, and Multitap all implement Device.
// Bus read handlers that do not care about Poll() (e.g. a mouse, which has
// no static 16-bit button word) should type-assert against Device rather
// than Controller.
type Device interface {
	// Latch updates the latch line. On a rising edge, the device samples
	// its live state into its internal shift register.
	Latch(bool)

	// ReadSerial returns the next serial bit (LSB of the returned byte).
	// Calls past the end of the report return 1, matching the 1-filled
	// idle state of an unplugged or exhausted line.
	ReadSerial() uint8
}

// Ensure StandardController, Mouse, SuperScope, and Multitap satisfy Device.
var (
	_ Device = (*StandardController)(nil)
	_ Device = (*Mouse)(nil)
	_ Device = (*SuperScope)(nil)
	_ Device = (*Multitap)(nil)
)
