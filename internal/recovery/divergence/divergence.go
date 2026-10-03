package divergence

import (
	"fmt"
	"strings"

	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/trace"
)

// MemoryAccess represents a memory read or write operation.
type MemoryAccess struct {
	Address uint32 `json:"address"`
	Value   uint16 `json:"value"`
	Width   int    `json:"width"` // 8 or 16 bits
}

// Equal reports whether m and other represent equivalent memory accesses.
func (m MemoryAccess) Equal(other MemoryAccess) bool {
	if m.Address != other.Address {
		return false
	}
	if m.Value != other.Value {
		return false
	}
	w1, w2 := m.Width, other.Width
	if w1 == 0 {
		w1 = 8
	}
	if w2 == 0 {
		w2 = 8
	}
	return w1 == w2
}

// RegisterState represents the CPU register state at an execution boundary.
type RegisterState struct {
	A  uint16 `json:"a"`
	X  uint16 `json:"x"`
	Y  uint16 `json:"y"`
	S  uint16 `json:"s"`
	D  uint16 `json:"d"`
	DB uint8  `json:"db"`
	PB uint8  `json:"pb"`
	P  uint8  `json:"p"`
	E  bool   `json:"e"`
}

// FromSnapshot converts a cpu.Snapshot into a RegisterState.
func FromSnapshot(s cpu.Snapshot) RegisterState {
	return RegisterState{
		A:  s.A,
		X:  s.X,
		Y:  s.Y,
		S:  s.S,
		D:  s.D,
		DB: s.DB,
		PB: s.PB,
		P:  s.P,
		E:  s.E,
	}
}

// Snapshot converts a RegisterState into a cpu.Snapshot.
func (r RegisterState) Snapshot() cpu.Snapshot {
	return cpu.Snapshot{
		A:  r.A,
		X:  r.X,
		Y:  r.Y,
		S:  r.S,
		D:  r.D,
		DB: r.DB,
		PB: r.PB,
		P:  r.P,
		E:  r.E,
	}
}

// Equal reports whether r and other have identical register contents.
func (r RegisterState) Equal(other RegisterState) bool {
	return r.A == other.A &&
		r.X == other.X &&
		r.Y == other.Y &&
		r.S == other.S &&
		r.D == other.D &&
		r.DB == other.DB &&
		r.PB == other.PB &&
		r.P == other.P &&
		r.E == other.E
}

// Diff returns a human-readable list of register differences between r (baseline) and other (altered).
func (r RegisterState) Diff(other RegisterState) string {
	var diffs []string
	if r.A != other.A {
		diffs = append(diffs, fmt.Sprintf("A (baseline=0x%04X, altered=0x%04X)", r.A, other.A))
	}
	if r.X != other.X {
		diffs = append(diffs, fmt.Sprintf("X (baseline=0x%04X, altered=0x%04X)", r.X, other.X))
	}
	if r.Y != other.Y {
		diffs = append(diffs, fmt.Sprintf("Y (baseline=0x%04X, altered=0x%04X)", r.Y, other.Y))
	}
	if r.S != other.S {
		diffs = append(diffs, fmt.Sprintf("S (baseline=0x%04X, altered=0x%04X)", r.S, other.S))
	}
	if r.D != other.D {
		diffs = append(diffs, fmt.Sprintf("D (baseline=0x%04X, altered=0x%04X)", r.D, other.D))
	}
	if r.DB != other.DB {
		diffs = append(diffs, fmt.Sprintf("DB (baseline=0x%02X, altered=0x%02X)", r.DB, other.DB))
	}
	if r.PB != other.PB {
		diffs = append(diffs, fmt.Sprintf("PB (baseline=0x%02X, altered=0x%02X)", r.PB, other.PB))
	}
	if r.P != other.P {
		diffs = append(diffs, fmt.Sprintf("P (baseline=0x%02X, altered=0x%02X)", r.P, other.P))
	}
	if r.E != other.E {
		diffs = append(diffs, fmt.Sprintf("E (baseline=%t, altered=%t)", r.E, other.E))
	}
	return strings.Join(diffs, ", ")
}

