package disasm

import (
	"fmt"
	"strings"

	"github.com/tmc/snes/internal/apu/spc700"
)

// PeekBusSPC700 is the minimal interface needed to read operand bytes from
// the SPC700's 16-bit address space. The APU's MainBus satisfies it directly.
type PeekBusSPC700 interface {
	Read(addr uint16) uint8
}

// DisassembleSPC700 returns a one-line trace for the instruction at the
// SPC700's current PC. It does not advance PC or mutate s.
//
// Format (matching bsnes / snes9x SPC trace output):
//
//	PC  mnemonic operand            A:AA X:XX Y:YY SP:SS NVPHIZC
func DisassembleSPC700(s *spc700.SPC700, bus PeekBusSPC700) string {
	pc := s.PC
	b0 := bus.Read(pc)
	b1 := bus.Read(pc + 1)
	b2 := bus.Read(pc + 2)
	word := uint16(b1) | uint16(b2)<<8

	mnem := formatInstructionSPC700(b0, b1, b2, word, pc)
	flags := formatFlagsSPC700(s)

	return fmt.Sprintf("%04X %s A:%02X X:%02X Y:%02X SP:%02X %s",
		pc, mnem, s.A, s.X, s.Y, s.SP, flags)
}

// InstructionSizeSPC700 returns the number of bytes consumed by the
// instruction at the SPC700's current PC. Only the opcode byte is read.
func InstructionSizeSPC700(s *spc700.SPC700, bus PeekBusSPC700) int {
	return instructionSizeForSPC700(bus.Read(s.PC))
}

func instructionSizeForSPC700(opcode uint8) int {
	switch opcodeTypeSPC700[opcode] {
	case 0:
		return 1
	case 1, 3:
		return 2
	case 2, 4, 5, 6:
		return 3
	}
	return 1
}

func formatInstructionSPC700(opcode, b1, b2 uint8, word uint16, pc uint16) string {
	rel := uint16(int32(pc) + 2 + int32(int8(b1)))
	rel2 := uint16(int32(pc) + 2 + int32(int8(b2)))
	// For bit-addressed abs.bit operands the immediate word packs the
	// 3-bit bit index into the top bits and the 13-bit address into the low.
	wordb := word & 0x1FFF
	bit := word >> 13

	var line string
	switch opcodeTypeSPC700[opcode] {
	case 0:
		line = opcodeNamesSPC700[opcode]
	case 1:
		line = fmt.Sprintf(opcodeNamesSPC700[opcode], b1)
	case 2:
		line = fmt.Sprintf(opcodeNamesSPC700[opcode], word)
	case 3:
		line = fmt.Sprintf(opcodeNamesSPC700[opcode], rel)
	case 4:
		// dd, ss (destination direct, source direct) — byte2 first in
		// mnemonic source attribution is in NOTICE.
		line = fmt.Sprintf(opcodeNamesSPC700[opcode], b2, b1)
	case 5:
		// dp, rel — direct page byte then relative branch.
		line = fmt.Sprintf(opcodeNamesSPC700[opcode], b1, rel2)
	case 6:
		line = fmt.Sprintf(opcodeNamesSPC700[opcode], wordb, bit)
	default:
		line = opcodeNamesSPC700[opcode]
	}
	return strings.TrimRight(line, " ")
}

// formatFlagsSPC700 emits 7 characters: N V P H I Z C (uppercase when set).
// The B (break) flag is intentionally omitted to match the trace shape used
// in this project's reference tooling.
func formatFlagsSPC700(s *spc700.SPC700) string {
	flag := func(set bool, hi, lo byte) byte {
		if set {
			return hi
		}
		return lo
	}
	return string([]byte{
		flag(s.N, 'N', 'n'),
		flag(s.V, 'V', 'v'),
		flag(s.P, 'P', 'p'),
		flag(s.H, 'H', 'h'),
		flag(s.I, 'I', 'i'),
		flag(s.Z, 'Z', 'z'),
		flag(s.C, 'C', 'c'),
	})
}

