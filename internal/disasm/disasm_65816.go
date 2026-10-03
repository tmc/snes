package disasm

import (
	"fmt"
	"strings"

	"github.com/tmc/snes/internal/cpu"
)

// PeekBus65816 is the minimal interface needed to read operand bytes from
// the 24-bit 65c816 address space. *bus.Bus satisfies it directly.
//
// Reads may update bus-level side state such as MDR. Callers who require
// zero-side-effect disassembly must wrap the bus.
type PeekBus65816 interface {
	Read(addr uint32) uint8
}

// Disassemble65816 returns a one-line trace for the instruction at the CPU's
// current PB:PC. It does not advance PC or otherwise mutate c.
//
// Format (matching bsnes trace output):
//
//	PB:PC  bb bb bb bb  mnemonic operand                   A:... X:... Y:... S:... D:... DB:... NVMXDIZC
func Disassemble65816(c *cpu.CPU, bus PeekBus65816) string {
	pb := c.PB
	pc := c.PC
	base := uint32(pb)<<16 | uint32(pc)

	// Read up to 4 bytes.
	b0 := bus.Read(base)
	b1 := bus.Read(uint32(pb)<<16 | uint32(pc+1))
	b2 := bus.Read(uint32(pb)<<16 | uint32(pc+2))
	b3 := bus.Read(uint32(pb)<<16 | uint32(pc+3))
	word := uint16(b1) | uint16(b2)<<8
	longv := uint32(word) | uint32(b3)<<16

	mnem, size := formatInstruction65816(c, b0, b1, b2, word, longv, pc)

	// Raw bytes column, padded to 4 bytes (11 chars: "XX XX XX XX").
	raw := rawBytes(size, b0, b1, b2, b3)

	flags := formatFlags65816(c)

	return fmt.Sprintf("%02X:%04X  %s  %s A:%04X X:%04X Y:%04X S:%04X D:%04X DB:%02X %s",
		pb, pc, raw, mnem, c.A, c.X, c.Y, c.S, c.D, c.DB, flags)
}

// InstructionSize65816 returns the number of bytes consumed by the
// instruction at the CPU's current PB:PC under its current m/x/E flags.
// It reads only the opcode byte; operand bytes are not touched.
func InstructionSize65816(c *cpu.CPU, bus PeekBus65816) int {
	base := uint32(c.PB)<<16 | uint32(c.PC)
	opcode := bus.Read(base)
	return instructionSizeFor65816(opcode, mFlag(c), xFlag(c))
}

// InstructionLength65816 returns the encoded length in bytes of the
// instruction with the given opcode. m8 and x8 report 8-bit memory and
// index registers, which are forced in emulation mode. BRK and COP are
// two bytes, including the signature byte.
func InstructionLength65816(opcode uint8, m8, x8 bool) int {
	return instructionSizeFor65816(opcode, m8, x8)
}

func instructionSizeFor65816(opcode uint8, m, x bool) int {
	switch opcodeType65816[opcode] {
	case 0:
		return 1
	case 1:
		return 2
	case 2:
		return 3
	case 3:
		return 4
	case 4:
		if m {
			return 2
		}
		return 3
	case 5:
		if x {
			return 2
		}
		return 3
	case 6:
		return 2
	case 7:
		return 3
	case 8:
		return 3
	}
	return 1
}