// Step represents a single executed instruction step in a trace.
type Step struct {
	Address   uint32         `json:"address"`
	Opcode    byte           `json:"opcode"`
	Mnemonic  string         `json:"mnemonic"`
	Registers RegisterState  `json:"registers"`
	Reads     []MemoryAccess `json:"reads,omitempty"`
	Writes    []MemoryAccess `json:"writes,omitempty"`
	Cycle     uint64         `json:"cycle,omitempty"`
	Sequence  int            `json:"sequence"`
}

// DivergenceCategory categorizes how two execution traces diverged.
type DivergenceCategory string

const (
	// DivergenceNone indicates runs are identical.
	DivergenceNone DivergenceCategory = "none"

	// DivergenceBranchOutcome indicates same branch PC executed, but branch was taken in one run and not taken in other.
	DivergenceBranchOutcome DivergenceCategory = "branch_outcome"

	// DivergenceIndirectTarget indicates same indirect jump executed, but jumped to different targets.
	DivergenceIndirectTarget DivergenceCategory = "indirect_target"

	// DivergenceReturnTarget indicates RTS/RTL returned to different addresses.
	DivergenceReturnTarget DivergenceCategory = "return_target"

	// DivergenceRegisterState indicates same PC and opcode executed, but register or flag state differed.
	DivergenceRegisterState DivergenceCategory = "register_state"

	// DivergenceMemoryWrite indicates first differing memory store (address, value, or order).
	DivergenceMemoryWrite DivergenceCategory = "memory_write"

	// DivergenceLengthMismatch indicates one execution halted or exited earlier while matching.
	DivergenceLengthMismatch DivergenceCategory = "length_mismatch"

	// DivergenceUncorrelated indicates executions jumped to completely different PCs.
	DivergenceUncorrelated DivergenceCategory = "uncorrelated"
)

// DivergenceDecision records the first control-flow or register decision change between two runs.
type DivergenceDecision struct {
	PC            uint32             `json:"pc"`
	Sequence      int                `json:"sequence"`
	Category      DivergenceCategory `json:"category"`
	BaselineValue string             `json:"baseline_value"`
	AlteredValue  string             `json:"altered_value"`
	Description   string             `json:"description"`
}

// DivergenceEffect records the first differing external observable effect (such as a memory write or MMIO write).
type DivergenceEffect struct {
	Sequence      int    `json:"sequence"`
	PC            uint32 `json:"pc"`
	Address       uint32 `json:"address"`
	BaselineValue uint16 `json:"baseline_value"`
	AlteredValue  uint16 `json:"altered_value"`
	BaselineStep  int    `json:"baseline_step"`
	AlteredStep   int    `json:"altered_step"`
	IsMMIO        bool   `json:"is_mmio"`
	Description   string `json:"description"`
}

// Report details the comparison results between baseline and altered traces.
type Report struct {
	CommonPrefixLength int                 `json:"common_prefix_length"`
	Decision           *DivergenceDecision `json:"decision,omitempty"`
	FirstEffect        *DivergenceEffect   `json:"first_effect,omitempty"`
	TotalBaselineSteps int                 `json:"total_baseline_steps"`
	TotalAlteredSteps  int                 `json:"total_altered_steps"`
	Summary            string              `json:"summary"`
}

// Options configures the divergence comparison.
type Options struct {
	// IgnoreCycles ignores cycle count variations between matching steps.
	IgnoreCycles bool `json:"ignore_cycles"`

	// SelectedAddresses specifies memory addresses to monitor for first effect.
	// If empty, any differing memory write or MMIO write is considered.
	SelectedAddresses []uint32 `json:"selected_addresses,omitempty"`

	// MaxSteps limits the number of steps evaluated from each trace.
	MaxSteps int `json:"max_steps,omitempty"`
}

