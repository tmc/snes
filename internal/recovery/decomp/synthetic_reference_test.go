package decomp

import (
	"fmt"
	"testing"

	"github.com/tmc/snes/internal/bus"
	"github.com/tmc/snes/internal/cartridge"
	"github.com/tmc/snes/internal/cpu"
)

// Authority for these in-memory stream tests is explicitly owned by the tests.
// Production verifiers have no implicit corpora.
var testCorpora = map[string]CorpusTrustRoot{}

func newTestEvidenceVerifier(root string) *EvidenceVerifier {
	v := newEvidenceVerifier(root)
	v.policy, _ = copyAdmissionPolicy(AdmissionPolicy{Corpora: testCorpora}, "", false)
	return v
}

// runSyntheticCPU evaluates authored instruction probes and a supplied RTL frame.
func runSyntheticCPU(t *testing.T, rom []byte, state CPUState, cells []MemoryCell, returnPB uint8, returnPC uint16) (ExecResult, error) {
	t.Helper()
	b := bus.NewBus()
	ram := bus.NewWRAMDevice()
	b.Map(0x7e0000, 0x7fffff, ram)
	for bank := uint32(0); bank < 256; bank++ {
		if bank <= 0x3f || bank >= 0x80 && bank <= 0xbf {
			b.Map(bank<<16, bank<<16|0x1fff, ram)
		}
	}
	cartridge.New(rom).MapToBus(b)
	for _, cell := range cells {
		b.Write(BusCanonicalAddr(cell.Address), cell.Value)
	}
	b.Write(uint32(state.S+1), byte(returnPC))
	b.Write(uint32(state.S+2), byte(returnPC>>8))
	b.Write(uint32(state.S+3), returnPB)
	var writes []MemoryWrite
	b.WriteHook = func(a uint32, v byte) { writes = append(writes, MemoryWrite{Address: BusCanonicalAddr(a), Value: v}) }
	c := cpu.NewCPU(b)
	c.A, c.X, c.Y, c.S, c.D = state.A, state.X, state.Y, state.S, state.D
	c.PC, c.PB, c.DB, c.P, c.E = state.PC, state.PB, state.DB, state.P, state.E
	for i := 0; i < 100; i++ {
		c.Step()
		if c.PB == returnPB && c.PC == returnPC+1 {
			out := CPUState{A: c.A, X: c.X, Y: c.Y, S: c.S, D: c.D, DB: c.DB, PB: c.PB, PC: c.PC, P: c.P, E: c.E}
			return ExecResult{State: out, NextPC: uint32(c.PB)<<16 | uint32(c.PC), Writes: writes, TotalWrites: uint32(len(writes))}, nil
		}
	}
	return ExecResult{}, fmt.Errorf("synthetic instruction probe did not reach supplied return")
}