// formatInstruction65816 returns the mnemonic+operand string (trimmed of the
// fixed-width trailing padding that the source templates carry) and the
// instruction size in bytes, which respects the m/x/E flags for immediate
// operations.
func formatInstruction65816(c *cpu.CPU, opcode, b1, b2 uint8, word uint16, longv uint32, pc uint16) (string, int) {
	var line string
	size := 1
	switch opcodeType65816[opcode] {
	case 0:
		line = opcodeNames65816[opcode]
		size = 1
	case 1:
		line = fmt.Sprintf(opcodeNames65816[opcode], b1)
		size = 2
	case 2:
		line = fmt.Sprintf(opcodeNames65816[opcode], word)
		size = 3
	case 3:
		line = fmt.Sprintf(opcodeNames65816[opcode], longv)
		size = 4
	case 4:
		// Accumulator / memory immediate: width depends on m flag
		// (and the E flag forces m=1).
		if mFlag(c) {
			line = fmt.Sprintf(opcodeNamesSp65816[opcode], b1)
			size = 2
		} else {
			line = fmt.Sprintf(opcodeNames65816[opcode], word)
			size = 3
		}
	case 5:
		// Index immediate: width depends on x flag (E forces x=1).
		if xFlag(c) {
			line = fmt.Sprintf(opcodeNamesSp65816[opcode], b1)
			size = 2
		} else {
			line = fmt.Sprintf(opcodeNames65816[opcode], word)
			size = 3
		}
	case 6:
		// PC-relative short branch.
		rel := uint16(int32(pc) + 2 + int32(int8(b1)))
		line = fmt.Sprintf(opcodeNames65816[opcode], rel)
		size = 2
	case 7:
		// PC-relative long (BRL, PER).
		rell := uint16(int32(pc) + 3 + int32(int16(word)))
		line = fmt.Sprintf(opcodeNames65816[opcode], rell)
		size = 3
	case 8:
		// Block move (MVN/MVP): two bank bytes, dest first in mnemonic.
		line = fmt.Sprintf(opcodeNames65816[opcode], b2, b1)
		size = 3
	default:
		line = opcodeNames65816[opcode]
	}

	// Prefer the CPU package's size table when it has a definitive answer
	// that matches (or disagrees with) our template-derived size. Keep the
	// immediate-width decision above since the CPU table stores the flag-
	// independent nominal size.
	if op := cpu.Opcodes[opcode]; op.Op != nil && size == 0 {
		size = int(op.Size)
	}

	return strings.TrimRight(line, " "), size
}

func rawBytes(size int, b0, b1, b2, b3 uint8) string {
	switch size {
	case 1:
		return fmt.Sprintf("%02X         ", b0)
	case 2:
		return fmt.Sprintf("%02X %02X      ", b0, b1)
	case 3:
		return fmt.Sprintf("%02X %02X %02X   ", b0, b1, b2)
	case 4:
		return fmt.Sprintf("%02X %02X %02X %02X", b0, b1, b2, b3)
	default:
		return fmt.Sprintf("%02X         ", b0)
	}
}

// mFlag reports whether the Accumulator/Memory is currently 8-bit.
func mFlag(c *cpu.CPU) bool {
	if c.E {
		return true
	}
	return c.P&0x20 != 0
}

// xFlag reports whether the index registers are currently 8-bit.
func xFlag(c *cpu.CPU) bool {
	if c.E {
		return true
	}
	return c.P&0x10 != 0
}

func formatFlags65816(c *cpu.CPU) string {
	// Order: N V M X D I Z C (high bit to low bit of P).
	// Uppercase when set, lowercase when clear. The conventional trace
	// doesn't surface E here; it is implied by M/X always reading as set.
	pairs := []struct {
		bit uint8
		hi  byte
		lo  byte
	}{
		{0x80, 'N', 'n'},
		{0x40, 'V', 'v'},
		{0x20, 'M', 'm'},
		{0x10, 'X', 'x'},
		{0x08, 'D', 'd'},
		{0x04, 'I', 'i'},
		{0x02, 'Z', 'z'},
		{0x01, 'C', 'c'},
	}
	buf := make([]byte, len(pairs))
	for i, p := range pairs {
		if c.P&p.bit != 0 {
			buf[i] = p.hi
		} else {
			buf[i] = p.lo
		}
	}
	return string(buf)
}

