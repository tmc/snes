package analysis

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/recovery"
)

// DispatchTable represents a recovered indexed 16-bit indirect jump table.
type DispatchTable struct {
	BaseAddress uint32   // 24-bit SNES bus address of the table
	BaseOffset  uint32   // Physical offset in ROM
	EntryCount  int      // Number of entries N
	Targets     []uint32 // 24-bit SNES bus addresses of valid handler targets
}

// RecoverDispatchTable attempts to recover an indexed 16-bit jump table for a
// JMP ($abs,X) instruction (opcode 0x7C) using the preceding basic block context.
func RecoverDispatchTable(rom []byte, jmpInst recovery.Instruction, preceding []recovery.Instruction) (*DispatchTable, error) {
	if jmpInst.Opcode != 0x7C {
		return nil, fmt.Errorf("dispatch: expected opcode 0x7C, got 0x%02X", jmpInst.Opcode)
	}

	var instBytes []byte
	if len(jmpInst.Bytes) >= 6 {
		var err error
		instBytes, err = hex.DecodeString(jmpInst.Bytes)
		if err != nil || len(instBytes) < 3 {
			instBytes = nil
		}
	}
	if len(instBytes) < 3 {
		if int(jmpInst.Offset)+3 <= len(rom) {
			instBytes = rom[jmpInst.Offset : jmpInst.Offset+3]
		} else {
			return nil, errors.New("dispatch: instruction extends past end of ROM")
		}
	}

	tableBase16 := binary.LittleEndian.Uint16(instBytes[1:3])
	if tableBase16 < 0x8000 {
		return nil, fmt.Errorf("dispatch: table base $0x%04X is below $8000 (RAM/MMIO)", tableBase16)
	}

	tableBase := (jmpInst.Address & 0xFF0000) | uint32(tableBase16)
	baseOffset, ok := LoROMToOffset(tableBase, len(rom))
	if !ok {
		return nil, fmt.Errorf("dispatch: table base 0x%06X is not mapped in LoROM", tableBase)
	}

	n, err := extractTableBounds(preceding)
	if err != nil {
		return nil, fmt.Errorf("dispatch: %w", err)
	}
	if n <= 0 || n > 256 {
		return nil, fmt.Errorf("dispatch: invalid entry count %d", n)
	}

	targets, err := validateTableTargets(rom, tableBase, baseOffset, n)
	if err != nil {
		return nil, fmt.Errorf("dispatch: invalid targets: %w", err)
	}

	return &DispatchTable{
		BaseAddress: tableBase,
		BaseOffset:  baseOffset,
		EntryCount:  n,
		Targets:     targets,
	}, nil
}

// extractTableBounds inspects the basic block preceding JMP ($abs,X) to identify
// the table bound N from an immediate CMP or CPX instruction.
func extractTableBounds(preceding []recovery.Instruction) (int, error) {
	if len(preceding) == 0 {
		return 0, errors.New("unbounded dispatch table: no preceding instructions")
	}

	// Find the most recent comparison instruction before the jump.
	cmpIdx := -1
	for i := len(preceding) - 1; i >= 0; i-- {
		op := preceding[i].Opcode
		if op == 0xC9 || op == 0xE0 || op == 0xC0 {
			cmpIdx = i
			break
		}
	}
	if cmpIdx == -1 {
		return 0, errors.New("unbounded dispatch table: no bounding comparison found")
	}

	cmpInst := preceding[cmpIdx]
	raw, err := hex.DecodeString(cmpInst.Bytes)
	if err != nil || len(raw) < 2 {
		return 0, errors.New("failed to decode comparison immediate operand")
	}

	var imm uint16
	if len(raw) == 2 {
		imm = uint16(raw[1])
	} else {
		imm = binary.LittleEndian.Uint16(raw[1:3])
	}
	if imm == 0 {
		return 0, errors.New("zero bound in comparison")
	}

	// Find if an ASL instruction exists in the preceding sequence.
	aslIdx := -1
	for i := len(preceding) - 1; i >= 0; i-- {
		op := preceding[i].Opcode
		if op == 0x0A || op == 0x0E || op == 0x06 {
			aslIdx = i
			break
		}
	}

	if aslIdx != -1 {
		if aslIdx < cmpIdx {
			// ASL preceded CMP: selector was doubled before comparison,
			// so imm is the byte offset bound.
			return int(imm / 2), nil
		}
		// ASL followed CMP: un-doubled selector count was compared.
		return int(imm), nil
	}

	// No ASL found.
	// If CPX was used, X is the byte offset index for JMP ($abs,X).
	if cmpInst.Opcode == 0xE0 {
		return int(imm / 2), nil
	}

	// For CMP without ASL, if imm is even, imm/2 is the byte offset bound.
	if imm%2 == 0 {
		return int(imm / 2), nil
	}
	return int(imm), nil
}

// validateTableTargets reads N 16-bit target offsets from the ROM table in LoROM
// mapping and validates each target as plausible code within mapped ROM.
func validateTableTargets(rom []byte, tableBase uint32, baseOffset uint32, n int) ([]uint32, error) {
	if n <= 0 {
		return nil, errors.New("entry count must be positive")
	}
	tableBytes := n * 2
	if int(baseOffset)+tableBytes > len(rom) {
		return nil, fmt.Errorf("table at offset 0x%06X (%d bytes) extends past end of ROM (%d bytes)", baseOffset, tableBytes, len(rom))
	}

	var targets []uint32
	for i := 0; i < n; i++ {
		off := baseOffset + uint32(i*2)
		target16 := binary.LittleEndian.Uint16(rom[off : off+2])
		if target16 < 0x8000 {
			return nil, fmt.Errorf("target %d ($%04X) is in RAM/MMIO below $8000", i, target16)
		}

		targetAddr := (tableBase & 0xFF0000) | uint32(target16)
		targetOff, ok := LoROMToOffset(targetAddr, len(rom))
		if !ok {
			return nil, fmt.Errorf("target %d ($%06X) is outside ROM bounds", i, targetAddr)
		}

		opByte := rom[targetOff]
		op := cpu.Opcodes[opByte]
		if op.Op == nil {
			return nil, fmt.Errorf("target %d at 0x%06X has unrecognized opcode 0x%02X", i, targetAddr, opByte)
		}
		if opByte == 0x00 {
			return nil, fmt.Errorf("target %d at 0x%06X begins with BRK (0x00)", i, targetAddr)
		}

		targets = append(targets, targetAddr)
	}

	return targets, nil
}
