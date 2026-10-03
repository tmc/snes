package exploration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/cpu"
)

// RegisterContext records CPU registers at a machine checkpoint.
type RegisterContext struct {
	A  uint16 `json:"a"`
	X  uint16 `json:"x"`
	Y  uint16 `json:"y"`
	S  uint16 `json:"s"`
	D  uint16 `json:"d"`
	PC uint16 `json:"pc"`
	DB uint8  `json:"db"`
	PB uint8  `json:"pb"`
	P  uint8  `json:"p"`
	E  bool   `json:"e"`
}

// RegisterContextFromSnapshot constructs a RegisterContext from a CPU snapshot.
func RegisterContextFromSnapshot(s cpu.Snapshot) RegisterContext {
	return RegisterContext{
		A:  s.A,
		X:  s.X,
		Y:  s.Y,
		S:  s.S,
		D:  s.D,
		PC: s.PC,
		DB: s.DB,
		PB: s.PB,
		P:  s.P,
		E:  s.E,
	}
}

// RegisterFlags records E, M, X, and C processor flags.
type RegisterFlags struct {
	E bool `json:"e"`
	M bool `json:"m"`
	X bool `json:"x"`
	C bool `json:"c"`
}

// Byte encodes E, M, X, C flags into a 4-bit integer:
// bit 3 = E, bit 2 = M, bit 1 = X, bit 0 = C.
func (f RegisterFlags) Byte() uint8 {
	var c uint8
	if f.E {
		c |= 8
	}
	if f.M {
		c |= 4
	}
	if f.X {
		c |= 2
	}
	if f.C {
		c |= 1
	}
	return c
}

// FlagsFromSnapshot extracts E, M, X, C flags from a CPU snapshot.
func FlagsFromSnapshot(s cpu.Snapshot) RegisterFlags {
	return RegisterFlags{
		E: s.E,
		M: s.P&0x20 != 0,
		X: s.P&0x10 != 0,
		C: s.P&0x01 != 0,
	}
}

// FlagsFromByte decodes a 4-bit integer into RegisterFlags.
func FlagsFromByte(b uint8) RegisterFlags {
	return RegisterFlags{
		E: b&8 != 0,
		M: b&4 != 0,
		X: b&2 != 0,
		C: b&1 != 0,
	}
}

// Checkpoint represents a saved complete-machine checkpoint.
type Checkpoint struct {
	ID              string          `json:"id"`
	Frame           int             `json:"frame"`
	Description     string          `json:"description,omitempty"`
	StateSHA256     string          `json:"state_sha256"`
	RegisterContext RegisterContext `json:"register_context"`
	State           []byte          `json:"state,omitempty"`
}

// NewCheckpoint creates a Checkpoint from the system's current serialized state.
func NewCheckpoint(id string, frame int, desc string, s *snes.System) (Checkpoint, error) {
	if s == nil {
		return Checkpoint{}, errors.New("checkpoint: nil system")
	}
	state, err := s.Serialize()
	if err != nil {
		return Checkpoint{}, fmt.Errorf("checkpoint serialize: %w", err)
	}
	h := sha256.Sum256(state)
	return Checkpoint{
		ID:              id,
		Frame:           frame,
		Description:     desc,
		StateSHA256:     hex.EncodeToString(h[:]),
		RegisterContext: RegisterContextFromSnapshot(s.CPU.Snapshot()),
		State:           state,
	}, nil
}

// Recipe is a reproducible reproduction bundle.
type Recipe struct {
	CheckpointID string   `json:"checkpoint_id"`
	Inputs       []uint16 `json:"inputs"`
}

// Discovery records a newly discovered physical instruction start or branch outcome.
type Discovery struct {
	Address       uint32        `json:"address"`
	Context       RegisterFlags `json:"context"`
	CheckpointID  string        `json:"checkpoint_id"`
	ScheduleIndex int           `json:"schedule_index"`
	Schedule      []uint16      `json:"schedule"`
	FrameOffset   int           `json:"frame_offset"`
	Recipe        Recipe        `json:"recipe"`
}

// ExplorationCensus accumulates discovered physical instruction starts,
// context variants, and branch outcomes.
type ExplorationCensus struct {
	PhysicalStarts     map[uint32]Discovery `json:"physical_starts"`
	ContextVariants    map[uint64]Discovery `json:"context_variants"`
	BranchesDiscovered map[uint64]Discovery `json:"branches_discovered"`
	TotalInstructions uint64               `json:"total_instructions"`
	TotalFrames        int                  `json:"total_frames"`
}

// NewExplorationCensus creates an initialized ExplorationCensus.
func NewExplorationCensus() *ExplorationCensus {
	return &ExplorationCensus{
		PhysicalStarts:     make(map[uint32]Discovery),
		ContextVariants:    make(map[uint64]Discovery),
		BranchesDiscovered: make(map[uint64]Discovery),
	}
}