// opcodeNames65816 holds the fmt.Sprintf format string for each opcode.
// Source attribution is in NOTICE; printf width/length
// specifiers kept as Go-compatible forms (%02X / %04X / %06X).
var opcodeNames65816 = [256]string{
	"BRK #$%02X     ", "ORA ($%02X,X)  ", "COP #$%02X     ", "ORA $%02X,S    ", "TSB $%02X      ", "ORA $%02X      ", "ASL $%02X      ", "ORA [$%02X]    ", "PHP          ", "ORA #$%04X   ", "ASL          ", "PHD          ", "TSB $%04X    ", "ORA $%04X    ", "ASL $%04X    ", "ORA $%06X  ",
	"BPL $%04X    ", "ORA ($%02X),Y  ", "ORA ($%02X)    ", "ORA ($%02X,S),Y", "TRB $%02X      ", "ORA $%02X,X    ", "ASL $%02X,X    ", "ORA [$%02X],Y  ", "CLC          ", "ORA $%04X,Y  ", "INC          ", "TCS          ", "TRB $%04X    ", "ORA $%04X,X  ", "ASL $%04X,X  ", "ORA $%06X,X",
	"JSR $%04X    ", "AND ($%02X,X)  ", "JSL $%06X  ", "AND $%02X,S    ", "BIT $%02X      ", "AND $%02X      ", "ROL $%02X      ", "AND [$%02X]    ", "PLP          ", "AND #$%04X   ", "ROL          ", "PLD          ", "BIT $%04X    ", "AND $%04X    ", "ROL $%04X    ", "AND $%06X  ",
	"BMI $%04X    ", "AND ($%02X),Y  ", "AND ($%02X)    ", "AND ($%02X,S),Y", "BIT $%02X,X    ", "AND $%02X,X    ", "ROL $%02X,X    ", "AND [$%02X],Y  ", "SEC          ", "AND $%04X,Y  ", "DEC          ", "TSC          ", "BIT $%04X,X  ", "AND $%04X,X  ", "ROL $%04X,X  ", "AND $%06X,X",
	"RTI          ", "EOR ($%02X,X)  ", "WDM #$%02X     ", "EOR $%02X,S    ", "MVP $%02X, $%02X ", "EOR $%02X      ", "LSR $%02X      ", "EOR [$%02X]    ", "PHA          ", "EOR #$%04X   ", "LSR          ", "PHK          ", "JMP $%04X    ", "EOR $%04X    ", "LSR $%04X    ", "EOR $%06X  ",
	"BVC $%04X    ", "EOR ($%02X),Y  ", "EOR ($%02X)    ", "EOR ($%02X,S),Y", "MVN $%02X, $%02X ", "EOR $%02X,X    ", "LSR $%02X,X    ", "EOR [$%02X],Y  ", "CLI          ", "EOR $%04X,Y  ", "PHY          ", "TCD          ", "JML $%06X  ", "EOR $%04X,X  ", "LSR $%04X,X  ", "EOR $%06X,X",
	"RTS          ", "ADC ($%02X,X)  ", "PER $%04X    ", "ADC $%02X,S    ", "STZ $%02X      ", "ADC $%02X      ", "ROR $%02X      ", "ADC [$%02X]    ", "PLA          ", "ADC #$%04X   ", "ROR          ", "RTL          ", "JMP ($%04X)  ", "ADC $%04X    ", "ROR $%04X    ", "ADC $%06X  ",
	"BVS $%04X    ", "ADC ($%02X),Y  ", "ADC ($%02X)    ", "ADC ($%02X,S),Y", "STZ $%02X,X    ", "ADC $%02X,X    ", "ROR $%02X,X    ", "ADC [$%02X],Y  ", "SEI          ", "ADC $%04X,Y  ", "PLY          ", "TDC          ", "JMP ($%04X,X)", "ADC $%04X,X  ", "ROR $%04X,X  ", "ADC $%06X,X",
	"BRA $%04X    ", "STA ($%02X,X)  ", "BRL $%04X    ", "STA $%02X,S    ", "STY $%02X      ", "STA $%02X      ", "STX $%02X      ", "STA [$%02X]    ", "DEY          ", "BIT #$%04X   ", "TXA          ", "PHB          ", "STY $%04X    ", "STA $%04X    ", "STX $%04X    ", "STA $%06X  ",
	"BCC $%04X    ", "STA ($%02X),Y  ", "STA ($%02X)    ", "STA ($%02X,S),Y", "STY $%02X,X    ", "STA $%02X,X    ", "STX $%02X,Y    ", "STA [$%02X],Y  ", "TYA          ", "STA $%04X,Y  ", "TXS          ", "TXY          ", "STZ $%04X    ", "STA $%04X,X  ", "STZ $%04X,X  ", "STA $%06X,X",
	"LDY #$%04X   ", "LDA ($%02X,X)  ", "LDX #$%04X   ", "LDA $%02X,S    ", "LDY $%02X      ", "LDA $%02X      ", "LDX $%02X      ", "LDA [$%02X]    ", "TAY          ", "LDA #$%04X   ", "TAX          ", "PLB          ", "LDY $%04X    ", "LDA $%04X    ", "LDX $%04X    ", "LDA $%06X  ",
	"BCS $%04X    ", "LDA ($%02X),Y  ", "LDA ($%02X)    ", "LDA ($%02X,S),Y", "LDY $%02X,X    ", "LDA $%02X,X    ", "LDX $%02X,Y    ", "LDA [$%02X],Y  ", "CLV          ", "LDA $%04X,Y  ", "TSX          ", "TYX          ", "LDY $%04X,X  ", "LDA $%04X,X  ", "LDX $%04X,Y  ", "LDA $%06X,X",
	"CPY #$%04X   ", "CMP ($%02X,X)  ", "REP #$%02X     ", "CMP $%02X,S    ", "CPY $%02X      ", "CMP $%02X      ", "DEC $%02X      ", "CMP [$%02X]    ", "INY          ", "CMP #$%04X   ", "DEX          ", "WAI          ", "CPY $%04X    ", "CMP $%04X    ", "DEC $%04X    ", "CMP $%06X  ",
	"BNE $%04X    ", "CMP ($%02X),Y  ", "CMP ($%02X)    ", "CMP ($%02X,S),Y", "PEI $%02X      ", "CMP $%02X,X    ", "DEC $%02X,X    ", "CMP [$%02X],Y  ", "CLD          ", "CMP $%04X,Y  ", "PHX          ", "STP          ", "JML [$%04X]  ", "CMP $%04X,X  ", "DEC $%04X,X  ", "CMP $%06X,X",
	"CPX #$%04X   ", "SBC ($%02X,X)  ", "SEP #$%02X     ", "SBC $%02X,S    ", "CPX $%02X      ", "SBC $%02X      ", "INC $%02X      ", "SBC [$%02X]    ", "INX          ", "SBC #$%04X   ", "NOP          ", "XBA          ", "CPX $%04X    ", "SBC $%04X    ", "INC $%04X    ", "SBC $%06X  ",
	"BEQ $%04X    ", "SBC ($%02X),Y  ", "SBC ($%02X)    ", "SBC ($%02X,S),Y", "PEA #$%04X   ", "SBC $%02X,X    ", "INC $%02X,X    ", "SBC [$%02X],Y  ", "SED          ", "SBC $%04X,Y  ", "PLX          ", "XCE          ", "JSR ($%04X,X)", "SBC $%04X,X  ", "INC $%04X,X  ", "SBC $%06X,X",
}

