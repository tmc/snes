package updsp

import (
	"bytes"
	"encoding/gob"
	"fmt"
)

// MapType selects which window the uPD77C25 exposes its DR/SR ports in.
// DSP-1/1B/2/3/4 ship on both LoROM and HiROM boards with distinct
// address windows; the cartridge loader picks one at construction time.
type MapType uint8

const (
	// MapLoROM matches the DSP-1 LoROM board wiring: the DR/SR window
	// occupies $6000-$7FFF in banks $20-$3F and $A0-$BF, with the even
	// address acting as DR and the odd address as SR.
	MapLoROM MapType = iota
	// MapLoROMSmall is the LoROM DSP-1 variant used on <= 1 MiB carts,
	// where the window is at $8000-$FFFF in banks $20-$3F / $A0-$BF.
	MapLoROMSmall
	// MapHiROM is the HiROM DSP-1 variant: DR/SR sits
	// at $6000-$6BFF in banks $00-$1F / $80-$9F.
	MapHiROM
)

// Mapper wraps an IO with the cartridge-side address decode that routes
// the DR/SR ports into the CPU address space. It satisfies the
// cartridge.Coprocessor contract (minus the MapType registration, which
// is done by the cartridge loader after construction).
type Mapper struct {
	IO             *IO
	MapType        MapType
	pal            bool
	clockRemainder uint64
}

// NewMapper returns a Mapper that dispatches to io over the address
// window selected by mt. If io is nil the Mapper returns a passthrough
// that always reads 0 and silently drops writes — handy for running a
// cartridge whose DSP ROM is missing.
func NewMapper(io *IO, mt MapType) *Mapper {
	return &Mapper{IO: io, MapType: mt}
}

// SetPAL selects the console master clock. Changing region clears any pending
// fractional DSP cycle. Configure the region before stepping the mapper.
func (m *Mapper) SetPAL(pal bool) {
	if m.pal != pal {
		m.clockRemainder = 0
	}
	m.pal = pal
}

// masterFrequency follows bsnes sfc/system/system.cpp and sfc/sfc.hpp:
// colorburst * 6 (NTSC) or * 4.8 (PAL), truncated to the thread's uint clock.
func masterFrequency(pal bool) uint64 {
	if pal {
		return 21_281_370
	}
	return 21_477_272
}

// mapped reports whether addr falls inside the active DSP window and,
// if so, returns true together with the bit that distinguishes the DR
// port (even) from the SR port (odd). drPort is true for the DR access
// and false for the SR access.
func (m *Mapper) mapped(addr uint32) (drPort bool, ok bool) {
	bank := (addr >> 16) & 0xFF
	off := uint16(addr & 0xFFFF)
	switch m.MapType {
	case MapLoROM:
		if !((bank >= 0x20 && bank <= 0x3F) || (bank >= 0xA0 && bank <= 0xBF)) {
			return false, false
		}
		if off < 0x6000 || off > 0x7FFF {
			return false, false
		}
	case MapLoROMSmall:
		if !((bank >= 0x20 && bank <= 0x3F) || (bank >= 0xA0 && bank <= 0xBF)) {
			return false, false
		}
		if off < 0x8000 {
			return false, false
		}
	case MapHiROM:
		if !((bank <= 0x1F) || (bank >= 0x80 && bank <= 0x9F)) {
			return false, false
		}
		if off < 0x6000 || off > 0x6BFF {
			return false, false
		}
	default:
		return false, false
	}
	return (addr & 1) == 0, true
}

// Read routes to IO.ReadDR on the even port and IO.ReadSR on the odd
// port. Returns (value, true) when the address falls inside the DSP
// window and (0, false) otherwise so the cartridge can continue to try
// ROM/SRAM decodes.
func (m *Mapper) Read(addr uint32) (uint8, bool) {
	dr, ok := m.mapped(addr)
	if !ok {
		return 0, false
	}
	var v uint8
	if m.IO == nil {
		v = 0
	} else if dr {
		v = m.IO.ReadDR()
	} else {
		v = m.IO.ReadSR()
	}
	if traceEnabled.Load() {
		recordTrace(addr, v, false, dr)
	}
	return v, true
}

