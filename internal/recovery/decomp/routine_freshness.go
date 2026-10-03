package decomp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/tmc/snes/internal/recovery"
)

// BindRegion binds a runner to an owned region and current project revision.
// The region must generate exactly the source compiled by this runner.
// RunBatch permits unbound synthetic execution; captured replay requires binding.
func (r *CompiledRoutineRunner) BindRegion(region *RegionIR, projectRevision string) error {
	if r == nil || region == nil {
		return errors.New("missing runner or region")
	}
	if projectRevision == "" {
		return errors.New("missing project revision")
	}
	owned, err := cloneRoutineRegion(region)
	if err != nil {
		return err
	}
	code, err := GenerateRegionC(owned)
	if err != nil {
		return fmt.Errorf("generate bound region: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Closed {
		return errors.New("runner is closed")
	}
	if code != r.sourceCode {
		return errors.New("region does not generate compiled source")
	}
	r.boundRegion = owned
	r.boundRevision = projectRevision
	r.boundNames = nil
	r.boundNamedSource = NamedRegionSource{}
	return nil
}

// BindNamedRegion binds an exact generated C translation unit, exported
// accessor fragment, and authored symbol annotations to an owned region.
// Evidence on a ByteSymbol is an annotation, not capture admission.
func (r *CompiledRoutineRunner) BindNamedRegion(region *RegionIR, named NamedRegionSource, symbols []ByteSymbol, projectRevision string) error {
	if r == nil || region == nil || projectRevision == "" {
		return errors.New("missing runner, region, or project revision")
	}
	owned, err := cloneRoutineRegion(region)
	if err != nil {
		return err
	}
	generated, err := GenerateNamedRegionC(owned, symbols)
	if err != nil {
		return fmt.Errorf("generate named region: %w", err)
	}
	if generated != named {
		return errors.New("named source or accessor fragment differs from generated region")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Closed {
		return errors.New("runner is closed")
	}
	if named.Source != r.sourceCode {
		return errors.New("named region does not generate compiled source")
	}
	if err := namedRegionROM(owned, r.romBytes); err != nil {
		return err
	}
	r.boundRegion = owned
	r.boundRevision = projectRevision
	r.boundNames = append([]ByteSymbol(nil), symbols...)
	r.boundNamedSource = named
	return nil
}

func namedBindingHash(named NamedRegionSource, symbols []ByteSymbol) string {
	ordered := append([]ByteSymbol(nil), symbols...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Address != ordered[j].Address {
			return ordered[i].Address < ordered[j].Address
		}
		return ordered[i].Name < ordered[j].Name
	})
	b, _ := json.Marshal(ordered)
	h := sha256.New()
	h.Write([]byte("named_region_binding_v1\n"))
	fmt.Fprintf(h, "db_mirror:%t\n", named.RequiresDBMirror)
	h.Write(b)
	h.Write([]byte(named.VariablesH))
	h.Write([]byte(named.Source))
	return fmt.Sprintf("%x", h.Sum(nil))
}

// namedRegionROM checks every retained instruction byte against the runner's
// pinned LoROM image. The IR and generated source are checked separately.
func namedRegionROM(region *RegionIR, rom []byte) error {
	if len(rom) == 0 {
		return errors.New("named region has no pinned ROM")
	}
	count := 0
	for _, block := range region.Blocks {
		for _, insn := range block.Instructions {
			code, err := hex.DecodeString(insn.Bytes)
			if err != nil || len(code) == 0 || code[0] != insn.Opcode {
				return fmt.Errorf("named region instruction bytes invalid at $%06X", insn.Address)
			}
			for i, b := range code {
				addr := insn.Address&0xff0000 | uint32(uint16(insn.Address)+uint16(i))
				actual, err := romByte(rom, addr)
				if err != nil || actual != b {
					return fmt.Errorf("named region instruction differs from pinned ROM at $%06X", addr)
				}
			}
			count++
		}
	}
	if count == 0 {
		return errors.New("named region has no instructions")
	}
	return nil
}

func cloneRoutineRegion(region *RegionIR) (*RegionIR, error) {
	if len(region.Blocks) == 0 {
		return nil, errors.New("region has no blocks")
	}
	for _, block := range region.Blocks {
		if block == nil {
			return nil, errors.New("region contains nil block")
		}
	}
	b, err := json.Marshal(region)
	if err != nil {
		return nil, fmt.Errorf("encode region identity: %w", err)
	}
	var owned RegionIR
	if err := json.Unmarshal(b, &owned); err != nil {
		return nil, fmt.Errorf("decode region identity: %w", err)
	}
	for i, block := range region.Blocks {
		for j, stmt := range block.Statements {
			out := &owned.Blocks[i].Statements[j]
			for _, pair := range []struct {
				src Expr
				dst *Expr
			}{{stmt.Expr, &out.Expr}, {stmt.MemAddress, &out.MemAddress}, {stmt.Condition, &out.Condition}} {
				cloned, err := cloneRoutineExpr(pair.src)
				if err != nil {
					return nil, err
				}
				*pair.dst = cloned
			}
		}
	}
	owned.ROMBytes = append([]byte(nil), region.ROMBytes...)
	return &owned, nil
}

func routineRegionHash(region *RegionIR) string {
	b, _ := json.Marshal(region)
	h := sha256.New()
	h.Write([]byte("routine_region_v1\n"))
	h.Write(b)
	code, err := GenerateRegionC(region)
	if err != nil {
		return ""
	}
	h.Write([]byte(code))
	h.Write(region.ROMBytes)
	return fmt.Sprintf("%x", h.Sum(nil))
}

type routineBinding struct {
	region     *RegionIR
	sourceCode string
	metadata   ReceiptMetadata
}

func (r *CompiledRoutineRunner) replayBinding(c ReplayCase) (routineBinding, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b routineBinding
	if r.Closed {
		return b, errors.New("runner is closed")
	}
	if r.boundRegion == nil || r.boundRevision == "" {
		return b, errors.New("runner has no bound region and project revision")
	}
	if r.boundRegion.EntryAddress != (uint32(c.InitialState.PB)<<16 | uint32(c.InitialState.PC)) {
		return b, errors.New("case entry differs from bound region")
	}
	if r.GeneratedCHash != ComputeCHash(r.sourceCode) || r.Compiler != r.observedCompiler || r.CompilerFlags != r.observedFlags {
		return b, errors.New("runner identity changed")
	}
	binary, err := os.ReadFile(r.BinPath)
	if err != nil {
		return b, fmt.Errorf("read current runner: %w", err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(binary)) != r.binaryHash {
		return b, errors.New("runner binary changed")
	}
	mem := make(map[uint32]uint8, len(c.InitialMemory))
	for _, m := range c.InitialMemory {
		if _, ok := mem[m.Address]; ok {
			return b, errors.New("duplicate initial memory address")
		}
		mem[m.Address] = m.Value
	}
	memHash, _ := ComputeInitialMemory(mem)
	region, err := cloneRoutineRegion(r.boundRegion)
	if err != nil {
		return b, err
	}
	var namedHash, nameAuthority string
	if len(r.boundNames) != 0 {
		if c.InitialState.D != 0 {
			return b, errors.New("named region requires D=0 for WRAM mirror addresses")
		}
		if r.boundNamedSource.RequiresDBMirror && !lowWRAMMirrorBank(c.InitialState.DB) {
			return b, errors.New("named region data bank does not map low WRAM mirror")
		}
		if err := namedRegionROM(region, r.romBytes); err != nil {
			return b, err
		}
		generated, err := GenerateNamedRegionC(region, r.boundNames)
		if err != nil || generated != r.boundNamedSource || generated.Source != r.sourceCode {
			return b, errors.New("named region binding changed")
		}
		namedHash = namedBindingHash(generated, r.boundNames)
		nameAuthority = "authored"
	} else {
		generated, err := GenerateRegionC(region)
		if err != nil || generated != r.sourceCode {
			return b, errors.New("region binding changed")
		}
	}
	id := fmt.Sprintf("routine_runner_v1\n%s\n%s\n%s\n%s\n%s\n", r.binaryHash, r.wrapperHash, r.RoutineID, r.observedCompiler, r.observedFlags)
	b = routineBinding{region: region, sourceCode: r.sourceCode, metadata: ReceiptMetadata{
		ProjectRevision: r.boundRevision, ROMSHA256: r.romSHA256, BlockID: c.RoutineID, StartAddress: region.EntryAddress,
		CodeHash: routineRegionHash(region), GeneratedCHash: ComputeCHash(r.sourceCode), Compiler: r.observedCompiler, CompilerFlags: r.observedFlags,
		NamedBindingSHA256: namedHash, NamedSymbolAuthority: nameAuthority,
		RunnerHash: ComputeCHash(id), Context: routineEntryContext(c.InitialState), MemoryPolicy: "snes_wram_mirror_v1", InitialMemHash: memHash, InitialCPUStateHash: ComputeCPUStateHash(c.InitialState),
	}}
	return b, nil
}

func lowWRAMMirrorBank(db uint8) bool {
	return db <= 0x3f || (db >= 0x80 && db <= 0xbf)
}

// ValidateRoutineReplayReceiptFreshness checks a persisted routine receipt against
// live project inputs and the current compiled runner. Expected identities must
// come from the current project, not from fields in the loaded receipt.
// A mismatch clears both eligibility flags. Admission alone cannot restore them.
func ValidateRoutineReplayReceiptFreshness(receipt *ReplayReceipt, currentCase *ReplayCase, currentRegion *RegionIR, currentC, expectedROM, expectedRevision string, runner *CompiledRoutineRunner) {
	defaultEvidenceVerifier.ValidateRoutineReplayReceiptFreshness(receipt, currentCase, currentRegion, currentC, expectedROM, expectedRevision, runner)
}

// ValidateNamedRoutineReplayReceiptFreshness checks the current authored
// symbols and accessor fragment as well as the ordinary captured replay inputs.
// Use the verifier that admitted the case; the default verifier has no authority.
func (v *EvidenceVerifier) ValidateNamedRoutineReplayReceiptFreshness(receipt *ReplayReceipt, currentCase *ReplayCase, currentRegion *RegionIR, currentNamed NamedRegionSource, currentSymbols []ByteSymbol, expectedROM, expectedRevision string, runner *CompiledRoutineRunner) {
	if receipt == nil {
		return
	}
	receipt.Eligible = false
	receipt.CapturedProofEligible = false
	stale := func(reason string) {
		receipt.Metadata.IsStale = true
		receipt.Metadata.StaleReason = reason
	}
	if currentRegion == nil || runner == nil || len(currentSymbols) == 0 {
		stale("missing current named region inputs")
		return
	}
	generated, err := GenerateNamedRegionC(currentRegion, currentSymbols)
	if err != nil || generated != currentNamed {
		stale("current named source or accessor fragment changed")
		return
	}
	if receipt.Metadata.NamedSymbolAuthority != "authored" || receipt.Metadata.NamedBindingSHA256 != namedBindingHash(generated, currentSymbols) {
		stale("named symbols or source differ from receipt")
		return
	}
	v.ValidateRoutineReplayReceiptFreshness(receipt, currentCase, currentRegion, currentNamed.Source, expectedROM, expectedRevision, runner)
}

// ValidateRoutineReplayReceiptFreshness checks authority owned by this verifier.
func (v *EvidenceVerifier) ValidateRoutineReplayReceiptFreshness(receipt *ReplayReceipt, currentCase *ReplayCase, currentRegion *RegionIR, currentC, expectedROM, expectedRevision string, runner *CompiledRoutineRunner) {
	if receipt == nil {
		return
	}
	receipt.Eligible = false
	receipt.CapturedProofEligible = false
	fail := func(reason string) { receipt.Metadata.IsStale = true; receipt.Metadata.StaleReason = reason }
	if currentCase == nil || currentRegion == nil || runner == nil {
		fail("missing current routine inputs")
		return
	}
	if currentC == "" || expectedROM == "" || expectedRevision == "" {
		fail("missing current routine identity")
		return
	}
	if receipt.Metadata.IsStale {
		return
	}
	binding, err := runner.replayBinding(*currentCase)
	if err != nil {
		fail(err.Error())
		return
	}
	liveRegion, err := cloneRoutineRegion(currentRegion)
	if err != nil {
		fail(err.Error())
		return
	}
	currentRegion = liveRegion
	expected := binding.metadata
	if expected.NamedBindingSHA256 != "" && (v == nil || v.policy == nil || v.policy.namedBindings[currentCase.RoutineID] != expected.NamedBindingSHA256) {
		fail("named binding lacks reviewed policy pin")
		return
	}
	if expected.ProjectRevision != expectedRevision || expected.ROMSHA256 != expectedROM || currentCase.ROMSHA256 != expectedROM {
		fail("current project or ROM differs from runner binding")
		return
	}
	if expected.GeneratedCHash != ComputeCHash(currentC) || expected.CodeHash != routineRegionHash(currentRegion) {
		fail("current region or source differs from runner binding")
		return
	}
	actual := receipt.Metadata
	if actual.ProjectRevision != expected.ProjectRevision || actual.ROMSHA256 != expected.ROMSHA256 || actual.BlockID != expected.BlockID || actual.StartAddress != expected.StartAddress || actual.CodeHash != expected.CodeHash || actual.GeneratedCHash != expected.GeneratedCHash || actual.NamedBindingSHA256 != expected.NamedBindingSHA256 || actual.NamedSymbolAuthority != expected.NamedSymbolAuthority || actual.Compiler != expected.Compiler || actual.CompilerFlags != expected.CompilerFlags || actual.RunnerHash != expected.RunnerHash || actual.Context != expected.Context || actual.MemoryPolicy != expected.MemoryPolicy || actual.InitialMemHash != expected.InitialMemHash || actual.InitialCPUStateHash != expected.InitialCPUStateHash {
		fail("routine receipt identity differs from current inputs")
		return
	}
	hash := ComputeCaseHash(*currentCase)
	if currentCase.CaseHash != hash || receipt.CaseHash != hash || receipt.CaseIdentity != currentCase.Identity() || receipt.CaseID != currentCase.CaseID || receipt.BlockID != currentCase.RoutineID {
		fail("routine case changed")
		return
	}
	if !currentCase.ObservedEffectsCapture || currentCase.AdmissionDigest == "" || receipt.AdmissionDigest != currentCase.AdmissionDigest || !v.IsAdmitted(hash, currentCase.AdmissionDigest) {
		fail("routine case is not currently admitted")
		return
	}
	if !receipt.Matched || !receipt.EffectsMatch || !receipt.EmulatorMatch || !receipt.ObservedMatch {
		fail("routine receipt did not match all comparisons")
		return
	}
	for _, result := range []ExecResult{receipt.CompiledC, receipt.ReferenceEmu, receipt.TraceObserved} {
		if ok, _ := CompareCPUStates(currentCase.ObservedExit, result.State); !ok {
			fail("routine receipt CPU result changed")
			return
		}
		if ok, _ := CompareWrites(currentCase.ObservedWrites, result.Writes); !ok {
			fail("routine receipt writes changed")
			return
		}
		if result.NextPC != currentCase.ObservedNextPC || result.TotalWrites != uint32(len(currentCase.ObservedWrites)) || result.MissingRead || result.MMIOAccess || result.WriteOverflow {
			fail("routine receipt result is incomplete")
			return
		}
	}
	receipt.Eligible = true
	receipt.CapturedProofEligible = true
}

func cloneRoutineExpr(expr Expr) (Expr, error) { return cloneRoutineExprDepth(expr, 0) }

func cloneRoutineExprDepth(expr Expr, depth int) (Expr, error) {
	if depth > 256 {
		return nil, errors.New("region expression nesting exceeds limit")
	}
	if expr == nil {
		return nil, nil
	}
	switch e := expr.(type) {
	case *ConstExpr:
		if e == nil {
			return nil, errors.New("region contains nil expression")
		}
		v := *e
		return &v, nil
	case *RegExpr:
		if e == nil {
			return nil, errors.New("region contains nil expression")
		}
		v := *e
		return &v, nil
	case *FlagExpr:
		if e == nil {
			return nil, errors.New("region contains nil expression")
		}
		v := *e
		return &v, nil
	case *TempExpr:
		if e == nil {
			return nil, errors.New("region contains nil expression")
		}
		v := *e
		return &v, nil
	case *BinaryExpr:
		if e == nil {
			return nil, errors.New("region contains nil expression")
		}
		l, err := cloneRoutineExprDepth(e.Left, depth+1)
		if err != nil {
			return nil, err
		}
		r, err := cloneRoutineExprDepth(e.Right, depth+1)
		if err != nil {
			return nil, err
		}
		v := *e
		v.Left = l
		v.Right = r
		return &v, nil
	case *UnaryExpr:
		if e == nil {
			return nil, errors.New("region contains nil expression")
		}
		x, err := cloneRoutineExprDepth(e.Expr, depth+1)
		if err != nil {
			return nil, err
		}
		v := *e
		v.Expr = x
		return &v, nil
	case *MemReadExpr:
		if e == nil {
			return nil, errors.New("region contains nil expression")
		}
		x, err := cloneRoutineExprDepth(e.Address, depth+1)
		if err != nil {
			return nil, err
		}
		v := *e
		v.Address = x
		return &v, nil
	default:
		return nil, fmt.Errorf("unsupported region expression %T", expr)
	}
}

func routineEntryContext(state CPUState) recovery.Context {
	flag := func(set bool) string {
		if set {
			return "set"
		}
		return "clear"
	}
	return recovery.Context{E: flag(state.E), M: flag(state.P&0x20 != 0), X: flag(state.P&0x10 != 0), C: flag(state.P&1 != 0)}
}