// FormatAddress returns a standard SNES address string "$BB:AAAA".
func FormatAddress(addr uint32) string {
	bank := (addr >> 16) & 0xFF
	pc := addr & 0xFFFF
	return fmt.Sprintf("$%02X:%04X", bank, pc)
}

// IsMMIO reports whether addr falls in the SNES MMIO memory-mapped register range.
func IsMMIO(addr uint32) bool {
	bank := (addr >> 16) & 0xFF
	offset := addr & 0xFFFF
	if bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF) {
		if (offset >= 0x2100 && offset <= 0x21FF) || (offset >= 0x4200 && offset <= 0x437F) {
			return true
		}
	}
	return false
}

// StepFromObservation converts a cpu.Observation into a Step.
// Registers is populated from obs.Exit to reflect the resulting register state.
func StepFromObservation(obs cpu.Observation, seq int) Step {
	var op byte
	if obs.NumFetches > 0 {
		op = obs.Fetches[0].Value
	}
	addr := uint32(obs.Entry.PB)<<16 | uint32(obs.Entry.PC)
	name := ""
	if int(op) < len(cpu.Opcodes) {
		name = cpu.Opcodes[op].Name
	}
	return Step{
		Address:   addr,
		Opcode:    op,
		Mnemonic:  name,
		Registers: FromSnapshot(obs.Exit),
		Cycle:     obs.Entry.Cycles,
		Sequence:  seq,
	}
}

// StepsFromObservations converts a slice of cpu.Observation into a slice of Steps.
func StepsFromObservations(observations []cpu.Observation) []Step {
	steps := make([]Step, len(observations))
	for i, obs := range observations {
		steps[i] = StepFromObservation(obs, i)
	}
	return steps
}

// StepFromTraceEvent converts a trace.Event of kind "cpu_insn" into a Step.
func StepFromTraceEvent(e trace.Event, seq int) (Step, bool) {
	if e.Insn == nil {
		return Step{}, false
	}
	addr := uint32(e.Insn.Entry.PB)<<16 | uint32(e.Insn.Entry.PC)
	var op byte
	if len(e.Insn.Fetches) > 0 {
		op = e.Insn.Fetches[0].Value
	}
	name := ""
	if int(op) < len(cpu.Opcodes) {
		name = cpu.Opcodes[op].Name
	}
	reg := RegisterState{
		A:  e.Insn.Exit.A,
		X:  e.Insn.Exit.X,
		Y:  e.Insn.Exit.Y,
		S:  e.Insn.Exit.S,
		D:  e.Insn.Exit.D,
		DB: e.Insn.Exit.DB,
		PB: e.Insn.Exit.PB,
		P:  e.Insn.Exit.P,
		E:  e.Insn.Exit.E,
	}
	return Step{
		Address:   addr,
		Opcode:    op,
		Mnemonic:  name,
		Registers: reg,
		Cycle:     e.Cycle,
		Sequence:  seq,
	}, true
}

// StepsFromTraceEvents converts trace events into Steps.
func StepsFromTraceEvents(events []trace.Event) []Step {
	var steps []Step
	for _, e := range events {
		if s, ok := StepFromTraceEvent(e, len(steps)); ok {
			steps = append(steps, s)
		}
	}
	return steps
}

