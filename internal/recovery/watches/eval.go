package watches

import (
	"encoding/binary"
	"fmt"
	"math/bits"
)

// CheckCycles verifies that there are no circular dependencies among watch conditions.
func CheckCycles(defs []WatchDefinition) error {
	byID := make(map[string]WatchDefinition)
	for _, w := range defs {
		byID[w.ID] = w
	}

	visited := make(map[string]int) // 0: unvisited, 1: visiting, 2: visited
	var path []string

	var dfs func(id string) error
	dfs = func(id string) error {
		visited[id] = 1
		path = append(path, id)

		def, ok := byID[id]
		if ok {
			for _, cond := range def.Conditions {
				depID := cond.WatchID
				if cond.OtherWatchID != nil {
					// Check other watch ID as well
					other := *cond.OtherWatchID
					if visited[other] == 1 {
						return fmt.Errorf("watches: dependency cycle detected: %v -> %s", path, other)
					}
					if visited[other] == 0 {
						if err := dfs(other); err != nil {
							return err
						}
					}
				}
				if depID == id {
					// Condition references self - not a cycle in definition order if self-value
					continue
				}
				if visited[depID] == 1 {
					return fmt.Errorf("watches: dependency cycle detected: %v -> %s", path, depID)
				}
				if visited[depID] == 0 {
					if err := dfs(depID); err != nil {
						return err
					}
				}
			}
		}

		visited[id] = 2
		path = path[:len(path)-1]
		return nil
	}

	for _, w := range defs {
		if visited[w.ID] == 0 {
			if err := dfs(w.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// TopologicalSort orders watches so dependencies are evaluated before dependents.
func TopologicalSort(defs []WatchDefinition) ([]WatchDefinition, error) {
	if err := CheckCycles(defs); err != nil {
		return nil, err
	}

	byID := make(map[string]WatchDefinition)
	for _, w := range defs {
		byID[w.ID] = w
	}

	visited := make(map[string]bool)
	var ordered []WatchDefinition

	var visit func(id string)
	visit = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true

		def, ok := byID[id]
		if !ok {
			return
		}
		for _, cond := range def.Conditions {
			if cond.WatchID != id {
				visit(cond.WatchID)
			}
			if cond.OtherWatchID != nil && *cond.OtherWatchID != id {
				visit(*cond.OtherWatchID)
			}
		}
		ordered = append(ordered, def)
	}

	for _, w := range defs {
		visit(w.ID)
	}

	return ordered, nil
}

// Evaluate evaluates a single watch against a snapshot, given previously evaluated watches.
func Evaluate(def *WatchDefinition, snap *Snapshot, context map[string]*Evaluation) Evaluation {
	eval := Evaluation{
		WatchID:        def.ID,
		Name:           def.Name,
		MemorySpace:    def.MemorySpace,
		Offset:         def.Offset,
		CPUAddress:     def.CPUAddress,
		MeaningSource:  def.MeaningSource,
		Unit:           def.Unit,
		SnapshotFrame:  snap.Frame,
		SnapshotRunID:  snap.RunID,
		DefinitionHash: def.Hash(),
	}

	// 1. Check ROM identity
	if snap.ROMSHA256 != "" && def.Validate() == nil {
		// Validated caller checks ROM match at file level, but we check if snapshot has data
	}

	// 2. Check memory range in snapshot
	snapEnd := uint64(snap.BaseOffset) + uint64(snap.Length)
	defEnd := uint64(def.Offset) + uint64(def.Width)

	if uint64(def.Offset) < uint64(snap.BaseOffset) || defEnd > snapEnd || uint64(len(snap.Data)) < uint64(snap.Length) {
		eval.Validity = ValidityMissing
		eval.ValidityReason = fmt.Sprintf("offset 0x%05x..0x%05x absent from snapshot (range 0x%05x..0x%05x)",
			def.Offset, defEnd, snap.BaseOffset, snapEnd)
		return eval
	}

	// Extract raw bytes
	relOffset := def.Offset - snap.BaseOffset
	rawBytes := make([]byte, def.Width)
	copy(rawBytes, snap.Data[relOffset:relOffset+uint32(def.Width)])
	eval.RawBytes = rawBytes

	// Parse raw value
	var rawVal uint32
	if def.ByteOrder == "big" {
		switch def.Width {
		case 1:
			rawVal = uint32(rawBytes[0])
		case 2:
			rawVal = uint32(binary.BigEndian.Uint16(rawBytes))
		case 3:
			rawVal = (uint32(rawBytes[0]) << 16) | (uint32(rawBytes[1]) << 8) | uint32(rawBytes[2])
		}
	} else {
		switch def.Width {
		case 1:
			rawVal = uint32(rawBytes[0])
		case 2:
			rawVal = uint32(binary.LittleEndian.Uint16(rawBytes))
		case 3:
			rawVal = uint32(rawBytes[0]) | (uint32(rawBytes[1]) << 8) | (uint32(rawBytes[2]) << 16)
		}
	}
	eval.RawValue = rawVal

	// Apply mask & shift
	maskedVal := rawVal
	if def.Mask != nil {
		maskedVal &= *def.Mask
	}
	if def.Shift > 0 {
		maskedVal >>= def.Shift
	}
	eval.MaskedValue = maskedVal

	// Effective bit width
	var effBits int
	if def.Mask != nil {
		m := (*def.Mask) >> def.Shift
		effBits = bits.Len32(m)
		if effBits == 0 {
			effBits = 1
		}
	} else {
		effBits = def.Width*8 - def.Shift
	}
	eval.EffectiveBits = effBits

	// Signed interpretation
	var numVal int64
	if def.Signed {
		signBit := uint32(1) << (effBits - 1)
		if (maskedVal & signBit) != 0 {
			// Negative in two's complement
			numVal = int64(maskedVal) - (int64(1) << effBits)
		} else {
			numVal = int64(maskedVal)
		}
	} else {
		numVal = int64(maskedVal)
	}

	// Scaling
	var scaled float64
	if def.ScaleNumerator != nil || def.ScaleDenominator != nil {
		num := int64(1)
		denom := int64(1)
		if def.ScaleNumerator != nil {
			num = *def.ScaleNumerator
		}
		if def.ScaleDenominator != nil {
			denom = *def.ScaleDenominator
		}
		scaled = float64(numVal*num) / float64(denom)
	} else {
		scaled = float64(numVal)
	}
	eval.DecodedNumber = scaled

	// Enum mapping
	hexKey := fmt.Sprintf("0x%X", maskedVal)
	decKey := fmt.Sprintf("%d", numVal)
	if label, ok := def.EnumLabels[decKey]; ok {
		eval.EnumLabel = label
		eval.DecodedString = label
	} else if label, ok := def.EnumLabels[hexKey]; ok {
		eval.EnumLabel = label
		eval.DecodedString = label
	} else {
		// Unknown enum retains raw number
		if def.ScaleNumerator != nil || def.ScaleDenominator != nil {
			if def.Unit != "" {
				eval.DecodedString = fmt.Sprintf("%g %s", scaled, def.Unit)
			} else {
				eval.DecodedString = fmt.Sprintf("%g", scaled)
			}
		} else {
			if def.Unit != "" {
				eval.DecodedString = fmt.Sprintf("%d %s", numVal, def.Unit)
			} else {
				eval.DecodedString = fmt.Sprintf("%d", numVal)
			}
		}
	}

	// 3. Evaluate validity conditions
	validity := ValidityValid
	validityReason := ""

	for _, cond := range def.Conditions {
		var opVal int64
		if cond.WatchID == def.ID {
			opVal = numVal
		} else {
			depEval, ok := context[cond.WatchID]
			if !ok || depEval.Validity == ValidityMissing || depEval.Validity == ValidityUnknown {
				validity = ValidityUnknown
				validityReason = fmt.Sprintf("condition input %q is missing or unknown", cond.WatchID)
				break
			}
			if depEval.Validity == ValidityInvalidInContext {
				validity = ValidityInvalidInContext
				validityReason = fmt.Sprintf("condition dependency %q is invalid in context", cond.WatchID)
				break
			}
			opVal = int64(depEval.MaskedValue)
		}

		var targetVal int64
		if cond.OtherWatchID != nil {
			otherEval, ok := context[*cond.OtherWatchID]
			if !ok || otherEval.Validity == ValidityMissing || otherEval.Validity == ValidityUnknown {
				validity = ValidityUnknown
				validityReason = fmt.Sprintf("condition target watch %q is missing or unknown", *cond.OtherWatchID)
				break
			}
			targetVal = int64(otherEval.MaskedValue)
		} else if cond.Value != nil {
			targetVal = *cond.Value
		}

		var passed bool
		switch cond.Op {
		case "==":
			passed = (opVal == targetVal)
		case "!=":
			passed = (opVal != targetVal)
		case "<":
			passed = (opVal < targetVal)
		case "<=":
			passed = (opVal <= targetVal)
		case ">":
			passed = (opVal > targetVal)
		case ">=":
			passed = (opVal >= targetVal)
		}

		if !passed {
			validity = ValidityInvalidInContext
			validityReason = fmt.Sprintf("condition failed: %s %s %d (got %d)", cond.WatchID, cond.Op, targetVal, opVal)
			break
		}
	}

	eval.Validity = validity
	eval.ValidityReason = validityReason
	return eval
}

// EvaluateAll evaluates all watches in a File against a snapshot.
func EvaluateAll(wf *File, snap *Snapshot) (map[string]Evaluation, error) {
	if wf.ROMSHA256 != "" && snap.ROMSHA256 != "" && wf.ROMSHA256 != snap.ROMSHA256 {
		return nil, fmt.Errorf("watches: ROM hash mismatch (definitions: %s, snapshot: %s)",
			wf.ROMSHA256, snap.ROMSHA256)
	}

	ordered, err := TopologicalSort(wf.Watches)
	if err != nil {
		return nil, err
	}

	results := make(map[string]Evaluation)
	ptrMap := make(map[string]*Evaluation)
	for _, def := range ordered {
		if err := def.Validate(); err != nil {
			return nil, fmt.Errorf("watches: definition %q invalid: %w", def.ID, err)
		}
		res := Evaluate(&def, snap, ptrMap)
		results[def.ID] = res
		ptrMap[def.ID] = &res
	}

	return results, nil
}
