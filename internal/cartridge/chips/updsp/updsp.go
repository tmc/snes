package updsp

import (
	"bytes"
	"encoding/gob"
	"fmt"
)

// Flags holds the four flag bits the uPD77C25 maintains per accumulator bank.
// The core holds two Flags values so ALU destination-bank selection can update
// the correct set without disturbing the other.
type Flags struct {
	OV0 bool // overflow from the most recent ALU operation
	OV1 bool // secondary overflow (set when OV0 would transition with OV0 already set)
	Z   bool // zero
	C   bool // carry
	S0  bool // sign (bit 15 of the 16-bit result, reading from the accumulator top)
	S1  bool // sign (signals arithmetic overflow into bit 16 of the 24-bit acc)
}

// Core is a uPD77C25 execution core. It holds the register file and memories
// but does not directly own the CPU<->DSP register protocol; see IO for the
// SR/DR transport layer. A zero value is not usable; construct one with
// [NewCore].
type Core struct {
	// Program memory (2048 words x 24 bits). Upper 8 bits of each uint32 are
	// unused by the ISA but retained so callers can load raw ROM dumps.
	PRG [2048]uint32

	// Data ROM (1024 words x 16 bits).
	DROM [1024]uint16

	// Data RAM (256 words x 16 bits). DSP-1 uses 256 words; the uPD77C25
	// itself has a 256-word data RAM.
	DRAM [256]uint16

	// Program counter (11 bits; masked to the program ROM size).
	PC uint16
	// Return-address stack (4 levels).
	STK [4]uint16
	SP  uint8

	// Accumulators A and B (24-bit). Stored in the low 24 bits of a uint32.
	A uint32
	B uint32

	// Flag pairs for accumulators A (index 0) and B (index 1).
	FA Flags
	FB Flags

	// Temporary register (16 bits), latches left-hand operand for the ALU.
	TR uint16
	// Temporary register B (16 bits); not used by DSP-1 programs in practice
	// but part of the core spec.
	TRB uint16

	// Data-memory pointer (8 bits; indexes into DRAM).
	DP uint8
	// ROM pointer (10 bits; indexes into DROM).
	RP uint16

	// Multiplier inputs K / L (16 bits each, signed).
	K int16
	L int16
	// Multiplier outputs M / N (16 bits each). M holds the high half, N the
	// low half of the 31-bit product K*L shifted left by 1.
	M uint16
	N uint16

	// Serial I/O registers (SI, SO). The DSP-1 program does not use the
	// serial channel; kept for completeness.
	SI uint16
	SO uint16

	// Status register; see io.go for the bit layout visible to the CPU.
	SR uint16
	// Data register latch held inside the core. The CPU reads this via the
	// DR protocol; the DSP writes to it via the SOR/SI register mapping.
	DR uint16

	// CycleCount is incremented once per executed instruction. The core has a
	// uniform ~100 ns instruction cycle on hardware; Step accepts an SNES
	// master-cycle budget and converts.
	CycleCount uint64
}

// NewCore returns a reset uPD77C25 core.
func NewCore() *Core {
	c := &Core{}
	c.Reset()
	return c
}

// Reset clears all registers and returns the PC to 0.
func (c *Core) Reset() {
	for i := range c.PRG {
		c.PRG[i] = 0
	}
	for i := range c.DRAM {
		c.DRAM[i] = 0
	}
	c.PC = 0
	c.SP = 0
	c.A = 0
	c.B = 0
	c.FA = Flags{}
	c.FB = Flags{}
	c.TR = 0
	c.TRB = 0
	c.DP = 0
	c.RP = 0
	c.K = 0
	c.L = 0
	c.M = 0
	c.N = 0
	c.SI = 0
	c.SO = 0
	// RQM=1 at reset: the DSP is idle and willing to accept a host write.
	c.SR = srRQM
	c.DR = 0
	c.CycleCount = 0
}