// Compare compares two execution traces and pinpoints the first divergent decision
// and the first differing external effect.
func Compare(baseline, altered []Step, opts Options) (*Report, error) {
	if opts.MaxSteps < 0 {
		return nil, fmt.Errorf("divergence: invalid negative MaxSteps %d", opts.MaxSteps)
	}

	totalBase := len(baseline)
	totalAlt := len(altered)

	if opts.MaxSteps > 0 {
		if len(baseline) > opts.MaxSteps {
			baseline = baseline[:opts.MaxSteps]
		}
		if len(altered) > opts.MaxSteps {
			altered = altered[:opts.MaxSteps]
		}
	}

	commonPrefix := 0
	var decision *DivergenceDecision

	minLen := len(baseline)
	if len(altered) < minLen {
		minLen = len(altered)
	}

	switch {
	case len(baseline) == 0 && len(altered) == 0:
		decision = &DivergenceDecision{
			Category:    DivergenceNone,
			Description: "runs are identical (empty)",
		}
	case len(baseline) == 0:
		decision = &DivergenceDecision{
			PC:           altered[0].Address,
			Sequence:     0,
			Category:     DivergenceLengthMismatch,
			AlteredValue: FormatAddress(altered[0].Address),
			Description:  fmt.Sprintf("baseline run is empty; altered run has %d steps", len(altered)),
		}
	case len(altered) == 0:
		decision = &DivergenceDecision{
			PC:            baseline[0].Address,
			Sequence:      0,
			Category:      DivergenceLengthMismatch,
			BaselineValue: FormatAddress(baseline[0].Address),
			Description:   fmt.Sprintf("altered run is empty; baseline run has %d steps", len(baseline)),
		}
	case baseline[0].Address != altered[0].Address:
		decision = &DivergenceDecision{
			PC:            baseline[0].Address,
			Sequence:      baseline[0].Sequence,
			Category:      DivergenceUncorrelated,
			BaselineValue: FormatAddress(baseline[0].Address),
			AlteredValue:  FormatAddress(altered[0].Address),
			Description: fmt.Sprintf("uncorrelated initial execution address: baseline started at %s, altered started at %s",
				FormatAddress(baseline[0].Address), FormatAddress(altered[0].Address)),
		}
	default:
		// Step-by-step alignment and comparison.
		for i := 0; i < minLen; i++ {
			b := baseline[i]
			a := altered[i]

			if b.Mnemonic == "" && int(b.Opcode) < len(cpu.Opcodes) {
				b.Mnemonic = cpu.Opcodes[b.Opcode].Name
			}
			if a.Mnemonic == "" && int(a.Opcode) < len(cpu.Opcodes) {
				a.Mnemonic = cpu.Opcodes[a.Opcode].Name
			}

			// Check opcode agreement at this address.
			if b.Opcode != a.Opcode {
				commonPrefix = i
				decision = &DivergenceDecision{
					PC:            b.Address,
					Sequence:      b.Sequence,
					Category:      DivergenceUncorrelated,
					BaselineValue: fmt.Sprintf("0x%02X (%s)", b.Opcode, b.Mnemonic),
					AlteredValue:  fmt.Sprintf("0x%02X (%s)", a.Opcode, a.Mnemonic),
					Description: fmt.Sprintf("opcode mismatch at %s: baseline executed %s (0x%02X), altered executed %s (0x%02X)",
						FormatAddress(b.Address), b.Mnemonic, b.Opcode, a.Mnemonic, a.Opcode),
				}
				break
			}

			// Check register state agreement.
			if !b.Registers.Equal(a.Registers) {
				commonPrefix = i
				diffStr := b.Registers.Diff(a.Registers)
				decision = &DivergenceDecision{
					PC:            b.Address,
					Sequence:      b.Sequence,
					Category:      DivergenceRegisterState,
					BaselineValue: formatRegisterValue(b.Registers, a.Registers, true),
					AlteredValue:  formatRegisterValue(b.Registers, a.Registers, false),
					Description:   fmt.Sprintf("register state differed at %s: %s", FormatAddress(b.Address), diffStr),
				}
				break
			}

			// Check memory writes at this step.
			if !writesEqual(b.Writes, a.Writes) {
				commonPrefix = i
				decision = &DivergenceDecision{
					PC:            b.Address,
					Sequence:      b.Sequence,
					Category:      DivergenceMemoryWrite,
					BaselineValue: formatWrites(b.Writes),
					AlteredValue:  formatWrites(a.Writes),
					Description: fmt.Sprintf("memory write differed at %s (seq %d): baseline wrote %s, altered wrote %s",
						FormatAddress(b.Address), b.Sequence, formatWrites(b.Writes), formatWrites(a.Writes)),
				}
				break
			}

			// Check cycles if not ignored.
			if !opts.IgnoreCycles && b.Cycle != a.Cycle && b.Cycle != 0 && a.Cycle != 0 {
				commonPrefix = i
				decision = &DivergenceDecision{
					PC:            b.Address,
					Sequence:      b.Sequence,
					Category:      DivergenceUncorrelated,
					BaselineValue: fmt.Sprintf("cycle %d", b.Cycle),
					AlteredValue:  fmt.Sprintf("cycle %d", a.Cycle),
					Description: fmt.Sprintf("cycle count differed at %s: baseline %d, altered %d",
						FormatAddress(b.Address), b.Cycle, a.Cycle),
				}
				break
			}

			// Check transition to step i+1.
			if i+1 < minLen {
				if baseline[i+1].Address != altered[i+1].Address {
					commonPrefix = i + 1
					nextBase := baseline[i+1].Address
					nextAlt := altered[i+1].Address

					switch {
					case isBranch(b.Opcode, b.Mnemonic):
						decision = &DivergenceDecision{
							PC:            b.Address,
							Sequence:      b.Sequence,
							Category:      DivergenceBranchOutcome,
							BaselineValue: FormatAddress(nextBase),
							AlteredValue:  FormatAddress(nextAlt),
							Description: fmt.Sprintf("branch %s at %s diverged: baseline branched to %s, altered branched to %s",
								b.Mnemonic, FormatAddress(b.Address), FormatAddress(nextBase), FormatAddress(nextAlt)),
						}
					case isIndirectJump(b.Opcode, b.Mnemonic):
						decision = &DivergenceDecision{
							PC:            b.Address,
							Sequence:      b.Sequence,
							Category:      DivergenceIndirectTarget,
							BaselineValue: FormatAddress(nextBase),
							AlteredValue:  FormatAddress(nextAlt),
							Description: fmt.Sprintf("indirect jump %s at %s diverged: baseline jumped to %s, altered jumped to %s",
								b.Mnemonic, FormatAddress(b.Address), FormatAddress(nextBase), FormatAddress(nextAlt)),
						}
					case isReturn(b.Opcode, b.Mnemonic):
						decision = &DivergenceDecision{
							PC:            b.Address,
							Sequence:      b.Sequence,
							Category:      DivergenceReturnTarget,
							BaselineValue: FormatAddress(nextBase),
							AlteredValue:  FormatAddress(nextAlt),
							Description: fmt.Sprintf("return %s at %s diverged: baseline returned to %s, altered returned to %s",
								b.Mnemonic, FormatAddress(b.Address), FormatAddress(nextBase), FormatAddress(nextAlt)),
						}
					default:
						decision = &DivergenceDecision{
							PC:            b.Address,
							Sequence:      b.Sequence,
							Category:      DivergenceUncorrelated,
							BaselineValue: FormatAddress(nextBase),
							AlteredValue:  FormatAddress(nextAlt),
							Description: fmt.Sprintf("control flow diverged after %s at %s: baseline reached %s, altered reached %s",
								b.Mnemonic, FormatAddress(b.Address), FormatAddress(nextBase), FormatAddress(nextAlt)),
						}
					}
					break
				}
			}
		}

		if decision == nil {
			commonPrefix = minLen
			if len(baseline) > len(altered) {
				decision = &DivergenceDecision{
					PC:            baseline[minLen].Address,
					Sequence:      minLen,
					Category:      DivergenceLengthMismatch,
					BaselineValue: fmt.Sprintf("continued (%s)", FormatAddress(baseline[minLen].Address)),
					AlteredValue:  "ended",
					Description: fmt.Sprintf("execution length mismatch: altered run ended after %d steps, but baseline continued to %d steps",
						len(altered), len(baseline)),
				}
			} else if len(altered) > len(baseline) {
				decision = &DivergenceDecision{
					PC:            altered[minLen].Address,
					Sequence:      minLen,
					Category:      DivergenceLengthMismatch,
					BaselineValue: "ended",
					AlteredValue:  fmt.Sprintf("continued (%s)", FormatAddress(altered[minLen].Address)),
					Description: fmt.Sprintf("execution length mismatch: baseline run ended after %d steps, but altered continued to %d steps",
						len(baseline), len(altered)),
				}
			} else {
				decision = &DivergenceDecision{
					Category:    DivergenceNone,
					Description: "runs are identical",
				}
			}
		}
	}

	firstEffect := findFirstEffect(baseline, altered, opts.SelectedAddresses)

	report := &Report{
		CommonPrefixLength: commonPrefix,
		Decision:           decision,
		FirstEffect:        firstEffect,
		TotalBaselineSteps: totalBase,
		TotalAlteredSteps:  totalAlt,
		Summary:            buildSummary(commonPrefix, decision, firstEffect, totalBase, totalAlt),
	}

	return report, nil
}

