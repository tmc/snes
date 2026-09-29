package trace

import (
	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/disasm"
)

// Registers is a CPU register snapshot.
//
// A is the full 16-bit accumulator. E is always serialized. Register
// widths are not serialized; they follow from E and P.
type Registers struct {
	A      uint16 `json:"a"`
	X      uint16 `json:"x"`
	Y      uint16 `json:"y"`
	S      uint16 `json:"s"`
	D      uint16 `json:"d"`
	DB     uint8  `json:"db"`
	PB     uint8  `json:"pb"`
	PC     uint16 `json:"pc"`
	P      uint8  `json:"p"`
	E      bool   `json:"e"`
	Cycles uint64 `json:"cycles"`
}

func registers(s cpu.Snapshot) Registers {
	return Registers{
		A: s.A, X: s.X, Y: s.Y, S: s.S, D: s.D,
		DB: s.DB, PB: s.PB, PC: s.PC, P: s.P,
		E: s.E, Cycles: s.Cycles,
	}
}

// Fetch roles.
const (
	RoleOpcode  = "opcode"
	RoleOperand = "operand"
)

// A FetchRecord is one instruction byte as the CPU fetched it.
// ROMOffset is present only when the mapper reports ROM at Addr.
type FetchRecord struct {
	Addr      uint32  `json:"addr"`
	Value     uint8   `json:"value"`
	Role      string  `json:"role"`
	ROMOffset *uint32 `json:"rom_offset,omitempty"`
}

// Instruction statuses.
const (
	StatusRetired = "retired"
	StatusFaulted = "faulted"
	StatusInvalid = "invalid"
)

// Instruction issues.
const (
	IssueLengthMismatch       = "length_mismatch"
	IssueFetchAddressMismatch = "fetch_address_mismatch"
	IssueFetchOverflow        = "fetch_overflow"
)

// An Insn is the record of one CPU dispatch (kind "cpu_insn").
//
// Length is decoded from the entry state. SequentialPC is the entry
// PB:PC advanced by Length within the program bank. SuccessorPC is the
// exit PB:PC. Fetches are never rewritten to agree with Length; any
// disagreement is reported in Issues and Status is StatusInvalid.
type Insn struct {
	Seq               uint64        `json:"seq"`
	Entry             Registers     `json:"entry"`
	Exit              Registers     `json:"exit"`
	Fetches           []FetchRecord `json:"fetches"`
	Length            int           `json:"length"`
	SequentialPC      PC            `json:"sequential_pc"`
	SuccessorPC       PC            `json:"successor_pc"`
	Status            string        `json:"status"`
	Fault             string        `json:"fault,omitempty"`
	SoftwareInterrupt string        `json:"software_interrupt,omitempty"`
	Issues            []string      `json:"issues,omitempty"`
}

// NewInsn builds the record for observation o. romOffset, if non-nil,
// maps a CPU bus address to a ROM offset.
func NewInsn(seq uint64, o cpu.Observation, romOffset func(addr uint32) (uint32, bool)) Insn {
	in := Insn{
		Seq:         seq,
		Entry:       registers(o.Entry),
		Exit:        registers(o.Exit),
		Fetches:     make([]FetchRecord, o.NumFetches),
		SuccessorPC: PC{Bank: o.Exit.PB, Addr: o.Exit.PC},
		Status:      StatusRetired,
	}
	for i := range in.Fetches {
		f := o.Fetches[i]
		r := FetchRecord{Addr: f.Addr, Value: f.Value, Role: RoleOperand}
		if i == 0 {
			r.Role = RoleOpcode
		}
		if romOffset != nil {
			if off, ok := romOffset(f.Addr); ok {
				r.ROMOffset = &off
			}
		}
		in.Fetches[i] = r
	}
	if o.NumFetches > 0 {
		op := o.Fetches[0].Value
		in.Length = disasm.InstructionLength65816(op, o.Entry.MemoryWidth() == 8, o.Entry.IndexWidth() == 8)
		switch op {
		case 0x00:
			in.SoftwareInterrupt = "brk"
		case 0x02:
			in.SoftwareInterrupt = "cop"
		}
	}
	in.SequentialPC = PC{Bank: o.Entry.PB, Addr: o.Entry.PC + uint16(in.Length)}

	if o.Overflow {
		in.Issues = append(in.Issues, IssueFetchOverflow)
	}
	if o.NumFetches != in.Length || o.Overflow {
		in.Issues = append(in.Issues, IssueLengthMismatch)
	}
	for i, f := range in.Fetches {
		want := uint32(o.Entry.PB)<<16 | uint32(o.Entry.PC+uint16(i))
		if f.Addr != want {
			in.Issues = append(in.Issues, IssueFetchAddressMismatch)
			break
		}
	}
	switch {
	case o.Fault != nil:
		in.Status = StatusFaulted
		in.Fault = o.Fault.Error()
	case len(in.Issues) > 0:
		in.Status = StatusInvalid
	}
	return in
}

// A Transition is the record of a hardware control transfer
// (kind "cpu_transition"). It is never an instruction.
type Transition struct {
	Seq        uint64    `json:"seq"`
	Kind       string    `json:"kind"`
	Before     Registers `json:"before"`
	After      Registers `json:"after"`
	VectorAddr *uint32   `json:"vector_addr,omitempty"`
	HandlerPC  *PC       `json:"handler_pc,omitempty"`
	FromWait   bool      `json:"from_wait"`
}

