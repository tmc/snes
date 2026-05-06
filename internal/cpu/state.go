package cpu

import "errors"

// CPUState captures the serializable CPU state.
type CPUState struct {
	A  uint16
	X  uint16
	Y  uint16
	S  uint16
	PC uint16
	D  uint16

	DB uint8
	PB uint8
	P  uint8

	E bool

	NMIPending bool
	IRQPending bool

	MultiplicandA        uint8
	Dividend             uint16
	Divisor              uint8
	Quotient             uint16
	MultiplicationResult uint16
	PendingProduct       uint16
	ProductReadyCycle    uint64
	MultiplyCounter      uint8
	MultiplyDividend     uint16
	MultiplyShift        uint16
	DRAMRefreshLine      uint64
	DRAMRefreshScanline  uint64
	DRAMRefreshLineStart uint64
	DRAMRefreshPosition  uint64

	Cycles     uint64
	TraceCount int
	Stopped    bool
	Waiting    bool
	Fault      string
}

// SaveState returns a snapshot of the CPU state.
func (c *CPU) SaveState() CPUState {
	fault := ""
	if c.Fault != nil {
		fault = c.Fault.Error()
	}
	return CPUState{
		A:                    c.A,
		X:                    c.X,
		Y:                    c.Y,
		S:                    c.S,
		PC:                   c.PC,
		D:                    c.D,
		DB:                   c.DB,
		PB:                   c.PB,
		P:                    c.P,
		E:                    c.E,
		NMIPending:           c.NMIPending,
		IRQPending:           c.IRQPending,
		MultiplicandA:        c.MultiplicandA,
		Dividend:             c.Dividend,
		Divisor:              c.Divisor,
		Quotient:             c.Quotient,
		MultiplicationResult: c.MultiplicationResult,
		PendingProduct:       c.PendingProduct,
		ProductReadyCycle:    c.ProductReadyCycle,
		MultiplyCounter:      c.MultiplyCounter,
		MultiplyDividend:     c.MultiplyDividend,
		MultiplyShift:        c.MultiplyShift,
		DRAMRefreshLine:      c.DRAMRefreshLine,
		DRAMRefreshScanline:  c.DRAMRefreshScanline,
		DRAMRefreshLineStart: c.DRAMRefreshLineStart,
		DRAMRefreshPosition:  c.DRAMRefreshPosition,
		Cycles:               c.Cycles,
		TraceCount:           c.TraceCount,
		Stopped:              c.Stopped,
		Waiting:              c.Waiting,
		Fault:                fault,
	}
}

// LoadState restores a previously saved CPU state.
func (c *CPU) LoadState(state CPUState) {
	c.A = state.A
	c.X = state.X
	c.Y = state.Y
	c.S = state.S
	c.PC = state.PC
	c.D = state.D
	c.DB = state.DB
	c.PB = state.PB
	c.P = state.P
	c.E = state.E
	c.NMIPending = state.NMIPending
	c.IRQPending = state.IRQPending
	c.MultiplicandA = state.MultiplicandA
	c.Dividend = state.Dividend
	c.Divisor = state.Divisor
	c.Quotient = state.Quotient
	c.MultiplicationResult = state.MultiplicationResult
	c.PendingProduct = state.PendingProduct
	c.ProductReadyCycle = state.ProductReadyCycle
	c.MultiplyCounter = state.MultiplyCounter
	c.MultiplyDividend = state.MultiplyDividend
	c.MultiplyShift = state.MultiplyShift
	c.DRAMRefreshLine = state.DRAMRefreshLine
	c.DRAMRefreshScanline = state.DRAMRefreshScanline
	c.DRAMRefreshLineStart = state.DRAMRefreshLineStart
	c.DRAMRefreshPosition = state.DRAMRefreshPosition
	c.Cycles = state.Cycles
	c.TraceCount = state.TraceCount
	c.Stopped = state.Stopped
	c.Waiting = state.Waiting
	if state.Fault != "" {
		c.Fault = errors.New(state.Fault)
	} else {
		c.Fault = nil
	}
}
