package asmexport

import (
	"encoding/hex"

	"github.com/tmc/snes/internal/recovery"
)

type instSpan struct {
	inst  recovery.Instruction
	start uint32
	end   uint32
}

// buildInstructionPlan identifies unambiguous instructions and marks conflicting/overlapping regions for raw byte fallback.
func buildInstructionPlan(instructions []recovery.Instruction, bankStart, bankEnd uint32) map[uint32]recovery.Instruction {
	var validSpans []instSpan

	// 1. Filter and parse spans for this bank.
	for _, inst := range instructions {
		if inst.Offset < bankStart || inst.Offset >= bankEnd {
			continue
		}
		raw, err := hex.DecodeString(inst.Bytes)
		if err != nil || len(raw) == 0 {
			continue
		}
		end := inst.Offset + uint32(len(raw))
		if end > bankEnd {
			continue
		}
		validSpans = append(validSpans, instSpan{
			inst:  inst,
			start: inst.Offset,
			end:   end,
		})
	}

	// 2. Identify conflicting spans (multiple candidates at same offset or overlapping spans).
	conflicted := make(map[uint32]bool)

	// Check duplicates at same offset with differing bytes.
	byOffset := make(map[uint32][]instSpan)
	for _, s := range validSpans {
		byOffset[s.start] = append(byOffset[s.start], s)
	}
	for _, spans := range byOffset {
		if len(spans) > 1 {
			firstBytes := spans[0].inst.Bytes
			for _, s := range spans[1:] {
				if s.inst.Bytes != firstBytes {
					// Conflicting candidate bytes at same offset.
					for _, span := range spans {
						for o := span.start; o < span.end; o++ {
							conflicted[o] = true
						}
					}
					break
				}
			}
		}
	}

	// Check overlapping spans.
	for i := 0; i < len(validSpans); i++ {
		for j := i + 1; j < len(validSpans); j++ {
			s1, s2 := validSpans[i], validSpans[j]
			if s1.start > s2.start {
				s1, s2 = s2, s1
			}
			if s1.start < s2.start && s2.start < s1.end {
				// Overlap detected: mark union span as conflicted.
				maxEnd := s1.end
				if s2.end > maxEnd {
					maxEnd = s2.end
				}
				for o := s1.start; o < maxEnd; o++ {
					conflicted[o] = true
				}
			}
		}
	}

	// 3. Retain only unconflicted instructions.
	plan := make(map[uint32]recovery.Instruction)
	for _, s := range validSpans {
		isConflicted := false
		for o := s.start; o < s.end; o++ {
			if conflicted[o] {
				isConflicted = true
				break
			}
		}
		if !isConflicted {
			if _, exists := plan[s.start]; !exists {
				plan[s.start] = s.inst
			}
		}
	}

	return plan
}