// Write routes to IO.WriteDR on the even port. Writes to the odd (SR)
// port are silently dropped on hardware. Returns true when the address
// claimed the write.
func (m *Mapper) Write(addr uint32, val uint8) bool {
	dr, ok := m.mapped(addr)
	if !ok {
		return false
	}
	if m.IO != nil && dr {
		m.IO.WriteDR(val)
	}
	if traceEnabled.Load() {
		recordTrace(addr, val, true, dr)
	}
	return true
}

// Step advances the DSP by the given console master-cycle budget. DSP-1/2/3/4
// use a 7.6 MHz oscillator (bsnes heuristics/super-famicom.cpp). One instruction
// consumes one DSP cycle (sfc/coprocessor/necdsp/necdsp.cpp main).
func (m *Mapper) Step(masterCycles uint64) {
	if m.IO == nil || m.IO.Core == nil || masterCycles == 0 {
		return
	}
	const dspFrequency = 7_600_000
	frequency := masterFrequency(m.pal)
	// Split before multiplying so the clock conversion cannot overflow.
	n := masterCycles / frequency * dspFrequency
	fraction := masterCycles%frequency*dspFrequency + m.clockRemainder
	n += fraction / frequency
	m.clockRemainder = fraction % frequency
	for n > 0 {
		count := n
		if count > uint64(^uint(0)>>1) {
			count = uint64(^uint(0) >> 1)
		}
		m.IO.Core.Step(int(count))
		n -= count
	}
}

type mapperState struct {
	Core           []byte
	SR             uint16
	DR             uint16
	DRHigh         bool
	DRLatch        uint8
	MapType        MapType
	HasCore        bool
	PAL            bool
	ClockRemainder uint64
}

// Serialize captures the mapper's state, including the underlying core.
func (m *Mapper) Serialize() ([]byte, error) {
	s := mapperState{MapType: m.MapType, PAL: m.pal, ClockRemainder: m.clockRemainder}
	if m.IO != nil {
		s.HasCore = m.IO.Core != nil
		if s.HasCore {
			data, err := m.IO.Core.Serialize()
			if err != nil {
				return nil, fmt.Errorf("serialize updsp mapper core: %w", err)
			}
			s.Core = data
			s.SR = m.IO.Core.SR
			s.DR = m.IO.Core.DR
		}
		s.DRHigh = m.IO.drHighNext
		s.DRLatch = m.IO.drLatchHi
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(s); err != nil {
		return nil, fmt.Errorf("serialize updsp mapper: %w", err)
	}
	return buf.Bytes(), nil
}

// Unserialize restores a mapper state previously produced by Serialize.
// The Mapper must already have IO and a Core wired so the restored state
// can be unpacked into them.
func (m *Mapper) Unserialize(data []byte) error {
	var s mapperState
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&s); err != nil {
		return fmt.Errorf("unserialize updsp mapper: %w", err)
	}
	if s.ClockRemainder >= masterFrequency(s.PAL) {
		return fmt.Errorf("unserialize updsp mapper: invalid fractional clock")
	}
	if s.MapType > MapHiROM {
		return fmt.Errorf("unserialize updsp mapper: invalid map type")
	}
	if s.HasCore != (m.IO != nil && m.IO.Core != nil) {
		return fmt.Errorf("unserialize updsp mapper: core configuration mismatch")
	}
	if m.IO == nil {
		m.MapType = s.MapType
		m.pal, m.clockRemainder = s.PAL, s.ClockRemainder
		return nil
	}
	if s.HasCore && m.IO.Core != nil {
		if err := m.IO.Core.Unserialize(s.Core); err != nil {
			return fmt.Errorf("unserialize updsp core: %w", err)
		}
		m.IO.Core.SR = s.SR
		m.IO.Core.DR = s.DR
	}
	m.MapType = s.MapType
	m.pal, m.clockRemainder = s.PAL, s.ClockRemainder
	m.IO.drHighNext = s.DRHigh
	m.IO.drLatchHi = s.DRLatch
	return nil
}

// Power restarts execution and the host transfer protocol, preserving firmware
// and data RAM. See bsnes NECDSP::power and processor uPD96050::power.
func (m *Mapper) Power() {
	m.clockRemainder = 0
	if m.IO == nil || m.IO.Core == nil {
		return
	}
	m.IO.Core.Reset()
	m.IO.drHighNext = false
	m.IO.drLatchHi = 0
}