// NewTransition builds the record for transition t.
func NewTransition(seq uint64, t cpu.Transition) Transition {
	r := Transition{
		Seq:      seq,
		Kind:     t.Kind.String(),
		Before:   registers(t.Before),
		After:    registers(t.After),
		FromWait: t.FromWait,
	}
	if t.Kind == cpu.TransitionNMI || t.Kind == cpu.TransitionIRQ {
		v := t.Vector
		r.VectorAddr = &v
		r.HandlerPC = &PC{Bank: t.After.PB, Addr: t.After.PC}
	}
	return r
}

// A Gap states that the executions numbered FirstSeq through LastSeq
// were observed but not serialized (kind "gap").
type Gap struct {
	FirstSeq uint64 `json:"first_seq"`
	LastSeq  uint64 `json:"last_seq"`
	Reason   string `json:"reason"`
}

// RunInfo is the header record of a stream (kind "run").
type RunInfo struct {
	ROMSHA256          string   `json:"rom_sha256"`
	Mapper             string   `json:"mapper"`
	ROMProvenance      string   `json:"rom_provenance"`
	EngineRevision     string   `json:"engine_revision,omitempty"`
	EngineDirty        bool     `json:"engine_dirty"`
	EngineDirtySHA256  string   `json:"engine_dirty_sha256,omitempty"`
	Start              string   `json:"start"`
	InitialStateSHA256 string   `json:"initial_state_sha256,omitempty"`
	ReplayInputSHA256  string   `json:"replay_input_sha256,omitempty"`
	Events             []string `json:"events"`
	Filters            *Filters `json:"filters,omitempty"`
	Limits             Limits   `json:"limits"`
}

// Filters lists the output filters of a run.
type Filters struct {
	PCRanges []Range `json:"pc_ranges,omitempty"`
}

// Limits lists the execution and output limits of a run. Zero means
// unlimited.
type Limits struct {
	Events int `json:"events,omitempty"`
	Bytes  int `json:"bytes,omitempty"`
	Frames int `json:"frames,omitempty"`
}

// A Recorder turns CPU observations into stream records.
// It implements cpu.Observer.
//
// Every observation advances the sequence number, whether or not it
// is serialized. Once the Writer fails or reaches a limit, nothing
// further is written; callers check Err and Writer.Truncated at safe
// points and report the outcome in the run's Receipt.
type Recorder struct {
	w *Writer

	// Frame reports the current frame number for each record.
	Frame func() int
	// ROMOffset, if non-nil, maps CPU bus addresses to ROM offsets.
	ROMOffset func(addr uint32) (uint32, bool)
	// Keep, if non-nil, selects which instructions are serialized by
	// their entry PB:PC. Transitions are always serialized.
	Keep func(pb uint8, pc uint16) bool
	// Instructions and Transitions enable the two record kinds.
	Instructions, Transitions bool

	seq      uint64
	gapFirst uint64 // first excluded seq of the open gap, or 0
}

// NewRecorder returns a Recorder writing to w.
func NewRecorder(w *Writer) *Recorder {
	return &Recorder{w: w, Instructions: true, Transitions: true}
}

// LastSeq returns the sequence number of the last observation, or 0.
func (r *Recorder) LastSeq() uint64 {
	return r.seq
}

// Err returns the first write error.
func (r *Recorder) Err() error {
	return r.w.Err()
}

func (r *Recorder) frame() int {
	if r.Frame == nil {
		return 0
	}
	return r.Frame()
}

// exclude marks seq as observed but not serialized.
func (r *Recorder) exclude(seq uint64) {
	if r.gapFirst == 0 {
		r.gapFirst = seq
	}
}

// closeGap writes the open gap, which ends before seq.
func (r *Recorder) closeGap(seq uint64) {
	if r.gapFirst == 0 {
		return
	}
	r.w.Emit(Event{Kind: "gap", Frame: r.frame(), Gap: &Gap{FirstSeq: r.gapFirst, LastSeq: seq - 1, Reason: "filter"}})
	r.gapFirst = 0
}

// Flush writes any open gap. Call it once when the run ends.
func (r *Recorder) Flush() error {
	r.closeGap(r.seq + 1)
	return r.Err()
}

func (r *Recorder) stopped() bool {
	return r.w.Err() != nil || r.w.Truncated()
}

// ObserveInstruction implements cpu.Observer.
func (r *Recorder) ObserveInstruction(o cpu.Observation) {
	r.seq++
	if r.stopped() {
		return
	}
	if !r.Instructions || (r.Keep != nil && !r.Keep(o.Entry.PB, o.Entry.PC)) {
		r.exclude(r.seq)
		return
	}
	r.closeGap(r.seq)
	in := NewInsn(r.seq, o, r.ROMOffset)
	r.w.Emit(Event{Kind: "cpu_insn", Frame: r.frame(), Cycle: o.Entry.Cycles, Insn: &in})
}

// ObserveTransition implements cpu.Observer.
func (r *Recorder) ObserveTransition(t cpu.Transition) {
	r.seq++
	if r.stopped() {
		return
	}
	if !r.Transitions {
		r.exclude(r.seq)
		return
	}
	r.closeGap(r.seq)
	tr := NewTransition(r.seq, t)
	r.w.Emit(Event{Kind: "cpu_transition", Frame: r.frame(), Cycle: t.After.Cycles, Transition: &tr})
}
