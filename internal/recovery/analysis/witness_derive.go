package analysis

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/trace"
)

// Pinned admitted stream and ROM hashes.
const (
	AdmittedStreamSHA256 = "68aecfcf95fac6863d657979ff802c27dae5610799168b3321aad9f41046e421"
	AdmittedROMSHA256    = "66871d66be19ad2c34c927d6b14cd8eb6fc3181965b6e517cb361f7316009cfb"
)

// PointerRead records a memory read event for an indirect pointer byte.
type PointerRead struct {
	EventID uint64 `json:"event_id"`
	Address uint32 `json:"address"`
	Value   byte   `json:"value"`
}

// DerivedWitness contains the verified dispatch witness derived directly from trace events.
type DerivedWitness struct {
	StreamSHA256    string           `json:"stream_sha256"`
	ROMSHA256       string           `json:"rom_sha256"`
	DispatchEventID uint64           `json:"dispatch_event_id"`
	TargetEventID   uint64           `json:"target_event_id"`
	PointerReads    []PointerRead    `json:"pointer_reads"`
	SourceAddress   uint32           `json:"source_address"`
	TargetAddress   uint32           `json:"target_address"`
	ObservedContext recovery.Context `json:"observed_context"`
	StaticContext   recovery.Context `json:"static_context"`
	Witness         DispatchWitness  `json:"witness"`
}

// WitnessRecoveryReport summarizes the result of applying a derived witness to static recovery.
type WitnessRecoveryReport struct {
	Mode                   string           `json:"mode"`
	EdgeIndex              int              `json:"edge_index"`
	StartAddress           string           `json:"start_address"`
	SeedAddress            string           `json:"seed_address"`
	SeedContext            recovery.Context `json:"seed_context"`
	Budget                 int              `json:"budget"`
	StreamSHA256           string           `json:"stream_sha256"`
	ROMSHA256              string           `json:"rom_sha256"`
	DispatchEventID        uint64           `json:"dispatch_event_id"`
	TargetEventID          uint64           `json:"target_event_id"`
	PointerEventIDs        []uint64         `json:"pointer_event_ids"`
	SourceAddress          string           `json:"source_address"`
	TargetAddress          string           `json:"target_address"`
	ObservedContext        recovery.Context `json:"observed_context"`
	StaticContext          recovery.Context `json:"static_context"`
	BaselineInstructions   int              `json:"baseline_instructions"`
	BaselinePhysicalStarts int              `json:"baseline_physical_starts"`
	BaselineEdges          int              `json:"baseline_edges"`
	WitnessInstructions    int              `json:"witness_instructions"`
	WitnessPhysicalStarts  int              `json:"witness_physical_starts"`
	WitnessEdges           int              `json:"witness_edges"`
	DeltaInstructions      int              `json:"delta_instructions"`
	DeltaPhysicalStarts    int              `json:"delta_physical_starts"`
	DeltaEdges             int              `json:"delta_edges"`
	AddedPhysicalOffsets   []uint32         `json:"added_physical_offsets"`
	UnresolvedIssues       []recovery.Issue `json:"unresolved_issues"`
}

// DeriveWitnessFromTrace parses raw JSONL trace bytes and derives the initial dispatch witness
// for events 29890..29892, 29893, and 29897 ($0080C6 -> $0CC120).
func DeriveWitnessFromTrace(traceBytes, rom []byte) (*DerivedWitness, error) {
	ws, err := DeriveWitnessesFromTrace(traceBytes, rom)
	if err != nil {
		return nil, err
	}
	if len(ws) == 0 {
		return nil, errors.New("no witnesses derived from trace")
	}
	return ws[0], nil
}

