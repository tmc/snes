package structure

import (
	"encoding/hex"
	"strings"

	"github.com/tmc/snes/internal/recovery"
)

// ExtractMemoryReferences inspects instructions and returns memory accesses and hardware register references.
func ExtractMemoryReferences(doc *recovery.Document) []MemoryReference {
	if doc == nil || len(doc.Instructions) == 0 {
		return nil
	}

	var refs []MemoryReference
	for _, inst := range doc.Instructions {
		ref, ok := parseInstructionMemoryReference(inst)
		if ok {
			refs = append(refs, ref)
		}
	}
	return refs
}

func parseInstructionMemoryReference(inst recovery.Instruction) (MemoryReference, bool) {
	rawBytes, err := hex.DecodeString(inst.Bytes)
	if err != nil || len(rawBytes) < 2 {
		return MemoryReference{}, false
	}

	var encodedAddr uint32
	var addrSpace string
	hasRef := false

	bank := inst.Address & 0xFF0000

	switch inst.Mode {
	case "absolute", "absolute_x", "absolute_y", "absolute_indirect", "absolute_indirect_x":
		if len(rawBytes) >= 3 {
			encodedAddr = bank | uint32(rawBytes[1]) | (uint32(rawBytes[2]) << 8)
			addrSpace = ClassifyAddress(encodedAddr)
			hasRef = true
		}
	case "long", "long_x", "absolute_indirect_long", "direct_indirect_long":
		if len(rawBytes) >= 4 {
			encodedAddr = uint32(rawBytes[1]) | (uint32(rawBytes[2]) << 8) | (uint32(rawBytes[3]) << 16)
			addrSpace = ClassifyAddress(encodedAddr)
			hasRef = true
		}
	case "direct_page", "direct_page_x", "direct_page_y", "direct_indirect":
		encodedAddr = uint32(rawBytes[1])
		addrSpace = "direct_page"
		hasRef = true
	}

	if !hasRef {
		return MemoryReference{}, false
	}

	dir := classifyDirection(inst.Opcode, inst.Mnemonic)
	hwName := HardwareRegister(encodedAddr)
	if hwName != "" {
		addrSpace = "hardware_register"
	}

	return MemoryReference{
		InstructionID:      inst.ID,
		InstructionAddress: inst.Address,
		Offset:             inst.Offset,
		EncodedAddress:     encodedAddr,
		AddressSpace:       addrSpace,
		HardwareName:       hwName,
		Direction:          dir,
		AddressingMode:     inst.Mode,
		Mnemonic:           inst.Mnemonic,
	}, true
}

func classifyDirection(opcode byte, mnemonic string) string {
	m := strings.ToLower(mnemonic)
	if strings.HasPrefix(m, "st") { // STA, STZ, STX, STY
		return "write"
	}
	if strings.HasPrefix(m, "ld") || strings.HasPrefix(m, "bit") || strings.HasPrefix(m, "cmp") ||
		strings.HasPrefix(m, "cpx") || strings.HasPrefix(m, "cpy") {
		return "read"
	}
	if strings.HasPrefix(m, "inc") || strings.HasPrefix(m, "dec") || strings.HasPrefix(m, "asl") ||
		strings.HasPrefix(m, "lsr") || strings.HasPrefix(m, "rol") || strings.HasPrefix(m, "ror") ||
		strings.HasPrefix(m, "trb") || strings.HasPrefix(m, "tsb") {
		return "read_write"
	}
	if strings.HasPrefix(m, "jmp") || strings.HasPrefix(m, "jsr") || strings.HasPrefix(m, "jsl") {
		return "execute"
	}
	return "read"
}
