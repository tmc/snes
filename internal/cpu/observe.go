package cpu

import "errors"

// A Snapshot is a copy of the CPU registers at an execution boundary.
//
// A is the full 16-bit accumulator, including the retained high byte
// when the accumulator is 8 bits wide. E is the emulation flag; it is
// not implied by the P.M and P.X bits.
type Snapshot struct {
	A, X, Y, S, D, PC uint16
	DB, PB, P         uint8
	E                 bool
	Cycles            uint64
}

// Snapshot returns the current registers.
func (c *CPU) Snapshot() Snapshot {
	return Snapshot{
		A: c.A, X: c.X, Y: c.Y, S: c.S, D: c.D, PC: c.PC,
		DB: c.DB, PB: c.PB, P: c.P,
		E:      c.E,
		Cycles: c.Cycles,
	}
}

// MemoryWidth reports the accumulator and memory width in bits.
func (s Snapshot) MemoryWidth() int {
	if s.E || s.P&0x20 != 0 {
		return 8
	}
	return 16
}

// IndexWidth reports the X and Y register width in bits.
func (s Snapshot) IndexWidth() int {
	if s.E || s.P&0x10 != 0 {
		return 8
	}
	return 16
}

// A Fetch is one byte read through the CPU's instruction fetch path.
// Addr is the 24-bit bus address that was read.
type Fetch struct {
	Addr  uint32
	Value uint8
}

// MaxFetches is the number of instruction fetches an Observation holds.
// No 65816 instruction is longer.
const MaxFetches = 4

// An Observation records one CPU dispatch: the fetch of an opcode and
// its operands and the execution that follows.
//
// Entry is taken immediately before the opcode fetch, after
// emulation-mode normalization of P and S. Exit is taken after the
// dispatch returns. Fetches holds the opcode and operand bytes in the
// order the CPU read them; pointer, stack and data reads are not
// included. The first fetch is the opcode.
//
// MVN and MVP rewind PC while bytes remain, so each repetition is a
// separate Observation.
type Observation struct {
	Entry, Exit Snapshot
	Fetches     [MaxFetches]Fetch
	NumFetches  int
	// Overflow reports that the dispatch fetched more than MaxFetches
	// bytes. The observation is then incomplete.
	Overflow bool
	// Fault is the CPU fault raised by this dispatch, if any.
	Fault error
}

// Bytes returns the fetched values.
func (in *Observation) Bytes() []uint8 {
	b := make([]uint8, in.NumFetches)
	for i := range b {
		b[i] = in.Fetches[i].Value
	}
	return b
}

// A TransitionKind identifies a change of CPU control that is not an
// instruction.
type TransitionKind uint8

const (
	TransitionReset TransitionKind = iota + 1
	TransitionNMI
	TransitionIRQ
	// TransitionWake is WAI resuming on an IRQ that is masked by P.I,
	// so no interrupt is taken.
	TransitionWake
)

func (k TransitionKind) String() string {
	switch k {
	case TransitionReset:
		return "reset"
	case TransitionNMI:
		return "nmi"
	case TransitionIRQ:
		return "irq"
	case TransitionWake:
		return "wake"
	}
	return "unknown"
}

// A Transition observes a hardware control transfer.
//
// For NMI and IRQ, Vector is the address of the vector's low byte and
// After.PB:After.PC is the handler entry. FromWait reports that the CPU
// was halted by WAI when the transition began.
type Transition struct {
	Kind          TransitionKind
	Before, After Snapshot
	Vector        uint32
	FromWait      bool
}

// An Observer receives CPU observations. Values are copies; an observer
// may retain them. Observers run on the emulation goroutine and must
// not call back into the CPU or bus.
type Observer interface {
	ObserveInstruction(Observation)
	ObserveTransition(Transition)
}

// ErrObserverAttached is returned by Observe when another observer is
// already attached.
var ErrObserverAttached = errors.New("cpu observer already attached")

// Observe attaches o. It fails if an observer is already attached.
// The returned function detaches o.
//
// Observation does not change execution: it performs no bus accesses
// and consumes no cycles.
func (c *CPU) Observe(o Observer) (detach func(), err error) {
	if o == nil {
		return nil, errors.New("nil cpu observer")
	}
	if c.observer != nil {
		return nil, ErrObserverAttached
	}
	c.observer = o
	return func() {
		if c.observer == o {
			c.observer = nil
			c.obsActive = false
		}
	}, nil
}

// observeBegin starts an instruction observation. The caller has
// checked c.observer.
func (c *CPU) observeBegin() {
	c.obs = Observation{Entry: c.Snapshot()}
	c.obsActive = true
}

// observeFetch records an instruction fetch. The caller has checked
// c.obsActive.
func (c *CPU) observeFetch(addr uint32, v uint8) {
	if c.obs.NumFetches == MaxFetches {
		c.obs.Overflow = true
		return
	}
	c.obs.Fetches[c.obs.NumFetches] = Fetch{Addr: addr, Value: v}
	c.obs.NumFetches++
}

// observeEnd completes the instruction observation.
func (c *CPU) observeEnd() {
	c.obsActive = false
	c.obs.Exit = c.Snapshot()
	c.obs.Fault = c.Fault
	if c.observer != nil {
		c.observer.ObserveInstruction(c.obs)
	}
}

// observeTransition reports a transition that began in state before.
func (c *CPU) observeTransition(kind TransitionKind, before Snapshot, vector uint32, fromWait bool) {
	c.observer.ObserveTransition(Transition{
		Kind:     kind,
		Before:   before,
		After:    c.Snapshot(),
		Vector:   vector,
		FromWait: fromWait,
	})
}

// Current returns the instruction observation in progress: its entry
// state and the bytes fetched so far. It reports false outside an
// instruction or when no observer is attached.
func (c *CPU) Current() (Observation, bool) {
	return c.obs, c.obsActive
}
