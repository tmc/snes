package updsp

// Status register bits visible to the SNES CPU side of the DR/SR interface.
//
// The physical uPD7725/77C25 SR is 16 bits; the DSP-1 board exposes the high
// byte over the odd-address read. These bit names follow the NEC datasheet:
//
//	RQM  (bit 15) - Request for master: 1 = DSP is ready for the next half of
//	                a 16-bit DR transfer. Cleared after each half-access.
//	USF1 (bit 14) - User flag 1 (DSP programmable).
//	USF0 (bit 13) - User flag 0 (DSP programmable).
//	DRS  (bit 12) - DR status: 1 after the first byte of a 16-bit transfer.
//	DMA  (bit 11) - DMA mode bit (not driven by SNES code in practice).
//	DRC  (bit 10) - DR control: 0 when the next DR transfer is a 16-bit
//	                access; 1 when 8-bit.
//	SOC  (bit  9) - Serial-out control (not driven by SNES code).
//	SIC  (bit  8) - Serial-in  control (not driven by SNES code).
//	EI   (bit  7) - Enable interrupts (DSP side).
//	P0   (bit  1) - User programmable.
//	P1   (bit  0) - User programmable.
const (
	srRQM  uint16 = 1 << 15
	srUSF1 uint16 = 1 << 14
	srUSF0 uint16 = 1 << 13
	srDRS  uint16 = 1 << 12
	srDMA  uint16 = 1 << 11
	srDRC  uint16 = 1 << 10
	srSOC  uint16 = 1 << 9
	srSIC  uint16 = 1 << 8
	srEI   uint16 = 1 << 7
	srP0   uint16 = 1 << 1
	srP1   uint16 = 1 << 0
)

// IO couples a Core to the CPU-side DR/SR register protocol.
//
// The SNES CPU sees two byte addresses inside the DSP window: the even
// address is the DR port and the odd address is the SR port. DR transfers
// are logically 16-bit but physically happen as a low byte then high byte.
// IO keeps track of which half is next and updates SR bit 15 (RQM) so the
// game code can poll for readiness between byte transfers.
type IO struct {
	Core *Core

	// drHighNext is true when the next DR byte read/written is the high
	// byte. It flips on every DR-byte access.
	drHighNext bool

	// drLatchHi holds the high byte buffered for a 16-bit transfer that
	// goes low-first then high. On write: the CPU writes low first, then
	// high; the second write completes the 16-bit value into Core.DR.
	// On read: the high byte is latched at the moment the DSP wrote DR, so
	// a CPU that reads low-then-high observes a stable snapshot.
	drLatchHi uint8
}

// NewIO returns an IO wired to the given core. The core starts with RQM
// asserted so the first CPU write is accepted immediately.
func NewIO(core *Core) *IO {
	io := &IO{Core: core}
	io.Core.SR |= srRQM
	return io
}

// ReadDR returns the next byte of a pending DR transfer and flips the
// half-pointer. ReadDR does not check RQM; the caller is expected to have
// polled ReadSR before reading, but the function must not block so that a
// buggy game reading mid-transfer still observes deterministic bytes.
func (io *IO) ReadDR() uint8 {
	if io.drHighNext {
		v := uint8(io.Core.DR >> 8)
		// Second half consumed: clear RQM until the DSP writes DR again.
		io.Core.SR &^= srRQM
		io.Core.SR &^= srDRS
		// Next read starts a new 16-bit transfer -> low byte first.
		io.drHighNext = false
		return v
	}
	// First half of a 16-bit read: snapshot the high byte so the next read
	// is stable even if the DSP program updates DR in between.
	io.drLatchHi = uint8(io.Core.DR >> 8)
	io.drHighNext = true
	io.Core.SR |= srDRS
	return uint8(io.Core.DR)
}

// WriteDR accepts the next byte of a pending DR transfer. On the second byte
// the 16-bit value is committed to Core.DR and RQM is cleared until the DSP
// program updates DR again.
func (io *IO) WriteDR(v uint8) {
	if io.drHighNext {
		// Commit the 16-bit value.
		io.Core.DR = (io.Core.DR & 0x00FF) | (uint16(v) << 8)
		io.Core.SR &^= srRQM
		io.Core.SR &^= srDRS
		io.drHighNext = false
		return
	}
	io.Core.DR = (io.Core.DR & 0xFF00) | uint16(v)
	io.drHighNext = true
	io.Core.SR |= srDRS
}

// ReadSR returns the high byte of SR (the visible half on the SNES DSP
// board). The high byte carries RQM, USF1, USF0, DRS, DMA, DRC.
func (io *IO) ReadSR() uint8 {
	sr := io.Core.SR
	if sr&srDRC != 0 {
		sr &^= srDRS
	}
	return uint8(sr >> 8)
}

// ResetProtocol clears any pending half-transfer and asserts RQM so the next
// CPU access starts a fresh 16-bit DR exchange. Intended for use by the
// loader after constructing the IO.
func (io *IO) ResetProtocol() {
	io.drHighNext = false
	io.Core.SR |= srRQM
	io.Core.SR &^= srDRS
}

// SetDSPResult is a test helper that models the DSP program writing DR with a
// new result. It sets RQM so the CPU side sees the new value as ready.
func (io *IO) SetDSPResult(v uint16) {
	io.Core.DR = v
	io.Core.SR |= srRQM
	io.drHighNext = false
	io.Core.SR &^= srDRS
}