// DeriveWitnessesFromTrace parses raw JSONL trace bytes and derives the two verified dispatch witnesses:
// Edge 1: events 29890..29892, 29893, and 29897 ($0080C6 -> $0CC120)
// Edge 2: events 29997..29999, 30000, and 30003 ($0087BD -> $0CC404)
func DeriveWitnessesFromTrace(traceBytes, rom []byte) ([]*DerivedWitness, error) {
	streamHash := fmt.Sprintf("%x", sha256.Sum256(traceBytes))
	if streamHash != AdmittedStreamSHA256 {
		return nil, fmt.Errorf("trace stream SHA-256 %q does not match admitted %q", streamHash, AdmittedStreamSHA256)
	}

	romHash := fmt.Sprintf("%x", sha256.Sum256(rom))
	if romHash != AdmittedROMSHA256 {
		return nil, fmt.Errorf("ROM SHA-256 %q does not match admitted %q", romHash, AdmittedROMSHA256)
	}

	dec := json.NewDecoder(bytes.NewReader(traceBytes))
	var (
		dispatch1 trace.Event
		target1   trace.Event
		p1Reads   []PointerRead

		dispatch2 trace.Event
		target2   trace.Event
		p2Reads   []PointerRead
	)

	for dec.More() {
		var e trace.Event
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("decoding trace event: %w", err)
		}
		switch e.ID {
		case 29890, 29891, 29892:
			p1Reads = append(p1Reads, PointerRead{
				EventID: e.ID,
				Address: e.Addr,
				Value:   byte(e.Value),
			})
		case 29893:
			dispatch1 = e
		case 29897:
			target1 = e
		case 29997, 29998, 29999:
			p2Reads = append(p2Reads, PointerRead{
				EventID: e.ID,
				Address: e.Addr,
				Value:   byte(e.Value),
			})
		case 30000:
			dispatch2 = e
		case 30003:
			target2 = e
		}
	}

	w1, err := buildDerivedWitness(streamHash, romHash, p1Reads, dispatch1, target1, rom, 0x000003)
	if err != nil {
		return nil, fmt.Errorf("deriving witness 1 ($0080C6 -> $0CC120): %w", err)
	}

	w2, err := buildDerivedWitness(streamHash, romHash, p2Reads, dispatch2, target2, rom, 0x000000)
	if err != nil {
		return nil, fmt.Errorf("deriving witness 2 ($0087BD -> $0CC404): %w", err)
	}

	return []*DerivedWitness{w1, w2}, nil
}