type writeRecord struct {
	stepIndex int
	seq       int
	pc        uint32
	access    MemoryAccess
}

func findFirstEffect(baseline, altered []Step, selected []uint32) *DivergenceEffect {
	var selMap map[uint32]bool
	if len(selected) > 0 {
		selMap = make(map[uint32]bool, len(selected))
		for _, addr := range selected {
			selMap[addr] = true
		}
	}

	collectWrites := func(steps []Step) []writeRecord {
		var recs []writeRecord
		for i, s := range steps {
			for _, w := range s.Writes {
				if selMap != nil && !selMap[w.Address] {
					continue
				}
				recs = append(recs, writeRecord{
					stepIndex: i,
					seq:       s.Sequence,
					pc:        s.Address,
					access:    w,
				})
			}
		}
		return recs
	}

	baseWrites := collectWrites(baseline)
	altWrites := collectWrites(altered)

	minW := len(baseWrites)
	if len(altWrites) < minW {
		minW = len(altWrites)
	}

	for k := 0; k < minW; k++ {
		bw := baseWrites[k]
		aw := altWrites[k]
		if !bw.access.Equal(aw.access) {
			desc := ""
			if bw.access.Address == aw.access.Address {
				desc = fmt.Sprintf("differing memory write at %s: baseline wrote 0x%02X at step %d, altered wrote 0x%02X at step %d",
					FormatAddress(bw.access.Address), bw.access.Value, bw.stepIndex, aw.access.Value, aw.stepIndex)
			} else {
				desc = fmt.Sprintf("differing write address: baseline wrote to %s (0x%02X) at step %d, altered wrote to %s (0x%02X) at step %d",
					FormatAddress(bw.access.Address), bw.access.Value, bw.stepIndex, FormatAddress(aw.access.Address), aw.access.Value, aw.stepIndex)
			}
			return &DivergenceEffect{
				Sequence:      bw.seq,
				PC:            bw.pc,
				Address:       bw.access.Address,
				BaselineValue: bw.access.Value,
				AlteredValue:  aw.access.Value,
				BaselineStep:  bw.stepIndex,
				AlteredStep:   aw.stepIndex,
				IsMMIO:        IsMMIO(bw.access.Address) || IsMMIO(aw.access.Address),
				Description:   desc,
			}
		}
	}

	if len(baseWrites) > len(altWrites) {
		bw := baseWrites[minW]
		return &DivergenceEffect{
			Sequence:      bw.seq,
			PC:            bw.pc,
			Address:       bw.access.Address,
			BaselineValue: bw.access.Value,
			BaselineStep:  bw.stepIndex,
			AlteredStep:   -1,
			IsMMIO:        IsMMIO(bw.access.Address),
			Description: fmt.Sprintf("unmatched baseline write to %s (val 0x%02X) at step %d (altered had no write)",
				FormatAddress(bw.access.Address), bw.access.Value, bw.stepIndex),
		}
	}

	if len(altWrites) > len(baseWrites) {
		aw := altWrites[minW]
		return &DivergenceEffect{
			Sequence:      aw.seq,
			PC:            aw.pc,
			Address:       aw.access.Address,
			AlteredValue:  aw.access.Value,
			BaselineStep:  -1,
			AlteredStep:   aw.stepIndex,
			IsMMIO:        IsMMIO(aw.access.Address),
			Description: fmt.Sprintf("unmatched altered write to %s (val 0x%02X) at step %d (baseline had no write)",
				FormatAddress(aw.access.Address), aw.access.Value, aw.stepIndex),
		}
	}

	return nil
}

