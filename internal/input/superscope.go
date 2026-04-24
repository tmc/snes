package input

// BeamLatcher is the hook the Super Scope uses to latch the PPU's H/V
// counters at the dot the light pen "fires." The SNES hardware implements
// this by pulsing WRIO which causes the PPU to latch OPHCT/OPVCT readable
// via $213F; the equivalent wiring in this emulator is a Conductor-installed
// PPU hook, see TODO in the agent handoff.
//
// Returning (h, v) in dot/scanline coordinates (0..339 horizontal,
// 0..261/311 vertical for NTSC/PAL).
type BeamLatcher interface {
	LatchBeam() (h, v uint16)
}

// BeamLatcherFunc adapts a bare function to the BeamLatcher interface.
type BeamLatcherFunc func() (h, v uint16)

// LatchBeam implements BeamLatcher.
func (f BeamLatcherFunc) LatchBeam() (h, v uint16) { return f() }

// SuperScope implements the Nintendo Super Scope (SNSP-SCOPE-A).
//
// Serial report is 32 bits, MSB-first:
//
//	31:    trigger (button)
//	30:    cursor (button)
//	29:    turbo latch (button)
//	28:    pause (button)
//	27:    off-screen flag (1 when the latched beam position is outside the
//	       active display area or no latch has been taken)
//	26:    noise flag (pseudo-noise bit that cycles with internal counter,
//	       matching the bsnes "set on specific frames" quirk)
//	25-16: X beam position bits 9..0 (10 bits, but packed to match bsnes
//	       read order; low byte first is also sometimes observed)
//	15-0:  Y beam position bits, right-aligned into the low 9 bits
//
// The trigger is edge-sensitive: a pull (transition from not-pressed to
// pressed) asks the PPU for a beam-position latch; the captured (h, v) pair
// is retained in the report until the next trigger pull.
//
// Bsnes reference: sfc/controller/super-scope/super-scope.cpp.
type SuperScope struct {
	// Live button state.
	Trigger, Cursor, Turbo, Pause bool

	// Latcher supplies the PPU beam position. If nil, the report always
	// carries the off-screen flag and H/V = 0. Callers wire this in at
	// construction time.
	Latcher BeamLatcher

	// trigPrev tracks the trigger state from the prior Latch(true) in order
	// to detect a trigger pull (edge).
	trigPrev bool

	// latchedH, latchedV hold the most recent beam capture; offscreen
	// tracks whether that capture is to be reported as off-screen (no
	// capture yet, or beam was outside the visible window).
	latchedH, latchedV uint16
	offscreen          bool

	// frame counts $4016 latch strobes since construction; the noise bit
	// cycles at a fixed cadence against it, matching the bsnes quirk that
	// it "is set on certain frame counts".
	frame uint32

	latched bool
	shift   uint32
	count   uint8
}

// SuperScopeState captures observable Super Scope state for save/restore.
type SuperScopeState struct {
	Trigger, Cursor, Turbo, Pause bool
	TrigPrev                      bool
	LatchedH, LatchedV            uint16
	Offscreen                     bool
	Frame                         uint32
	Latched                       bool
	Shift                         uint32
	Count                         uint8
}

// NewSuperScope returns a SuperScope that will call latcher on each trigger
// pull to sample the PPU beam position. Pass a nil latcher in contexts
// (e.g. unit tests that do not care about the captured coordinates) where
// the beam position is unimportant; the off-screen flag will then always be
// set.
func NewSuperScope(latcher BeamLatcher) *SuperScope {
	return &SuperScope{Latcher: latcher, offscreen: true}
}

// SetButtons replaces the live button state.
func (s *SuperScope) SetButtons(trigger, cursor, turbo, pause bool) {
	s.Trigger = trigger
	s.Cursor = cursor
	s.Turbo = turbo
	s.Pause = pause
}

// Latch drives the $4016 latch line. On the rising edge the device:
//  1. detects a trigger pull (edge on Trigger),
//  2. if pulled and a latcher is connected, samples (h, v) from the PPU,
//  3. snapshots the current state into the shift register.
//
// Falling-edge transitions reset the shift counter but do not re-sample.
func (s *SuperScope) Latch(enabled bool) {
	if s.latched == enabled {
		return
	}
	s.latched = enabled
	s.count = 0
	if !enabled {
		return
	}
	s.frame++
	// Trigger pull: sample beam via latcher, if present.
	if s.Trigger && !s.trigPrev {
		if s.Latcher != nil {
			h, v := s.Latcher.LatchBeam()
			s.latchedH = h
			s.latchedV = v
			s.offscreen = !beamOnScreen(h, v)
		} else {
			s.offscreen = true
		}
	}
	s.trigPrev = s.Trigger
	s.shift = s.buildReport()
}

// ReadSerial returns the next bit of the 32-bit report.
func (s *SuperScope) ReadSerial() uint8 {
	if s.count >= 32 {
		return 1
	}
	bit := uint8((s.shift >> 31) & 1)
	s.shift <<= 1
	s.count++
	return bit
}

// buildReport packs the current report word.
func (s *SuperScope) buildReport() uint32 {
	var r uint32
	if s.Trigger {
		r |= 1 << 31
	}
	if s.Cursor {
		r |= 1 << 30
	}
	if s.Turbo {
		r |= 1 << 29
	}
	if s.Pause {
		r |= 1 << 28
	}
	if s.offscreen {
		r |= 1 << 27
	}
	if s.noiseBit() {
		r |= 1 << 26
	}
	// X beam in bits 25..16; Y beam in bits 15..0.
	r |= uint32(s.latchedH&0x03FF) << 16
	r |= uint32(s.latchedV & 0x01FF)
	return r
}

// noiseBit is the pseudo-noise bit documented in bsnes; toggled on a fixed
// cadence against the internal frame counter so that software can measure a
// stable noise pattern. We toggle every 4 latches, matching the order of
// magnitude in bsnes' counter mask.
func (s *SuperScope) noiseBit() bool {
	return s.frame&0x4 != 0
}

// beamOnScreen reports whether an (h, v) dot position is inside the 256x224
// active display area for NTSC. The check is conservative — software that
// inspects the off-screen flag does not distinguish PAL vs NTSC; it only
// cares that a trigger aimed at the bezel reads as off-screen.
func beamOnScreen(h, v uint16) bool {
	return h < 256 && v > 0 && v <= 224
}

// SaveState returns a snapshot of the Super Scope state.
func (s *SuperScope) SaveState() SuperScopeState {
	return SuperScopeState{
		Trigger:   s.Trigger,
		Cursor:    s.Cursor,
		Turbo:     s.Turbo,
		Pause:     s.Pause,
		TrigPrev:  s.trigPrev,
		LatchedH:  s.latchedH,
		LatchedV:  s.latchedV,
		Offscreen: s.offscreen,
		Frame:     s.frame,
		Latched:   s.latched,
		Shift:     s.shift,
		Count:     s.count,
	}
}

// LoadState restores a previously saved Super Scope state.
func (s *SuperScope) LoadState(st SuperScopeState) {
	s.Trigger = st.Trigger
	s.Cursor = st.Cursor
	s.Turbo = st.Turbo
	s.Pause = st.Pause
	s.trigPrev = st.TrigPrev
	s.latchedH = st.LatchedH
	s.latchedV = st.LatchedV
	s.offscreen = st.Offscreen
	s.frame = st.Frame
	s.latched = st.Latched
	s.shift = st.Shift
	s.count = st.Count
}