// opcodeNamesSPC700 is the fmt.Sprintf template for each SPC700 opcode.
// Source attribution is in NOTICE.
var opcodeNamesSPC700 = [256]string{
	"NOP              ", "TCALL 0          ", "SET1 $%02X.0       ", "BBS $%02X.0, $%04X ", "OR A, $%02X        ", "OR A, $%04X      ", "OR A, [X]        ", "OR A, [$%02X+X]    ", "OR A, #$%02X       ", "OR $%02X, $%02X      ", "OR1 C, $%04X.%01X   ", "ASL $%02X          ", "ASL $%04X        ", "PUSH P           ", "TSET $%04X       ", "BRK              ",
	"BPL $%04X        ", "TCALL 1          ", "CLR1 $%02X.0       ", "BBC $%02X.0, $%04X ", "OR A, $%02X+X      ", "OR A, $%04X+X    ", "OR A, $%04X+Y    ", "OR A, [$%02X]+Y    ", "OR $%02X, #$%02X     ", "OR [X], [Y]      ", "DECW $%02X         ", "ASL $%02X+X        ", "ASL A            ", "DEC X            ", "CMP X, $%04X     ", "JMP [$%04X+X]    ",
	"CLRP             ", "TCALL 2          ", "SET1 $%02X.1       ", "BBS $%02X.1, $%04X ", "AND A, $%02X       ", "AND A, $%04X     ", "AND A, [X]       ", "AND A, [$%02X+X]   ", "AND A, #$%02X      ", "AND $%02X, $%02X     ", "OR1 C, /$%04X.%01X  ", "ROL $%02X          ", "ROL $%04X        ", "PUSH A           ", "CBNE $%02X, $%04X  ", "BRA $%04X        ",
	"BMI $%04X        ", "TCALL 3          ", "CLR1 $%02X.1       ", "BBC $%02X.1, $%04X ", "AND A, $%02X+X     ", "AND A, $%04X+X   ", "AND A, $%04X+Y   ", "AND A, [$%02X]+Y   ", "AND $%02X, #$%02X    ", "AND [X], [Y]     ", "INCW $%02X         ", "ROL $%02X+X        ", "ROL A            ", "INC X            ", "CMP X, $%02X       ", "CALL $%04X       ",
	"SETP             ", "TCALL 4          ", "SET1 $%02X.2       ", "BBS $%02X.2, $%04X ", "EOR A, $%02X       ", "EOR A, $%04X     ", "EOR A, [X]       ", "EOR A, [$%02X+X]   ", "EOR A, #$%02X      ", "EOR $%02X, $%02X     ", "AND1 C, $%04X.%01X  ", "LSR $%02X          ", "LSR $%04X        ", "PUSH X           ", "TCLR $%04X       ", "PCALL $%02X        ",
	"BVC $%04X        ", "TCALL 5          ", "CLR1 $%02X.2       ", "BBC $%02X.2, $%04X ", "EOR A, $%02X+X     ", "EOR A, $%04X+X   ", "EOR A, $%04X+Y   ", "EOR A, [$%02X]+Y   ", "EOR $%02X, #$%02X    ", "EOR [X], [Y]     ", "CMPW YA, $%02X     ", "LSR $%02X+X        ", "LSR A            ", "MOV X, A         ", "CMP Y, $%04X     ", "JMP $%04X        ",
	"CLRC             ", "TCALL 6          ", "SET1 $%02X.3       ", "BBS $%02X.3, $%04X ", "CMP A, $%02X       ", "CMP A, $%04X     ", "CMP A, [X]       ", "CMP A, [$%02X+X]   ", "CMP A, #$%02X      ", "CMP $%02X, $%02X     ", "AND1 C, /$%04X.%01X ", "ROR $%02X          ", "ROR $%04X        ", "PUSH Y           ", "DBNZ $%02X, $%04X  ", "RET              ",
	"BVS $%04X        ", "TCALL 7          ", "CLR1 $%02X.3       ", "BBC $%02X.3, $%04X ", "CMP A, $%02X+X     ", "CMP A, $%04X+X   ", "CMP A, $%04X+Y   ", "CMP A, [$%02X]+Y   ", "CMP $%02X, #$%02X    ", "CMP [X], [Y]     ", "ADDW YA, $%02X     ", "ROR $%02X+X        ", "ROR A            ", "MOV A, X         ", "CMP Y, $%02X       ", "RETI             ",
	"SETC             ", "TCALL 8          ", "SET1 $%02X.4       ", "BBS $%02X.4, $%04X ", "ADC A, $%02X       ", "ADC A, $%04X     ", "ADC A, [X]       ", "ADC A, [$%02X+X]   ", "ADC A, #$%02X      ", "ADC $%02X, $%02X     ", "EOR1 C, $%04X.%01X  ", "DEC $%02X          ", "DEC $%04X        ", "MOV Y, #$%02X      ", "POP P            ", "MOV $%02X, #$%02X    ",
	"BCC $%04X        ", "TCALL 9          ", "CLR1 $%02X.4       ", "BBC $%02X.4, $%04X ", "ADC A, $%02X+X     ", "ADC A, $%04X+X   ", "ADC A, $%04X+Y   ", "ADC A, [$%02X]+Y   ", "ADC $%02X, #$%02X    ", "ADC [X], [Y]     ", "SUBW YA, $%02X     ", "DEC $%02X+X        ", "DEC A            ", "MOV X, SP        ", "DIV YA, X        ", "XCN A            ",
	"EI               ", "TCALL 10         ", "SET1 $%02X.5       ", "BBS $%02X.5, $%04X ", "SBC A, $%02X       ", "SBC A, $%04X     ", "SBC A, [X]       ", "SBC A, [$%02X+X]   ", "SBC A, #$%02X      ", "SBC $%02X, $%02X     ", "MOV1 C, $%04X.%01X  ", "INC $%02X          ", "INC $%04X        ", "CMP Y, #$%02X      ", "POP A            ", "MOV [X+], A      ",
	"BCS $%04X        ", "TCALL 11         ", "CLR1 $%02X.5       ", "BBC $%02X.5, $%04X ", "SBC A, $%02X+X     ", "SBC A, $%04X+X   ", "SBC A, $%04X+Y   ", "SBC A, [$%02X]+Y   ", "SBC $%02X, #$%02X    ", "SBC [X], [Y]     ", "MOVW YA, $%02X     ", "INC $%02X+X        ", "INC A            ", "MOV SP, X        ", "DAS A            ", "MOV A, [X+]      ",
	"DI               ", "TCALL 12         ", "SET1 $%02X.6       ", "BBS $%02X.6, $%04X ", "MOV $%02X, A       ", "MOV $%04X, A     ", "MOV [X], A       ", "MOV [$%02X+X], A   ", "CMP X, #$%02X      ", "MOV $%04X, X     ", "MOV1 $%04X.%01X, C  ", "MOV $%02X, Y       ", "MOV $%04X, Y     ", "MOV X, #$%02X      ", "POP X            ", "MUL YA           ",
	"BNE $%04X        ", "TCALL 13         ", "CLR1 $%02X.6       ", "BBC $%02X.6, $%04X ", "MOV $%02X+X, A     ", "MOV $%04X+X, A   ", "MOV $%04X+Y, A   ", "MOV [$%02X]+Y, A   ", "MOV $%02X, X       ", "MOV $%02X+Y, X     ", "MOVW $%02X, YA     ", "MOV $%02X+X, Y     ", "DEC Y            ", "MOV A, Y         ", "CBNE $%02X+X, $%04X", "DAA A            ",
	"CLRV             ", "TCALL 14         ", "SET1 $%02X.7       ", "BBS $%02X.7, $%04X ", "MOV A, $%02X       ", "MOV A, $%04X     ", "MOV A, [X]       ", "MOV A, [$%02X+X]   ", "MOV A, #$%02X      ", "MOV X, $%04X     ", "NOT1 $%04X.%01X     ", "MOV Y, $%02X       ", "MOV Y, $%04X     ", "NOTC             ", "POP Y            ", "SLEEP            ",
	"BEQ $%04X        ", "TCALL 15         ", "CLR1 $%02X.7       ", "BBC $%02X.7, $%04X ", "MOV A, $%02X+X     ", "MOV A, $%04X+X   ", "MOV A, $%04X+Y   ", "MOV A, [$%02X]+Y   ", "MOV X, $%02X       ", "MOV X, $%02X+Y     ", "MOV $%02X, $%02X     ", "MOV Y, $%02X+X     ", "INC Y            ", "MOV Y, A         ", "DBNZ Y, $%04X    ", "STOP             ",
}