func buildDerivedWitness(streamHash, romHash string, pReads []PointerRead, dispatch, target trace.Event, rom []byte, basePtrAddr uint32) (*DerivedWitness, error) {
	if len(pReads) != 3 {
		return nil, fmt.Errorf("expected 3 pointer read events, found %d", len(pReads))
	}
	for i, pr := range pReads {
		expectedAddr := basePtrAddr + uint32(i)
		if pr.Address != expectedAddr {
			return nil, fmt.Errorf("pointer read %d address mismatch: expected $%06X, got $%06X", i, expectedAddr, pr.Address)
		}
	}
	if dispatch.Insn == nil || target.Insn == nil {
		return nil, errors.New("missing dispatch or target instruction events")
	}
	if dispatch.Insn.Status != "retired" || target.Insn.Status != "retired" {
		return nil, errors.New("dispatch or target instruction not retired")
	}
	if dispatch.Insn.Seq+1 != target.Insn.Seq {
		return nil, fmt.Errorf("instruction sequence discontinuity: dispatch seq %d, target seq %d", dispatch.Insn.Seq, target.Insn.Seq)
	}
	if dispatch.Insn.Exit != target.Insn.Entry {
		return nil, errors.New("CPU register context mismatch between dispatch exit and target entry")
	}

	// Verify pointer value matches target
	ptrVal := uint32(pReads[0].Value) | (uint32(pReads[1].Value) << 8) | (uint32(pReads[2].Value) << 16)
	sourceAddr := uint32(dispatch.Insn.Entry.PB)<<16 | uint32(dispatch.Insn.Entry.PC)
	targetAddr := uint32(target.Insn.Entry.PB)<<16 | uint32(target.Insn.Entry.PC)

	if ptrVal != targetAddr {
		return nil, fmt.Errorf("pointer value $%06X does not match target address $%06X", ptrVal, targetAddr)
	}

	// Verify instruction fetches against physical ROM
	for _, e := range []trace.Event{dispatch, target} {
		for _, f := range e.Insn.Fetches {
			off, ok := LoROMToOffset(f.Addr, len(rom))
			if !ok {
				return nil, fmt.Errorf("fetch address $%06X outside mapped ROM", f.Addr)
			}
			if f.ROMOffset != nil && off != *f.ROMOffset {
				return nil, fmt.Errorf("fetch ROM offset mismatch: %d != %d", off, *f.ROMOffset)
			}
			if int(off) >= len(rom) || rom[off] != f.Value {
				return nil, fmt.Errorf("fetch byte mismatch at ROM offset %d: expected 0x%02X, got 0x%02X", off, f.Value, rom[off])
			}
		}
	}

	flag := func(b bool) string {
		if b {
			return "set"
		}
		return "clear"
	}
	r := target.Insn.Entry
	observedCtx := recovery.Context{
		E: flag(r.E),
		M: flag(r.P&0x20 != 0),
		X: flag(r.P&0x10 != 0),
		C: flag(r.P&1 != 0),
	}
	staticCtx := recovery.Context{
		E: "clear",
		M: "set",
		X: "set",
		C: "clear",
	}

	witness := DispatchWitness{
		SourceAddress: sourceAddr,
		TargetAddress: targetAddr,
		TargetContext: observedCtx,
		Evidence: []string{
			fmt.Sprintf("run:%s event:%d", streamHash, dispatch.ID),
			fmt.Sprintf("run:%s pointer:%d..%d", streamHash, pReads[0].EventID, pReads[len(pReads)-1].EventID),
			fmt.Sprintf("run:%s event:%d", streamHash, target.ID),
		},
	}

	return &DerivedWitness{
		StreamSHA256:    streamHash,
		ROMSHA256:       romHash,
		DispatchEventID: dispatch.ID,
		TargetEventID:   target.ID,
		PointerReads:    pReads,
		SourceAddress:   sourceAddr,
		TargetAddress:   targetAddr,
		ObservedContext: observedCtx,
		StaticContext:   staticCtx,
		Witness:         witness,
	}, nil
}

// RunWitnessRecovery executes baseline and witness-augmented analysis for a single derived witness.
func RunWitnessRecovery(rom []byte, doc *recovery.Document, derived *DerivedWitness, cfg Config) (*WitnessRecoveryReport, error) {
	return RunCumulativeWitnessRecovery(rom, doc, []*DerivedWitness{derived}, cfg)
}