// CensusReport summarizes the results of a checkpoint exploration campaign.
type CensusReport struct {
	CheckpointID       string             `json:"checkpoint_id"`
	SchedulesRun       int                `json:"schedules_run"`
	TotalFrames        int                `json:"total_frames"`
	TotalInstructions  uint64             `json:"total_instructions"`
	NewPhysicalStarts  int                `json:"new_physical_starts"`
	NewContextVariants int                `json:"new_context_variants"`
	NewBranches        int                `json:"new_branches"`
	Census             *ExplorationCensus `json:"census"`
}

// NormalizePhysicalAddress normalizes LoROM addresses ($00-$3F:$8000-$FFFF
// and $80-$BF:$8000-$FFFF) to a physical ROM offset:
// (addr & 0x7FFF) | ((addr & 0x7F0000) >> 1).
func NormalizePhysicalAddress(addr uint32, romSize int) uint32 {
	bank := (addr >> 16) & 0xFF
	offset := addr & 0xFFFF
	if offset >= 0x8000 && ((bank <= 0x3F) || (bank >= 0x80 && bank <= 0xBF)) {
		phys := (addr & 0x7FFF) | ((addr & 0x7F0000) >> 1)
		if romSize > 0 {
			phys %= uint32(romSize)
		}
		return phys
	}
	if (bank >= 0x40 && bank <= 0x7D) || (bank >= 0xC0 && bank <= 0xFF) {
		phys := ((bank & 0x3F) << 16) | offset
		if romSize > 0 {
			phys %= uint32(romSize)
		}
		return phys
	}
	return addr
}

// ContextKey computes the map key for ContextVariants:
// (uint64(physAddr) << 4) | uint64(emxc).
func ContextKey(addr uint32, emxc uint8) uint64 {
	return (uint64(addr) << 4) | (uint64(emxc) & 0x0F)
}

// ContextKeyFromFlags computes the map key for ContextVariants from RegisterFlags.
func ContextKeyFromFlags(addr uint32, flags RegisterFlags) uint64 {
	return ContextKey(addr, flags.Byte())
}

// BranchKey computes the map key for BranchesDiscovered:
// (uint64(addr) << 1) | outcome (1 if taken, 0 if untaken).
func BranchKey(addr uint32, taken bool) uint64 {
	key := uint64(addr) << 1
	if taken {
		key |= 1
	}
	return key
}

// BranchTaken reports whether a branch key represents a taken branch.
func BranchTaken(key uint64) bool {
	return (key & 1) != 0
}

// BranchAddress returns the branch address from a branch key.
func BranchAddress(key uint64) uint32 {
	return uint32(key >> 1)
}

// CensusTracker tracks discoveries and accumulates them into an ExplorationCensus.
type CensusTracker struct {
	Census        *ExplorationCensus
	CheckpointID  string
	ScheduleIndex int
	Schedule      []uint16
	FrameOffset   int
	ROMSize       int

	NewPhysicalStarts  int
	NewContextVariants int
	NewBranches        int
}

// ObserveTransition implements cpu.Observer.
func (t *CensusTracker) ObserveTransition(cpu.Transition) {}

// ObserveInstruction implements cpu.Observer.
func (t *CensusTracker) ObserveInstruction(in cpu.Observation) {
	t.ProcessObservation(in)
}

// ProcessObservation processes a single CPU observation and updates the census.
func (t *CensusTracker) ProcessObservation(in cpu.Observation) {
	if t.Census == nil {
		return
	}
	t.Census.TotalInstructions++

	busAddr := (uint32(in.Entry.PB) << 16) | uint32(in.Entry.PC)
	physAddr := NormalizePhysicalAddress(busAddr, t.ROMSize)
	flags := FlagsFromSnapshot(in.Entry)
	emxc := flags.Byte()

	var recipeInputs []uint16
	if t.FrameOffset >= 0 && t.FrameOffset < len(t.Schedule) {
		recipeInputs = append([]uint16(nil), t.Schedule[:t.FrameOffset+1]...)
	} else if len(t.Schedule) > 0 {
		recipeInputs = append([]uint16(nil), t.Schedule...)
	}

	d := Discovery{
		Address:       physAddr,
		Context:       flags,
		CheckpointID:  t.CheckpointID,
		ScheduleIndex: t.ScheduleIndex,
		Schedule:      append([]uint16(nil), t.Schedule...),
		FrameOffset:   t.FrameOffset,
		Recipe: Recipe{
			CheckpointID: t.CheckpointID,
			Inputs:       recipeInputs,
		},
	}

	if t.Census.PhysicalStarts == nil {
		t.Census.PhysicalStarts = make(map[uint32]Discovery)
	}
	if _, ok := t.Census.PhysicalStarts[physAddr]; !ok {
		t.Census.PhysicalStarts[physAddr] = d
		t.NewPhysicalStarts++
	}

	ctxKey := ContextKey(physAddr, emxc)
	if t.Census.ContextVariants == nil {
		t.Census.ContextVariants = make(map[uint64]Discovery)
	}
	if _, ok := t.Census.ContextVariants[ctxKey]; !ok {
		t.Census.ContextVariants[ctxKey] = d
		t.NewContextVariants++
	}

	if in.NumFetches > 0 {
		opcode := in.Fetches[0].Value
		if taken, isBranch := branchTaken(opcode, in.Entry.P); isBranch {
			bKey := BranchKey(physAddr, taken)
			if t.Census.BranchesDiscovered == nil {
				t.Census.BranchesDiscovered = make(map[uint64]Discovery)
			}
			if _, ok := t.Census.BranchesDiscovered[bKey]; !ok {
				t.Census.BranchesDiscovered[bKey] = d
				t.NewBranches++
			}
		}
	}
}

