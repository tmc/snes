package input

// Mouse implements the Nintendo SNES Mouse (SNSP-MO-A).
//
// Serial report is 32 bits, MSB-first (bit 31 is shifted out first by
// ReadSerial):
//
//	31-28: signature 0001
//	27-26: reserved (0)
//	25-24: sensitivity (00 Lo, 01 Mid, 10 Hi)
//	23:    right-button
//	22:    left-button
//	21-16: reserved (0)
//	15:    Y sign (1 = up / negative)
//	14-8:  Y magnitude (7-bit, 0..127)
//	7:     X sign (1 = left / negative)
//	6-0:   X magnitude (7-bit, 0..127)
//
// Sensitivity cycles Lo -> Mid -> Hi -> Lo on a SenseToggle() call. Games
// read the right+left button press as the UI gesture that means "change
// sensitivity", then invoke the sensitivity-select signal; this is the
// sensitivity-toggle quirk documented in implementation_plan.md Phase 8.
//
// Coordinates are snapshotted on the $4016 rising edge (Latch(true)): a
// frame of mouse motion is queued via SetDelta(dx,dy). The report drains
// dx/dy as signed 7-bit magnitudes per latch, clamped to +/-127, with any
// residual retained for the next latch.
//
// Bsnes reference: sfc/controller/mouse/mouse.cpp.
type Mouse struct {
	// LeftButton and RightButton are the live button state.
	LeftButton, RightButton bool

	// Sensitivity is the current sensitivity level (0 Lo, 1 Mid, 2 Hi).
	Sensitivity uint8

	// dxAcc and dyAcc accumulate motion between latches.
	dxAcc, dyAcc int32

	latched bool
	shift   uint32
	count   uint8
}

// MouseState captures observable Mouse state for save/restore.
type MouseState struct {
	LeftButton, RightButton bool
	Sensitivity             uint8
	DXAcc, DYAcc            int32
	Latched                 bool
	Shift                   uint32
	Count                   uint8
}

// Sensitivity levels.
const (
	MouseSensitivityLo  uint8 = 0
	MouseSensitivityMid uint8 = 1
	MouseSensitivityHi  uint8 = 2
)

// NewMouse returns a Mouse at Lo sensitivity with no pending motion.
func NewMouse() *Mouse { return &Mouse{} }

// SetDelta queues a pending motion in pixels. Positive dx means right,
// positive dy means down. Deltas accumulate; SetDelta(0,0) is a no-op.
func (m *Mouse) SetDelta(dx, dy int32) {
	m.dxAcc += dx
	m.dyAcc += dy
}

// SetButton sets the live button state.
func (m *Mouse) SetButton(left, right bool) {
	m.LeftButton = left
	m.RightButton = right
}

// SenseToggle cycles sensitivity Lo -> Mid -> Hi -> Lo.
//
// This is the programmer-visible "sensitivity select" signal described in
// Phase 8: software detects the user gesture (typically L+R held) and
// invokes the toggle, which advances to the next sensitivity level.
func (m *Mouse) SenseToggle() { m.Sensitivity = (m.Sensitivity + 1) % 3 }

// Latch drives the $4016 latch line. On the rising edge the live state is
// sampled into the shift register; falling-edge transitions are ignored.
func (m *Mouse) Latch(enabled bool) {
	if m.latched == enabled {
		return
	}
	m.latched = enabled
	m.count = 0
	if enabled {
		m.shift = m.buildReport()
	}
}

// ReadSerial returns the next bit in the report (LSB of the return byte).
// After 32 bits the line idles high (1), matching hardware.
func (m *Mouse) ReadSerial() uint8 {
	if m.count >= 32 {
		return 1
	}
	bit := uint8((m.shift >> 31) & 1)
	m.shift <<= 1
	m.count++
	return bit
}

// buildReport snapshots live state into the 32-bit serial word.
func (m *Mouse) buildReport() uint32 {
	dx, rx := clampDelta(m.dxAcc)
	dy, ry := clampDelta(m.dyAcc)
	m.dxAcc = rx
	m.dyAcc = ry

	var r uint32
	r |= 0x1 << 28
	r |= uint32(m.Sensitivity&0x3) << 24
	if m.RightButton {
		r |= 1 << 23
	}
	if m.LeftButton {
		r |= 1 << 22
	}
	if dy < 0 {
		r |= 1 << 15
		r |= uint32(-dy&0x7F) << 8
	} else {
		r |= uint32(dy&0x7F) << 8
	}
	if dx < 0 {
		r |= 1 << 7
		r |= uint32(-dx & 0x7F)
	} else {
		r |= uint32(dx & 0x7F)
	}
	return r
}

// clampDelta splits an accumulated delta into the signed 7-bit portion
// emitted this latch (-127..127) and the residual that carries forward.
func clampDelta(v int32) (out, residual int32) {
	switch {
	case v > 127:
		return 127, v - 127
	case v < -127:
		return -127, v + 127
	default:
		return v, 0
	}
}

// SaveState returns a snapshot of the mouse state.
func (m *Mouse) SaveState() MouseState {
	return MouseState{
		LeftButton:  m.LeftButton,
		RightButton: m.RightButton,
		Sensitivity: m.Sensitivity,
		DXAcc:       m.dxAcc,
		DYAcc:       m.dyAcc,
		Latched:     m.latched,
		Shift:       m.shift,
		Count:       m.count,
	}
}

// LoadState restores a previously saved mouse state.
func (m *Mouse) LoadState(s MouseState) {
	m.LeftButton = s.LeftButton
	m.RightButton = s.RightButton
	m.Sensitivity = s.Sensitivity
	m.dxAcc = s.DXAcc
	m.dyAcc = s.DYAcc
	m.latched = s.Latched
	m.shift = s.Shift
	m.count = s.Count
}