// LoadProgramROM installs the 24-bit packed program ROM. The input must be a
// multiple of 3 bytes encoding big-endian words. Returns an error if the
// buffer is an unexpected size.
func (c *Core) LoadProgramROM(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("updsp: empty program rom")
	}
	if len(data)%3 != 0 {
		return fmt.Errorf("updsp: program rom length %d not a multiple of 3", len(data))
	}
	if words := len(data) / 3; words > len(c.PRG) {
		return fmt.Errorf("updsp: program rom %d words exceeds %d", words, len(c.PRG))
	}
	for i := 0; i < len(data); i += 3 {
		c.PRG[i/3] = uint32(data[i])<<16 | uint32(data[i+1])<<8 | uint32(data[i+2])
	}
	return nil
}

// LoadDataROM installs the 16-bit packed data ROM (big-endian words).
func (c *Core) LoadDataROM(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("updsp: empty data rom")
	}
	if len(data)%2 != 0 {
		return fmt.Errorf("updsp: data rom length %d not even", len(data))
	}
	if words := len(data) / 2; words > len(c.DROM) {
		return fmt.Errorf("updsp: data rom %d words exceeds %d", words, len(c.DROM))
	}
	for i := 0; i < len(data); i += 2 {
		c.DROM[i/2] = uint16(data[i])<<8 | uint16(data[i+1])
	}
	return nil
}

// Step runs the core for the given number of instructions. Returns the number
// actually executed (equal to n unless the program halts on a STP-like state).
func (c *Core) Step(n int) int {
	for i := 0; i < n; i++ {
		c.execOne()
	}
	return n
}

// State mirrors the persistent fields of the core for gob encoding.
type state struct {
	PRG        []uint32
	DROM       []uint16
	DRAM       [256]uint16
	PC         uint16
	STK        [4]uint16
	SP         uint8
	A, B       uint32
	FA, FB     Flags
	TR, TRB    uint16
	DP         uint8
	RP         uint16
	K, L       int16
	M, N       uint16
	SI, SO     uint16
	SR, DR     uint16
	CycleCount uint64
}

// Serialize captures the core state. PRG and DROM are included so a restore
// does not require re-loading the program ROM.
func (c *Core) Serialize() ([]byte, error) {
	var buf bytes.Buffer
	s := state{
		PRG:        append([]uint32(nil), c.PRG[:]...),
		DROM:       append([]uint16(nil), c.DROM[:]...),
		DRAM:       c.DRAM,
		PC:         c.PC,
		STK:        c.STK,
		SP:         c.SP,
		A:          c.A,
		B:          c.B,
		FA:         c.FA,
		FB:         c.FB,
		TR:         c.TR,
		TRB:        c.TRB,
		DP:         c.DP,
		RP:         c.RP,
		K:          c.K,
		L:          c.L,
		M:          c.M,
		N:          c.N,
		SI:         c.SI,
		SO:         c.SO,
		SR:         c.SR,
		DR:         c.DR,
		CycleCount: c.CycleCount,
	}
	if err := gob.NewEncoder(&buf).Encode(s); err != nil {
		return nil, fmt.Errorf("serialize updsp core: %w", err)
	}
	return buf.Bytes(), nil
}

// Unserialize restores a core state previously produced by [Core.Serialize].
func (c *Core) Unserialize(data []byte) error {
	var s state
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&s); err != nil {
		return fmt.Errorf("unserialize updsp core: %w", err)
	}
	if len(s.PRG) != len(c.PRG) || len(s.DROM) != len(c.DROM) {
		return fmt.Errorf("unserialize updsp core: invalid rom size")
	}
	copy(c.PRG[:], s.PRG)
	copy(c.DROM[:], s.DROM)
	c.DRAM = s.DRAM
	c.PC = s.PC
	c.STK = s.STK
	c.SP = s.SP
	c.A = s.A
	c.B = s.B
	c.FA = s.FA
	c.FB = s.FB
	c.TR = s.TR
	c.TRB = s.TRB
	c.DP = s.DP
	c.RP = s.RP
	c.K = s.K
	c.L = s.L
	c.M = s.M
	c.N = s.N
	c.SI = s.SI
	c.SO = s.SO
	c.SR = s.SR
	c.DR = s.DR
	c.CycleCount = s.CycleCount
	return nil
}
