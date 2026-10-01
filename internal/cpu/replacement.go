package cpu

import "fmt"

// InstructionExecutor replaces the semantics of one fetched instruction.
// The executor must use InstructionIO for timed bus operations, and return the
// architectural successor state with Cycles equal to io.State().Cycles.
// Failure stops the CPU; partially executed operations are not rolled back.
// The owner must discard or restore the failed machine branch.
type InstructionExecutor func(io *InstructionIO) (Snapshot, error)

// InstructionIO provides runtime bus timing to an instruction executor.
// It is valid only during that executor call and must not be used concurrently.
// The zero value is inactive. Each instruction allows at most 128 operations.
type InstructionIO struct {
	lease *instructionLease
}

type instructionLease struct {
	cpu        *CPU
	active     bool
	operations int
	fetches    int
	err        error
}

func (io *InstructionIO) check() error {
	if io == nil || io.lease == nil || !io.lease.active || io.lease.cpu == nil {
		return fmt.Errorf("instruction execution is inactive")
	}
	if io.lease.err != nil {
		return io.lease.err
	}
	io.lease.operations++
	if io.lease.operations > 128 {
		io.lease.err = fmt.Errorf("instruction operation limit exceeded")
		return io.lease.err
	}
	return nil
}

// State returns the registers after the operations completed so far.
func (io *InstructionIO) State() Snapshot {
	if io == nil || io.lease == nil || !io.lease.active || io.lease.cpu == nil {
		return Snapshot{}
	}
	return io.lease.cpu.Snapshot()
}

// Fetch reads an operand through the timed fetch path and advances PC in-bank.
func (io *InstructionIO) Fetch() (uint8, error) {
	if err := io.check(); err != nil {
		return 0, err
	}
	if io.lease.fetches >= MaxFetches {
		io.lease.err = fmt.Errorf("instruction fetch limit exceeded")
		return 0, io.lease.err
	}
	io.lease.fetches++
	return io.lease.cpu.fetchByte(), nil
}

// Read performs a timed physical bus read.
func (io *InstructionIO) Read(address uint32) (uint8, error) {
	if err := io.check(); err != nil {
		return 0, err
	}
	if address >= 1<<24 {
		io.lease.err = fmt.Errorf("read address exceeds 24 bits")
		return 0, io.lease.err
	}
	return io.lease.cpu.read(address), nil
}

// Write performs a timed physical bus write.
func (io *InstructionIO) Write(address uint32, value uint8) error {
	if err := io.check(); err != nil {
		return err
	}
	if address >= 1<<24 {
		io.lease.err = fmt.Errorf("write address exceeds 24 bits")
		return io.lease.err
	}
	io.lease.cpu.write(address, value)
	return nil
}

// Idle performs one to eight internal CPU cycles of six master clocks each.
func (io *InstructionIO) Idle(clocks uint64) error {
	if err := io.check(); err != nil {
		return err
	}
	if clocks == 0 || clocks > 48 || clocks%6 != 0 {
		io.lease.err = fmt.Errorf("invalid instruction idle clocks")
		return io.lease.err
	}
	io.lease.cpu.Idle(clocks)
	return nil
}

func (c *CPU) runReplacement(execute InstructionExecutor) {
	io := &InstructionIO{lease: &instructionLease{cpu: c, active: true, fetches: 1}}
	state, err := execute(io)
	io.lease.active = false
	if err == nil {
		err = io.lease.err
	}
	if err == nil && state.Cycles != c.Cycles {
		err = fmt.Errorf("successor changes runtime clock")
	}
	if err == nil && state.E && (state.P&0x30 != 0x30 || state.S&0xff00 != 0x100) {
		err = fmt.Errorf("invalid emulation successor")
	}
	if err == nil && (state.E || state.P&0x10 != 0) && (state.X > 255 || state.Y > 255) {
		err = fmt.Errorf("invalid narrow index successor")
	}
	if err != nil {
		c.setFaultf("instruction replacement: %v", err)
		return
	}
	c.A, c.X, c.Y, c.S, c.D, c.PC = state.A, state.X, state.Y, state.S, state.D, state.PC
	c.DB, c.PB, c.P, c.E = state.DB, state.PB, state.P, state.E
}