// opcodeNamesSp65816 is the 8-bit-immediate variant used when m (type 4) or
// x (type 5) flags force a narrow immediate. Entries are empty string for
// opcodes that are not flag-sensitive immediates.
var opcodeNamesSp65816 = [256]string{
	// 0x00-0x0F
	"", "", "", "", "", "", "", "", "", "ORA #$%02X     ", "", "", "", "", "", "",
	// 0x10-0x1F
	"", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "",
	// 0x20-0x2F
	"", "", "", "", "", "", "", "", "", "AND #$%02X     ", "", "", "", "", "", "",
	// 0x30-0x3F
	"", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "",
	// 0x40-0x4F
	"", "", "", "", "", "", "", "", "", "EOR #$%02X     ", "", "", "", "", "", "",
	// 0x50-0x5F
	"", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "",
	// 0x60-0x6F
	"", "", "", "", "", "", "", "", "", "ADC #$%02X     ", "", "", "", "", "", "",
	// 0x70-0x7F
	"", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "",
	// 0x80-0x8F
	"", "", "", "", "", "", "", "", "", "BIT #$%02X     ", "", "", "", "", "", "",
	// 0x90-0x9F
	"", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "",
	// 0xA0-0xAF
	"LDY #$%02X     ", "", "LDX #$%02X     ", "", "", "", "", "", "", "LDA #$%02X     ", "", "", "", "", "", "",
	// 0xB0-0xBF
	"", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "",
	// 0xC0-0xCF
	"CPY #$%02X     ", "", "", "", "", "", "", "", "", "CMP #$%02X     ", "", "", "", "", "", "",
	// 0xD0-0xDF
	"", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "",
	// 0xE0-0xEF
	"CPX #$%02X     ", "", "", "", "", "", "", "", "", "SBC #$%02X     ", "", "", "", "", "", "",
	// 0xF0-0xFF
	"", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "",
}