// RunCumulativeWitnessRecovery executes baseline and cumulative witness-augmented analysis.
// If len(witnesses) == 1, baseline is 0 witnesses and target is witness[0] ($0080C6 -> $0CC120: 9 -> 79, delta +70).
// If len(witnesses) >= 2, baseline is witness[0..len-2] (1-witness: 79 starts) and target is witness[0..len-1] (2-witness: 107 starts, delta +28).
func RunCumulativeWitnessRecovery(rom []byte, doc *recovery.Document, witnesses []*DerivedWitness, cfg Config) (*WitnessRecoveryReport, error) {
	if len(witnesses) == 0 {
		return nil, errors.New("at least one derived witness required")
	}
	if cfg.MaxInstructions <= 0 {
		cfg.MaxInstructions = 5000
	}

	activeIdx := len(witnesses) - 1
	activeDerived := witnesses[activeIdx]

	// 1. Configure baseline witnesses: all except the last active witness
	cfgBaseline := cfg
	var baseWitnesses []DispatchWitness
	for i := 0; i < activeIdx; i++ {
		baseWitnesses = append(baseWitnesses, witnesses[i].Witness)
	}
	cfgBaseline.DispatchWitnesses = baseWitnesses
	resBaseline, err := AnalyzeLoROM(rom, doc, cfgBaseline)
	if err != nil {
		return nil, fmt.Errorf("baseline analysis failed: %w", err)
	}

	// 2. Configure target witnesses: cumulative including the active witness
	cfgWitness := cfg
	var targetWitnesses []DispatchWitness
	for _, w := range witnesses {
		targetWitnesses = append(targetWitnesses, w.Witness)
	}
	cfgWitness.DispatchWitnesses = targetWitnesses
	resWitness, err := AnalyzeLoROM(rom, doc, cfgWitness)
	if err != nil {
		return nil, fmt.Errorf("witness analysis failed: %w", err)
	}

	// Map physical ROM starts
	baseStarts := make(map[uint32]bool)
	for _, inst := range resBaseline.Instructions {
		if off, ok := LoROMToOffset(inst.Address, len(rom)); ok {
			baseStarts[off] = true
		}
	}

	witnessStarts := make(map[uint32]bool)
	var addedOffsets []uint32
	for _, inst := range resWitness.Instructions {
		if off, ok := LoROMToOffset(inst.Address, len(rom)); ok {
			witnessStarts[off] = true
			if !baseStarts[off] {
				addedOffsets = append(addedOffsets, off)
			}
		}
	}
	sort.Slice(addedOffsets, func(i, j int) bool {
		return addedOffsets[i] < addedOffsets[j]
	})

	mode := "reset"
	startAddr := fmt.Sprintf("$%06X", resBaseline.ResetAddress)
	if cfg.SeedAddress != 0 {
		mode = "regional"
		startAddr = fmt.Sprintf("$%06X", cfg.SeedAddress)
	}

	// Obtain static_context from actual baseline dispatch instruction
	var staticCtx recovery.Context
	foundStatic := false
	for _, inst := range resBaseline.Instructions {
		if inst.Address == activeDerived.SourceAddress {
			staticCtx = inst.Context
			foundStatic = true
			break
		}
	}
	if !foundStatic {
		staticCtx = recovery.Context{E: "unknown", M: "unknown", X: "unknown", C: "unknown"}
	}

	var ptrIDs []uint64
	for _, p := range activeDerived.PointerReads {
		ptrIDs = append(ptrIDs, p.EventID)
	}

	return &WitnessRecoveryReport{
		Mode:                   mode,
		EdgeIndex:              activeIdx + 1,
		StartAddress:           startAddr,
		SeedAddress:            startAddr,
		SeedContext:            cfg.SeedContext,
		Budget:                 cfg.MaxInstructions,
		StreamSHA256:           activeDerived.StreamSHA256,
		ROMSHA256:              activeDerived.ROMSHA256,
		DispatchEventID:        activeDerived.DispatchEventID,
		TargetEventID:          activeDerived.TargetEventID,
		PointerEventIDs:        ptrIDs,
		SourceAddress:          fmt.Sprintf("$%06X", activeDerived.SourceAddress),
		TargetAddress:          fmt.Sprintf("$%06X", activeDerived.TargetAddress),
		ObservedContext:        activeDerived.ObservedContext,
		StaticContext:          staticCtx,
		BaselineInstructions:   len(resBaseline.Instructions),
		BaselinePhysicalStarts: len(baseStarts),
		BaselineEdges:          len(resBaseline.Edges),
		WitnessInstructions:    len(resWitness.Instructions),
		WitnessPhysicalStarts:  len(witnessStarts),
		WitnessEdges:           len(resWitness.Edges),
		DeltaInstructions:      len(resWitness.Instructions) - len(resBaseline.Instructions),
		DeltaPhysicalStarts:    len(witnessStarts) - len(baseStarts),
		DeltaEdges:             len(resWitness.Edges) - len(resBaseline.Edges),
		AddedPhysicalOffsets:   addedOffsets,
		UnresolvedIssues:       resWitness.Issues,
	}, nil
}