func isBranch(op byte, mnem string) bool {
	switch strings.ToUpper(mnem) {
	case "BNE", "BEQ", "BPL", "BMI", "BCC", "BCS", "BVC", "BVS", "BRA", "BRL":
		return true
	}
	switch op {
	case 0x10, 0x30, 0x50, 0x70, 0x90, 0xB0, 0xD0, 0xF0, 0x80, 0x82:
		return true
	}
	return false
}

func isIndirectJump(op byte, mnem string) bool {
	switch op {
	case 0x6C, 0x7C, 0xDC, 0xFC:
		return true
	}
	m := strings.ToUpper(mnem)
	if (m == "JMP" || m == "JML" || m == "JSR") && (strings.Contains(mnem, "(") || strings.Contains(mnem, "[")) {
		return true
	}
	return false
}

func isReturn(op byte, mnem string) bool {
	switch strings.ToUpper(mnem) {
	case "RTS", "RTL", "RTI":
		return true
	}
	switch op {
	case 0x60, 0x6B, 0x40:
		return true
	}
	return false
}

func writesEqual(a, b []MemoryAccess) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

func formatWrites(writes []MemoryAccess) string {
	if len(writes) == 0 {
		return "none"
	}
	var parts []string
	for _, w := range writes {
		parts = append(parts, fmt.Sprintf("%s=0x%02X", FormatAddress(w.Address), w.Value))
	}
	return strings.Join(parts, ", ")
}

