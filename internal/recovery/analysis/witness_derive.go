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
	StartAddress           string           `json:"start_address"`
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

// DeriveWitnessFromTrace parses raw JSONL trace bytes and derives the dispatch witness
// for events 29890..29892, 29893, and 29897.
func DeriveWitnessFromTrace(traceBytes, rom []byte) (*DerivedWitness, error) {
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
		dispatch trace.Event
		target   trace.Event
		pReads   []PointerRead
	)

	for dec.More() {
		var e trace.Event
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("decoding trace event: %w", err)
		}
		switch e.ID {
		case 29890, 29891, 29892:
			pReads = append(pReads, PointerRead{
				EventID: e.ID,
				Address: e.Addr,
				Value:   byte(e.Value),
			})
		case 29893:
			dispatch = e
		case 29897:
			target = e
		}
	}

	if len(pReads) != 3 {
		return nil, fmt.Errorf("expected 3 pointer read events (29890..29892), found %d", len(pReads))
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

// RunWitnessRecovery executes baseline and witness-augmented analysis, returning a detailed delta report.
func RunWitnessRecovery(rom []byte, doc *recovery.Document, derived *DerivedWitness, cfg Config) (*WitnessRecoveryReport, error) {
	if cfg.MaxInstructions <= 0 {
		cfg.MaxInstructions = 5000
	}

	// 1. Run baseline
	cfgBaseline := cfg
	cfgBaseline.DispatchWitnesses = nil
	resBaseline, err := AnalyzeLoROM(rom, doc, cfgBaseline)
	if err != nil {
		return nil, fmt.Errorf("baseline analysis failed: %w", err)
	}

	// 2. Run with derived witness
	cfgWitness := cfg
	cfgWitness.DispatchWitnesses = []DispatchWitness{derived.Witness}
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
		if inst.Address == derived.SourceAddress {
			staticCtx = inst.Context
			foundStatic = true
			break
		}
	}
	if !foundStatic {
		staticCtx = recovery.Context{E: "unknown", M: "unknown", X: "unknown", C: "unknown"}
	}

	var ptrIDs []uint64
	for _, p := range derived.PointerReads {
		ptrIDs = append(ptrIDs, p.EventID)
	}

	return &WitnessRecoveryReport{
		Mode:                   mode,
		StartAddress:           startAddr,
		StreamSHA256:           derived.StreamSHA256,
		ROMSHA256:              derived.ROMSHA256,
		DispatchEventID:        derived.DispatchEventID,
		TargetEventID:          derived.TargetEventID,
		PointerEventIDs:        ptrIDs,
		SourceAddress:          fmt.Sprintf("$%06X", derived.SourceAddress),
		TargetAddress:          fmt.Sprintf("$%06X", derived.TargetAddress),
		ObservedContext:        derived.ObservedContext,
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
