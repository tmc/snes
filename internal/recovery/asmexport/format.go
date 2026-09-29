package asmexport

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/tmc/snes/internal/recovery"
)

// ensureSuffix appends suffix if mnemonic does not already contain a dot suffix.
func ensureSuffix(mnemonic, suffix string) string {
	if strings.Contains(mnemonic, ".") {
		return mnemonic
	}
	return mnemonic + suffix
}

// FormatInstructionASM formats a recovery.Instruction into standard assembly syntax for snesasm.
func FormatInstructionASM(inst recovery.Instruction) (string, error) {
	raw, err := hex.DecodeString(inst.Bytes)
	if err != nil {
		return "", fmt.Errorf("asmexport: invalid instruction bytes %q: %w", inst.Bytes, err)
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("asmexport: empty instruction bytes for %s", inst.ID)
	}

	mnemonic := strings.ToLower(inst.Mnemonic)

	switch inst.Mode {
	case "implied":
		return mnemonic, nil

	case "accumulator":
		return mnemonic + " a", nil

	case "immediate":
		if len(raw) == 2 {
			return fmt.Sprintf("%s #$%02X", mnemonic, raw[1]), nil
		} else if len(raw) == 3 {
			val := binary.LittleEndian.Uint16(raw[1:3])
			return fmt.Sprintf("%s #$%04X", mnemonic, val), nil
		}
		return "", fmt.Errorf("asmexport: unsupported immediate length %d", len(raw))

	case "direct_page":
		if len(raw) < 2 {
			return "", fmt.Errorf("asmexport: short direct_page instruction")
		}
		return fmt.Sprintf("%s $%02X", ensureSuffix(mnemonic, ".b"), raw[1]), nil

	case "direct_page_x":
		if len(raw) < 2 {
			return "", fmt.Errorf("asmexport: short direct_page_x instruction")
		}
		return fmt.Sprintf("%s $%02X,x", ensureSuffix(mnemonic, ".b"), raw[1]), nil

	case "direct_page_y":
		if len(raw) < 2 {
			return "", fmt.Errorf("asmexport: short direct_page_y instruction")
		}
		return fmt.Sprintf("%s $%02X,y", ensureSuffix(mnemonic, ".b"), raw[1]), nil

	case "absolute":
		if len(raw) < 3 {
			return "", fmt.Errorf("asmexport: short absolute instruction")
		}
		val := binary.LittleEndian.Uint16(raw[1:3])
		return fmt.Sprintf("%s $%04X", ensureSuffix(mnemonic, ".w"), val), nil

	case "absolute_x":
		if len(raw) < 3 {
			return "", fmt.Errorf("asmexport: short absolute_x instruction")
		}
		val := binary.LittleEndian.Uint16(raw[1:3])
		return fmt.Sprintf("%s $%04X,x", ensureSuffix(mnemonic, ".w"), val), nil

	case "absolute_y":
		if len(raw) < 3 {
			return "", fmt.Errorf("asmexport: short absolute_y instruction")
		}
		val := binary.LittleEndian.Uint16(raw[1:3])
		return fmt.Sprintf("%s $%04X,y", ensureSuffix(mnemonic, ".w"), val), nil

	case "long":
		if len(raw) < 4 {
			return "", fmt.Errorf("asmexport: short long instruction")
		}
		val := uint32(raw[1]) | (uint32(raw[2]) << 8) | (uint32(raw[3]) << 16)
		return fmt.Sprintf("%s $%06X", ensureSuffix(mnemonic, ".l"), val), nil

	case "long_x":
		if len(raw) < 4 {
			return "", fmt.Errorf("asmexport: short long_x instruction")
		}
		val := uint32(raw[1]) | (uint32(raw[2]) << 8) | (uint32(raw[3]) << 16)
		return fmt.Sprintf("%s $%06X,x", ensureSuffix(mnemonic, ".l"), val), nil

	case "relative":
		if len(raw) < 2 {
			return "", fmt.Errorf("asmexport: short relative instruction")
		}
		rel := int8(raw[1])
		target := uint32(int32(inst.Address) + 2 + int32(rel))
		return fmt.Sprintf("%s $%04X", mnemonic, target&0xFFFF), nil

	case "relative_long":
		if len(raw) < 3 {
			return "", fmt.Errorf("asmexport: short relative_long instruction")
		}
		rel := int16(binary.LittleEndian.Uint16(raw[1:3]))
		target := uint32(int32(inst.Address) + 3 + int32(rel))
		return fmt.Sprintf("%s $%04X", mnemonic, target&0xFFFF), nil

	case "direct_indirect":
		if len(raw) < 2 {
			return "", fmt.Errorf("asmexport: short direct_indirect instruction")
		}
		return fmt.Sprintf("%s ($%02X)", ensureSuffix(mnemonic, ".b"), raw[1]), nil

	case "direct_indirect_long":
		if len(raw) < 2 {
			return "", fmt.Errorf("asmexport: short direct_indirect_long instruction")
		}
		return fmt.Sprintf("%s [$%02X]", ensureSuffix(mnemonic, ".b"), raw[1]), nil

	case "direct_indirect_long_y":
		if len(raw) < 2 {
			return "", fmt.Errorf("asmexport: short direct_indirect_long_y instruction")
		}
		return fmt.Sprintf("%s [$%02X],y", ensureSuffix(mnemonic, ".b"), raw[1]), nil

	case "indirect_x":
		if len(raw) < 2 {
			return "", fmt.Errorf("asmexport: short indirect_x instruction")
		}
		return fmt.Sprintf("%s ($%02X,x)", ensureSuffix(mnemonic, ".b"), raw[1]), nil

	case "indirect_y":
		if len(raw) < 2 {
			return "", fmt.Errorf("asmexport: short indirect_y instruction")
		}
		return fmt.Sprintf("%s ($%02X),y", ensureSuffix(mnemonic, ".b"), raw[1]), nil

	case "absolute_indirect":
		if len(raw) < 3 {
			return "", fmt.Errorf("asmexport: short absolute_indirect instruction")
		}
		val := binary.LittleEndian.Uint16(raw[1:3])
		return fmt.Sprintf("%s ($%04X)", ensureSuffix(mnemonic, ".w"), val), nil

	case "absolute_indirect_x":
		if len(raw) < 3 {
			return "", fmt.Errorf("asmexport: short absolute_indirect_x instruction")
		}
		val := binary.LittleEndian.Uint16(raw[1:3])
		return fmt.Sprintf("%s ($%04X,x)", ensureSuffix(mnemonic, ".w"), val), nil

	case "absolute_indirect_long":
		if len(raw) < 3 {
			return "", fmt.Errorf("asmexport: short absolute_indirect_long instruction")
		}
		val := binary.LittleEndian.Uint16(raw[1:3])
		return fmt.Sprintf("%s [$%04X]", ensureSuffix(mnemonic, ".w"), val), nil

	case "stack_relative":
		if len(raw) < 2 {
			return "", fmt.Errorf("asmexport: short stack_relative instruction")
		}
		return fmt.Sprintf("%s $%02X,s", mnemonic, raw[1]), nil

	case "stack_relative_y":
		if len(raw) < 2 {
			return "", fmt.Errorf("asmexport: short stack_relative_y instruction")
		}
		return fmt.Sprintf("%s ($%02X,s),y", mnemonic, raw[1]), nil

	case "block_move":
		if len(raw) < 3 {
			return "", fmt.Errorf("asmexport: short block_move instruction")
		}
		return fmt.Sprintf("%s $%02X, $%02X", mnemonic, raw[2], raw[1]), nil

	default:
		return "", fmt.Errorf("asmexport: unrecognized addressing mode %q", inst.Mode)
	}
}