func formatRegisterValue(b, a RegisterState, forBaseline bool) string {
	r := b
	if !forBaseline {
		r = a
	}
	var parts []string
	if b.A != a.A {
		parts = append(parts, fmt.Sprintf("A=0x%04X", r.A))
	}
	if b.X != a.X {
		parts = append(parts, fmt.Sprintf("X=0x%04X", r.X))
	}
	if b.Y != a.Y {
		parts = append(parts, fmt.Sprintf("Y=0x%04X", r.Y))
	}
	if b.S != a.S {
		parts = append(parts, fmt.Sprintf("S=0x%04X", r.S))
	}
	if b.D != a.D {
		parts = append(parts, fmt.Sprintf("D=0x%04X", r.D))
	}
	if b.DB != a.DB {
		parts = append(parts, fmt.Sprintf("DB=0x%02X", r.DB))
	}
	if b.PB != a.PB {
		parts = append(parts, fmt.Sprintf("PB=0x%02X", r.PB))
	}
	if b.P != a.P {
		parts = append(parts, fmt.Sprintf("P=0x%02X", r.P))
	}
	if b.E != a.E {
		parts = append(parts, fmt.Sprintf("E=%t", r.E))
	}
	return strings.Join(parts, ", ")
}

func buildSummary(prefixLen int, decision *DivergenceDecision, effect *DivergenceEffect, baseLen, altLen int) string {
	if decision == nil || decision.Category == DivergenceNone {
		return fmt.Sprintf("runs are identical (%d steps)", baseLen)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("divergence after %d common steps: %s", prefixLen, decision.Description))
	if effect != nil {
		sb.WriteString(fmt.Sprintf("; first effect: %s", effect.Description))
	}
	return sb.String()
}