func branchTaken(opcode uint8, p uint8) (taken bool, isBranch bool) {
	switch opcode {
	case 0x10: // BPL
		return p&0x80 == 0, true
	case 0x30: // BMI
		return p&0x80 != 0, true
	case 0x50: // BVC
		return p&0x40 == 0, true
	case 0x70: // BVS
		return p&0x40 != 0, true
	case 0x90: // BCC
		return p&0x01 == 0, true
	case 0xB0: // BCS
		return p&0x01 != 0, true
	case 0xD0: // BNE
		return p&0x02 == 0, true
	case 0xF0: // BEQ
		return p&0x02 != 0, true
	default:
		return false, false
	}
}

// ReplayRecipe restores a checkpoint on s and replays the recipe's input schedule.
func ReplayRecipe(s *snes.System, cp Checkpoint, r Recipe) error {
	if s == nil {
		return errors.New("replay: nil system")
	}
	if len(cp.State) > 0 {
		if err := s.Unserialize(cp.State); err != nil {
			return fmt.Errorf("restore checkpoint: %w", err)
		}
	}
	for i, in := range r.Inputs {
		if err := s.SetInputState(0, in); err != nil {
			return fmt.Errorf("set input frame %d: %w", i, err)
		}
		if err := s.RunFrame(); err != nil {
			return fmt.Errorf("run frame %d: %w", i, err)
		}
	}
	return nil
}

// RunCheckpointCampaign runs bounded controller schedules starting from a
// complete-machine checkpoint, recording discoveries in census.
func RunCheckpointCampaign(
	ctx context.Context,
	s *snes.System,
	cp Checkpoint,
	schedules [][]uint16,
	census *ExplorationCensus,
	maxFrames int,
) (*CensusReport, error) {
	if census == nil {
		census = NewExplorationCensus()
	}
	if census.PhysicalStarts == nil {
		census.PhysicalStarts = make(map[uint32]Discovery)
	}
	if census.ContextVariants == nil {
		census.ContextVariants = make(map[uint64]Discovery)
	}
	if census.BranchesDiscovered == nil {
		census.BranchesDiscovered = make(map[uint64]Discovery)
	}

	var savedState []byte
	if len(cp.State) > 0 {
		savedState = cp.State
	} else if s != nil {
		var err error
		savedState, err = s.Serialize()
		if err != nil {
			return nil, fmt.Errorf("serialize checkpoint: %w", err)
		}
	}

	report := &CensusReport{
		CheckpointID: cp.ID,
		Census:       census,
	}

	initialPhys := len(census.PhysicalStarts)
	initialCtx := len(census.ContextVariants)
	initialBranches := len(census.BranchesDiscovered)

	tracker := &CensusTracker{
		Census:       census,
		CheckpointID: cp.ID,
	}

	for idx, schedule := range schedules {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		if s != nil && len(savedState) > 0 {
			if err := s.Unserialize(savedState); err != nil {
				return nil, fmt.Errorf("restore checkpoint for schedule %d: %w", idx, err)
			}
		}

		frames := len(schedule)
		if maxFrames > 0 && frames > maxFrames {
			frames = maxFrames
		}
		inputs := schedule[:frames]

		tracker.ScheduleIndex = idx
		tracker.Schedule = inputs

		if s != nil {
			detach, err := s.CPU.Observe(tracker)
			if err != nil {
				return nil, fmt.Errorf("attach cpu observer: %w", err)
			}

			for f := 0; f < frames; f++ {
				if err := ctx.Err(); err != nil {
					detach()
					return nil, err
				}
				tracker.FrameOffset = f
				if err := s.SetInputState(0, inputs[f]); err != nil {
					detach()
					return nil, fmt.Errorf("set input frame %d: %w", f, err)
				}
				if err := s.RunFrame(); err != nil {
					detach()
					return nil, fmt.Errorf("run frame %d: %w", f, err)
				}
				census.TotalFrames++
				report.TotalFrames++
			}
			detach()
		} else {
			census.TotalFrames += frames
			report.TotalFrames += frames
		}
		report.SchedulesRun++
	}

	report.TotalInstructions = census.TotalInstructions
	report.NewPhysicalStarts = len(census.PhysicalStarts) - initialPhys
	report.NewContextVariants = len(census.ContextVariants) - initialCtx
	report.NewBranches = len(census.BranchesDiscovered) - initialBranches

	return report, nil
}