// opcodeType65816 classifies each opcode for operand decoding:
//
//	0 implied/accumulator (no operand)
//	1 one-byte operand
//	2 two-byte (word) operand
//	3 three-byte (long) operand
//	4 accumulator-immediate: byte when m=1, word when m=0
//	5 index-immediate: byte when x=1, word when x=0
//	6 relative (signed 8-bit, displayed as absolute PC)
//	7 long relative (signed 16-bit)
//	8 block move (two bank bytes)
var opcodeType65816 = [256]int{
	1, 1, 1, 1, 1, 1, 1, 1, 0, 4, 0, 0, 2, 2, 2, 3,
	6, 1, 1, 1, 1, 1, 1, 1, 0, 2, 0, 0, 2, 2, 2, 3,
	2, 1, 3, 1, 1, 1, 1, 1, 0, 4, 0, 0, 2, 2, 2, 3,
	6, 1, 1, 1, 1, 1, 1, 1, 0, 2, 0, 0, 2, 2, 2, 3,
	0, 1, 1, 1, 8, 1, 1, 1, 0, 4, 0, 0, 2, 2, 2, 3,
	6, 1, 1, 1, 8, 1, 1, 1, 0, 2, 0, 0, 3, 2, 2, 3,
	0, 1, 7, 1, 1, 1, 1, 1, 0, 4, 0, 0, 2, 2, 2, 3,
	6, 1, 1, 1, 1, 1, 1, 1, 0, 2, 0, 0, 2, 2, 2, 3,
	6, 1, 7, 1, 1, 1, 1, 1, 0, 4, 0, 0, 2, 2, 2, 3,
	6, 1, 1, 1, 1, 1, 1, 1, 0, 2, 0, 0, 2, 2, 2, 3,
	5, 1, 5, 1, 1, 1, 1, 1, 0, 4, 0, 0, 2, 2, 2, 3,
	6, 1, 1, 1, 1, 1, 1, 1, 0, 2, 0, 0, 2, 2, 2, 3,
	5, 1, 1, 1, 1, 1, 1, 1, 0, 4, 0, 0, 2, 2, 2, 3,
	6, 1, 1, 1, 1, 1, 1, 1, 0, 2, 0, 0, 2, 2, 2, 3,
	5, 1, 1, 1, 1, 1, 1, 1, 0, 4, 0, 0, 2, 2, 2, 3,
	6, 1, 1, 1, 2, 1, 1, 1, 0, 2, 0, 0, 2, 2, 2, 3,
}
