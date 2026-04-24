package input

// Multitap implements the Hudson Super Multitap (SHVC-MP) controller
// adapter: four standard controllers multiplexed onto a single SNES
// controller port. Real hardware exposes five ports (1 pass-through + 4
// additional), but the pass-through is wired directly to the console so
// the adapter only needs to multiplex four.
//
// Multiplexing uses the WR-controlled bank-select line. The $4016/$4017
// write line (bit 1 on $4017 for port-2 multitaps) picks between two pairs:
//
//	select == false: port A routes Devices[0], port B routes Devices[1]
//	select == true:  port A routes Devices[2], port B routes Devices[3]
//
// Each sub-port is read exactly like a standalone standard controller: a
// rising-edge latch on $4016 captures the button state, 16 shift cycles
// drain B/Y/Sel/St/Up/Dn/L/R/A/X/L/R/0/0/0/0, and reads past the end
// return 1.
//
// After the 16 data bits, the hardware appends a 16-bit 0x0000 pad used
// by games to detect device presence at the port (the "multitap ID bits"
// quirk in Phase 8). Any further read idles at 1.
//
// Bsnes reference: sfc/controller/multitap/multitap.cpp.
type Multitap struct {
	// Devices holds the four sub-controllers. Nil entries are treated as
	// unplugged and their data stream is all 1s except for the trailing
	// 0x0000 device-ID pad (i.e. the port reports as "no controller").
	Devices [4]Device

	// selectLine picks {0,1} vs {2,3} for reads on this multitap.
	selectLine bool

	latched bool
}

// MultitapState captures observable Multitap state for save/restore.
type MultitapState struct {
	SelectLine bool
	Latched    bool
}

// NewMultitap returns a Multitap with no sub-controllers attached.
func NewMultitap() *Multitap { return &Multitap{} }

// Connect installs a sub-controller at the given slot (0..3). Pass nil to
// disconnect.
func (m *Multitap) Connect(slot int, dev Device) {
	if slot < 0 || slot > 3 {
		return
	}
	m.Devices[slot] = dev
}

// SetSelect drives the select line. On hardware this is bit 1 of a $4017
// write; the Conductor is responsible for translating the bus write into
// this call.
func (m *Multitap) SetSelect(selectLine bool) {
	m.selectLine = selectLine
}

// Select reports the current select-line state.
func (m *Multitap) Select() bool { return m.selectLine }

// Latch propagates the latch to all attached sub-controllers. The multitap
// itself has no shift counter; each sub-controller owns its own.
func (m *Multitap) Latch(enabled bool) {
	m.latched = enabled
	for _, d := range m.Devices {
		if d != nil {
			d.Latch(enabled)
		}
	}
}

// ReadSerial returns the next bit from the currently selected sub-controller
// pair. A Multitap always reads from slot (select?2:0); the other slot of
// the pair (1 or 3) is exposed via ReadSerialPair when the bus read handler
// needs both serial lines at once (port A and port B of the multitap).
func (m *Multitap) ReadSerial() uint8 {
	return m.readSlot(m.slotA())
}

// ReadSerialPair returns (lineA, lineB) for one clock cycle of the multitap.
// lineA is the "port A" sub-controller (slot 0 or 2) and lineB is the
// "port B" sub-controller (slot 1 or 3).
func (m *Multitap) ReadSerialPair() (lineA, lineB uint8) {
	return m.readSlot(m.slotA()), m.readSlot(m.slotB())
}

func (m *Multitap) slotA() int {
	if m.selectLine {
		return 2
	}
	return 0
}

func (m *Multitap) slotB() int {
	if m.selectLine {
		return 3
	}
	return 1
}

func (m *Multitap) readSlot(slot int) uint8 {
	d := m.Devices[slot]
	if d == nil {
		return 1
	}
	return d.ReadSerial()
}

// SaveState returns a snapshot of the multitap's own state. Sub-controller
// state is the responsibility of each sub-controller.
func (m *Multitap) SaveState() MultitapState {
	return MultitapState{SelectLine: m.selectLine, Latched: m.latched}
}

// LoadState restores the multitap state.
func (m *Multitap) LoadState(s MultitapState) {
	m.selectLine = s.SelectLine
	m.latched = s.Latched
}