// opcodeTypeSPC700 classifies operand encodings; source attribution is in NOTICE.
//
//	0 implied
//	1 one-byte direct-page operand
//	2 two-byte absolute operand
//	3 relative branch
//	4 dest-dd, src-dd (two direct-page bytes; word = src|dst<<8)
//	5 dp, rel (direct-page + relative branch, e.g. CBNE/DBNZ)
//	6 abs13.bit (13-bit address + 3-bit bit number packed into a word)
var opcodeTypeSPC700 = [256]int{
	0, 0, 1, 5, 1, 2, 0, 1, 1, 4, 6, 1, 2, 0, 2, 0,
	3, 0, 1, 5, 1, 2, 2, 1, 4, 0, 1, 1, 0, 0, 2, 2,
	0, 0, 1, 5, 1, 2, 0, 1, 1, 4, 6, 1, 2, 0, 5, 3,
	3, 0, 1, 5, 1, 2, 2, 1, 4, 0, 1, 1, 0, 0, 1, 2,
	0, 0, 1, 5, 1, 2, 0, 1, 1, 4, 6, 1, 2, 0, 2, 1,
	3, 0, 1, 5, 1, 2, 2, 1, 4, 0, 1, 1, 0, 0, 2, 2,
	0, 0, 1, 5, 1, 2, 0, 1, 1, 4, 6, 1, 2, 0, 5, 0,
	3, 0, 1, 5, 1, 2, 2, 1, 4, 0, 1, 1, 0, 0, 1, 0,
	0, 0, 1, 5, 1, 2, 0, 1, 1, 4, 6, 1, 2, 1, 0, 4,
	3, 0, 1, 5, 1, 2, 2, 1, 4, 0, 1, 1, 0, 0, 0, 0,
	0, 0, 1, 5, 1, 2, 0, 1, 1, 4, 6, 1, 2, 1, 0, 0,
	3, 0, 1, 5, 1, 2, 2, 1, 4, 0, 1, 1, 0, 0, 0, 0,
	0, 0, 1, 5, 1, 2, 0, 1, 1, 2, 6, 1, 2, 1, 0, 0,
	3, 0, 1, 5, 1, 2, 2, 1, 1, 1, 1, 1, 0, 0, 5, 0,
	0, 0, 1, 5, 1, 2, 0, 1, 1, 2, 6, 1, 2, 0, 0, 0,
	3, 0, 1, 5, 1, 2, 2, 1, 1, 1, 4, 1, 0, 0, 3, 0,
}
