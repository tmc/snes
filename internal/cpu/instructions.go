package cpu

func init() {
	// 0x00 BRK
	Opcodes[0x00] = Opcode{Name: "BRK", Op: opBRK, Mode: AddrImm, Cycles: 7, Size: 2} // Mode Stack/Imm? Size 2.

	// 0x02 COP
	Opcodes[0x02] = Opcode{Name: "COP", Op: opCOP, Mode: AddrImm, Cycles: 7, Size: 2}

	// 0xEA NOP

	// 0xEA NOP
	Opcodes[0xEA] = Opcode{Name: "NOP", Op: opNOP, Mode: AddrImpl, Cycles: 2, Size: 1}

	// 0xFB XCE (Exchange Carry with Emulation)
	Opcodes[0xFB] = Opcode{Name: "XCE", Op: opXCE, Mode: AddrImpl, Cycles: 2, Size: 1}

	// 0x18 CLC (Clear Carry)
	Opcodes[0x18] = Opcode{Name: "CLC", Op: opCLC, Mode: AddrImpl, Cycles: 2, Size: 1}

	// 0x38 SEC (Set Carry)
	Opcodes[0x38] = Opcode{Name: "SEC", Op: opSEC, Mode: AddrImpl, Cycles: 2, Size: 1}

	// 0xC2 REP (Reset Status Bits)
	Opcodes[0xC2] = Opcode{Name: "REP", Op: opREP, Mode: AddrImm, Cycles: 3, Size: 2}

	// 0xE2 SEP (Set Status Bits)
	Opcodes[0xE2] = Opcode{Name: "SEP", Op: opSEP, Mode: AddrImm, Cycles: 3, Size: 2}

	// 0xEB XBA (Exchange B and A) (Accumulator High/Low swap)
	Opcodes[0xEB] = Opcode{Name: "XBA", Op: opXBA, Mode: AddrImpl, Cycles: 3, Size: 1}

	// 0x54 MVN (Block Move Negative)
	// Cycles: 7 per byte.
	Opcodes[0x54] = Opcode{Name: "MVN", Op: opMVN, Mode: AddrBlock, Cycles: 7, Size: 3} // Size 3: Op, DestBank, SrcBank

	// 0x44 MVP (Block Move Positive)
	Opcodes[0x44] = Opcode{Name: "MVP", Op: opMVP, Mode: AddrBlock, Cycles: 7, Size: 3}

	// 0x40 RTI (Return from Interrupt)
	Opcodes[0x40] = Opcode{Name: "RTI", Op: opRTI, Mode: AddrImpl, Cycles: 6, Size: 1}

	// Flag Instructions
	Opcodes[0x78] = Opcode{Name: "SEI", Op: opSEI, Mode: AddrImpl, Cycles: 2, Size: 1}
	Opcodes[0x58] = Opcode{Name: "CLI", Op: opCLI, Mode: AddrImpl, Cycles: 2, Size: 1}
	Opcodes[0xD8] = Opcode{Name: "CLD", Op: opCLD, Mode: AddrImpl, Cycles: 2, Size: 1}
	Opcodes[0xF8] = Opcode{Name: "SED", Op: opSED, Mode: AddrImpl, Cycles: 2, Size: 1}

	// LDA
	Opcodes[0xA9] = Opcode{Name: "LDA", Op: opLDA, Mode: AddrImm, Cycles: 2, Size: 2}
	Opcodes[0xA5] = Opcode{Name: "LDA", Op: opLDA, Mode: AddrDir, Cycles: 3, Size: 2}
	Opcodes[0xA1] = Opcode{Name: "LDA", Op: opLDA, Mode: AddrIndX, Cycles: 6, Size: 2}
	Opcodes[0xA3] = Opcode{Name: "LDA", Op: opLDA, Mode: AddrSr, Cycles: 4, Size: 2}
	Opcodes[0xAD] = Opcode{Name: "LDA", Op: opLDA, Mode: AddrAbs, Cycles: 4, Size: 3}
	Opcodes[0xAF] = Opcode{Name: "LDA", Op: opLDA, Mode: AddrLong, Cycles: 5, Size: 4}
	Opcodes[0xB1] = Opcode{Name: "LDA", Op: opLDA, Mode: AddrIndY, Cycles: 5, Size: 2}
	Opcodes[0xB2] = Opcode{Name: "LDA", Op: opLDA, Mode: AddrDirInd, Cycles: 5, Size: 2}
	Opcodes[0xA7] = Opcode{Name: "LDA", Op: opLDA, Mode: AddrDirIndL, Cycles: 6, Size: 2} // [d]
	Opcodes[0xB3] = Opcode{Name: "LDA", Op: opLDA, Mode: AddrSrIndY, Cycles: 7, Size: 2}
	Opcodes[0xB5] = Opcode{Name: "LDA", Op: opLDA, Mode: AddrDirX, Cycles: 4, Size: 2}
	Opcodes[0xB7] = Opcode{Name: "LDA", Op: opLDA, Mode: AddrDirIndLIdxY, Cycles: 6, Size: 2} // [d],y
	Opcodes[0xB9] = Opcode{Name: "LDA", Op: opLDA, Mode: AddrAbsY, Cycles: 4, Size: 3}
	Opcodes[0xBD] = Opcode{Name: "LDA", Op: opLDA, Mode: AddrAbsX, Cycles: 4, Size: 3}
	Opcodes[0xBF] = Opcode{Name: "LDA", Op: opLDA, Mode: AddrLongX, Cycles: 5, Size: 4}

	// STX
	Opcodes[0xA2] = Opcode{Name: "LDX", Op: opLDX, Mode: AddrImm, Cycles: 2, Size: 2}
	Opcodes[0xA6] = Opcode{Name: "LDX", Op: opLDX, Mode: AddrDir, Cycles: 3, Size: 2}
	Opcodes[0xAE] = Opcode{Name: "LDX", Op: opLDX, Mode: AddrAbs, Cycles: 4, Size: 3}
	Opcodes[0xB6] = Opcode{Name: "LDX", Op: opLDX, Mode: AddrDirY, Cycles: 4, Size: 2}
	Opcodes[0xBE] = Opcode{Name: "LDX", Op: opLDX, Mode: AddrAbsY, Cycles: 4, Size: 3}

	// LDY
	Opcodes[0xA0] = Opcode{Name: "LDY", Op: opLDY, Mode: AddrImm, Cycles: 2, Size: 2}
	Opcodes[0xA4] = Opcode{Name: "LDY", Op: opLDY, Mode: AddrDir, Cycles: 3, Size: 2}
	Opcodes[0xAC] = Opcode{Name: "LDY", Op: opLDY, Mode: AddrAbs, Cycles: 4, Size: 3}
	Opcodes[0xB4] = Opcode{Name: "LDY", Op: opLDY, Mode: AddrDirX, Cycles: 4, Size: 2}
	Opcodes[0xBC] = Opcode{Name: "LDY", Op: opLDY, Mode: AddrAbsX, Cycles: 4, Size: 3}

	// Transfer Instructions
	Opcodes[0xAA] = Opcode{Name: "TAX", Op: opTAX, Mode: AddrImpl, Cycles: 2, Size: 1}
	Opcodes[0xA8] = Opcode{Name: "TAY", Op: opTAY, Mode: AddrImpl, Cycles: 2, Size: 1}
	Opcodes[0xBA] = Opcode{Name: "TSX", Op: opTSX, Mode: AddrImpl, Cycles: 2, Size: 1}
	Opcodes[0x8A] = Opcode{Name: "TXA", Op: opTXA, Mode: AddrImpl, Cycles: 2, Size: 1}
	Opcodes[0x9A] = Opcode{Name: "TXS", Op: opTXS, Mode: AddrImpl, Cycles: 2, Size: 1}
	Opcodes[0x98] = Opcode{Name: "TYA", Op: opTYA, Mode: AddrImpl, Cycles: 2, Size: 1}
	Opcodes[0x9B] = Opcode{Name: "TXY", Op: opTXY, Mode: AddrImpl, Cycles: 2, Size: 1}
	Opcodes[0xBB] = Opcode{Name: "TYX", Op: opTYX, Mode: AddrImpl, Cycles: 2, Size: 1}
	Opcodes[0x5B] = Opcode{Name: "TCD", Op: opTCD, Mode: AddrImpl, Cycles: 2, Size: 1}
	Opcodes[0x7B] = Opcode{Name: "TDC", Op: opTDC, Mode: AddrImpl, Cycles: 2, Size: 1}
	Opcodes[0x1B] = Opcode{Name: "TCS", Op: opTCS, Mode: AddrImpl, Cycles: 2, Size: 1}
	Opcodes[0x3B] = Opcode{Name: "TSC", Op: opTSC, Mode: AddrImpl, Cycles: 2, Size: 1}

	// Stack Instructions
	Opcodes[0x4B] = Opcode{Name: "PHK", Op: opPHK, Mode: AddrImpl, Cycles: 3, Size: 1}
	Opcodes[0x08] = Opcode{Name: "PHP", Op: opPHP, Mode: AddrImpl, Cycles: 3, Size: 1}
	Opcodes[0x48] = Opcode{Name: "PHA", Op: opPHA, Mode: AddrImpl, Cycles: 3, Size: 1}
	Opcodes[0xDA] = Opcode{Name: "PHX", Op: opPHX, Mode: AddrImpl, Cycles: 3, Size: 1}
	Opcodes[0x5A] = Opcode{Name: "PHY", Op: opPHY, Mode: AddrImpl, Cycles: 3, Size: 1}
	Opcodes[0x8B] = Opcode{Name: "PHB", Op: opPHB, Mode: AddrImpl, Cycles: 3, Size: 1}
	Opcodes[0x0B] = Opcode{Name: "PHD", Op: opPHD, Mode: AddrImpl, Cycles: 4, Size: 1}

	Opcodes[0x28] = Opcode{Name: "PLP", Op: opPLP, Mode: AddrImpl, Cycles: 4, Size: 1}
	Opcodes[0x2B] = Opcode{Name: "PLD", Op: opPLD, Mode: AddrImpl, Cycles: 5, Size: 1}
	Opcodes[0x68] = Opcode{Name: "PLA", Op: opPLA, Mode: AddrImpl, Cycles: 4, Size: 1}
	Opcodes[0xFA] = Opcode{Name: "PLX", Op: opPLX, Mode: AddrImpl, Cycles: 4, Size: 1}
	Opcodes[0x7A] = Opcode{Name: "PLY", Op: opPLY, Mode: AddrImpl, Cycles: 4, Size: 1}
	Opcodes[0xAB] = Opcode{Name: "PLB", Op: opPLB, Mode: AddrImpl, Cycles: 4, Size: 1}

	// Jump Instructions
	Opcodes[0x4C] = Opcode{Name: "JMP", Op: opJMP_Abs, Mode: AddrAbs, Cycles: 3, Size: 3}
	Opcodes[0x5C] = Opcode{Name: "JML", Op: opJML_Abs, Mode: AddrLong, Cycles: 4, Size: 4}
	Opcodes[0x6C] = Opcode{Name: "JMP", Op: opJMP_Ind, Mode: AddrAbsInd, Cycles: 5, Size: 3}
	Opcodes[0x7C] = Opcode{Name: "JMP", Op: opJMP_IndX, Mode: AddrAbsIndX, Cycles: 6, Size: 3}
	Opcodes[0xDC] = Opcode{Name: "JML", Op: opJML_Ind, Mode: AddrAbsIndLong, Cycles: 6, Size: 3} // Indirect Long

	Opcodes[0x20] = Opcode{Name: "JSR", Op: opJSR, Mode: AddrAbs, Cycles: 6, Size: 3}
	Opcodes[0xFC] = Opcode{Name: "JSR", Op: opJSR_IndX, Mode: AddrAbsIndX, Cycles: 8, Size: 3}
	Opcodes[0x22] = Opcode{Name: "JSL", Op: opJSL, Mode: AddrLong, Cycles: 8, Size: 4}

	Opcodes[0x60] = Opcode{Name: "RTS", Op: opRTS, Mode: AddrImpl, Cycles: 6, Size: 1}
	Opcodes[0x6B] = Opcode{Name: "RTL", Op: opRTL, Mode: AddrImpl, Cycles: 6, Size: 1}

	// Store Instructions
	// STA
	Opcodes[0x85] = Opcode{Name: "STA", Op: opSTA, Mode: AddrDir, Cycles: 3, Size: 2}
	Opcodes[0x81] = Opcode{Name: "STA", Op: opSTA, Mode: AddrIndX, Cycles: 6, Size: 2}
	Opcodes[0x83] = Opcode{Name: "STA", Op: opSTA, Mode: AddrSr, Cycles: 4, Size: 2}
	Opcodes[0x8D] = Opcode{Name: "STA", Op: opSTA, Mode: AddrAbs, Cycles: 4, Size: 3}
	Opcodes[0x8F] = Opcode{Name: "STA", Op: opSTA, Mode: AddrLong, Cycles: 5, Size: 4}
	Opcodes[0x91] = Opcode{Name: "STA", Op: opSTA, Mode: AddrIndY, Cycles: 6, Size: 2}
	Opcodes[0x92] = Opcode{Name: "STA", Op: opSTA, Mode: AddrDirInd, Cycles: 5, Size: 2}
	Opcodes[0x87] = Opcode{Name: "STA", Op: opSTA, Mode: AddrDirIndL, Cycles: 6, Size: 2} // [d]
	Opcodes[0x93] = Opcode{Name: "STA", Op: opSTA, Mode: AddrSrIndY, Cycles: 7, Size: 2}
	Opcodes[0x95] = Opcode{Name: "STA", Op: opSTA, Mode: AddrDirX, Cycles: 4, Size: 2}
	Opcodes[0x97] = Opcode{Name: "STA", Op: opSTA, Mode: AddrDirIndLIdxY, Cycles: 6, Size: 2} // [d],y
	Opcodes[0x99] = Opcode{Name: "STA", Op: opSTA, Mode: AddrAbsY, Cycles: 5, Size: 3}
	Opcodes[0x9D] = Opcode{Name: "STA", Op: opSTA, Mode: AddrAbsX, Cycles: 5, Size: 3}
	Opcodes[0x9F] = Opcode{Name: "STA", Op: opSTA, Mode: AddrLongX, Cycles: 5, Size: 4}

	// STX
	Opcodes[0x86] = Opcode{Name: "STX", Op: opSTX, Mode: AddrDir, Cycles: 3, Size: 2}
	Opcodes[0x8E] = Opcode{Name: "STX", Op: opSTX, Mode: AddrAbs, Cycles: 4, Size: 3}
	Opcodes[0x96] = Opcode{Name: "STX", Op: opSTX, Mode: AddrDirY, Cycles: 4, Size: 2}

	// STY
	Opcodes[0x84] = Opcode{Name: "STY", Op: opSTY, Mode: AddrDir, Cycles: 3, Size: 2}
	Opcodes[0x8C] = Opcode{Name: "STY", Op: opSTY, Mode: AddrAbs, Cycles: 4, Size: 3}
	Opcodes[0x94] = Opcode{Name: "STY", Op: opSTY, Mode: AddrDirX, Cycles: 4, Size: 2}

	// STZ
	Opcodes[0x64] = Opcode{Name: "STZ", Op: opSTZ, Mode: AddrDir, Cycles: 3, Size: 2}
	Opcodes[0x74] = Opcode{Name: "STZ", Op: opSTZ, Mode: AddrDirX, Cycles: 4, Size: 2}
	Opcodes[0x9C] = Opcode{Name: "STZ", Op: opSTZ, Mode: AddrAbs, Cycles: 4, Size: 3}
	Opcodes[0x9E] = Opcode{Name: "STZ", Op: opSTZ, Mode: AddrAbsX, Cycles: 5, Size: 3}

	// Compare Instructions
	// CMP
	Opcodes[0xC9] = Opcode{Name: "CMP", Op: opCMP, Mode: AddrImm, Cycles: 2, Size: 2}
	Opcodes[0xC5] = Opcode{Name: "CMP", Op: opCMP, Mode: AddrDir, Cycles: 3, Size: 2}
	Opcodes[0xC1] = Opcode{Name: "CMP", Op: opCMP, Mode: AddrIndX, Cycles: 6, Size: 2}
	Opcodes[0xC3] = Opcode{Name: "CMP", Op: opCMP, Mode: AddrSr, Cycles: 4, Size: 2}
	Opcodes[0xCD] = Opcode{Name: "CMP", Op: opCMP, Mode: AddrAbs, Cycles: 4, Size: 3}
	Opcodes[0xCF] = Opcode{Name: "CMP", Op: opCMP, Mode: AddrLong, Cycles: 5, Size: 4}
	Opcodes[0xD1] = Opcode{Name: "CMP", Op: opCMP, Mode: AddrIndY, Cycles: 5, Size: 2}
	Opcodes[0xD2] = Opcode{Name: "CMP", Op: opCMP, Mode: AddrDirInd, Cycles: 5, Size: 2}
	Opcodes[0xC7] = Opcode{Name: "CMP", Op: opCMP, Mode: AddrDirIndL, Cycles: 6, Size: 2} // [d]
	Opcodes[0xD3] = Opcode{Name: "CMP", Op: opCMP, Mode: AddrSrIndY, Cycles: 7, Size: 2}
	Opcodes[0xD5] = Opcode{Name: "CMP", Op: opCMP, Mode: AddrDirX, Cycles: 4, Size: 2}
	Opcodes[0xD7] = Opcode{Name: "CMP", Op: opCMP, Mode: AddrDirIndLIdxY, Cycles: 6, Size: 2} // [d],y
	Opcodes[0xD9] = Opcode{Name: "CMP", Op: opCMP, Mode: AddrAbsY, Cycles: 4, Size: 3}
	Opcodes[0xDD] = Opcode{Name: "CMP", Op: opCMP, Mode: AddrAbsX, Cycles: 4, Size: 3}
	Opcodes[0xDF] = Opcode{Name: "CMP", Op: opCMP, Mode: AddrLongX, Cycles: 5, Size: 4}

	// CPX
	Opcodes[0xE0] = Opcode{Name: "CPX", Op: opCPX, Mode: AddrImm, Cycles: 2, Size: 2}
	Opcodes[0xE4] = Opcode{Name: "CPX", Op: opCPX, Mode: AddrDir, Cycles: 3, Size: 2}
	Opcodes[0xEC] = Opcode{Name: "CPX", Op: opCPX, Mode: AddrAbs, Cycles: 4, Size: 3}

	// CPY
	Opcodes[0xC0] = Opcode{Name: "CPY", Op: opCPY, Mode: AddrImm, Cycles: 2, Size: 2}
	Opcodes[0xC4] = Opcode{Name: "CPY", Op: opCPY, Mode: AddrDir, Cycles: 3, Size: 2}
	Opcodes[0xCC] = Opcode{Name: "CPY", Op: opCPY, Mode: AddrAbs, Cycles: 4, Size: 3}

	// Branch Instructions
	Opcodes[0x90] = Opcode{Name: "BCC", Op: opBCC, Mode: AddrRel, Cycles: 2, Size: 2}
	Opcodes[0xB0] = Opcode{Name: "BCS", Op: opBCS, Mode: AddrRel, Cycles: 2, Size: 2}
	Opcodes[0xF0] = Opcode{Name: "BEQ", Op: opBEQ, Mode: AddrRel, Cycles: 2, Size: 2}
	Opcodes[0xD0] = Opcode{Name: "BNE", Op: opBNE, Mode: AddrRel, Cycles: 2, Size: 2}
	Opcodes[0x30] = Opcode{Name: "BMI", Op: opBMI, Mode: AddrRel, Cycles: 2, Size: 2}
	Opcodes[0x10] = Opcode{Name: "BPL", Op: opBPL, Mode: AddrRel, Cycles: 2, Size: 2}
	Opcodes[0x50] = Opcode{Name: "BVC", Op: opBVC, Mode: AddrRel, Cycles: 2, Size: 2}
	Opcodes[0x70] = Opcode{Name: "BVS", Op: opBVS, Mode: AddrRel, Cycles: 2, Size: 2}
	Opcodes[0x80] = Opcode{Name: "BRA", Op: opBRA, Mode: AddrRel, Cycles: 2, Size: 2}
	Opcodes[0x82] = Opcode{Name: "BRL", Op: opBRL, Mode: AddrRelL, Cycles: 4, Size: 3}

	Opcodes[0xB8] = Opcode{Name: "CLV", Op: opCLV, Mode: AddrImpl, Cycles: 2, Size: 1}
	Opcodes[0xF4] = Opcode{Name: "PEA", Op: opPEA, Mode: AddrAbs, Cycles: 5, Size: 3} // Effective Absolute (Immediate 16-bit push)

	// Increment/Decrement
	Opcodes[0x1A] = Opcode{Name: "INC", Op: opINC, Mode: AddrAcc, Cycles: 2, Size: 1}
	Opcodes[0xE6] = Opcode{Name: "INC", Op: opINC, Mode: AddrDir, Cycles: 5, Size: 2}
	Opcodes[0xEE] = Opcode{Name: "INC", Op: opINC, Mode: AddrAbs, Cycles: 6, Size: 3}
	Opcodes[0xF6] = Opcode{Name: "INC", Op: opINC, Mode: AddrDirX, Cycles: 6, Size: 2}
	Opcodes[0xFE] = Opcode{Name: "INC", Op: opINC, Mode: AddrAbsX, Cycles: 7, Size: 3}

	Opcodes[0x3A] = Opcode{Name: "DEC", Op: opDEC, Mode: AddrAcc, Cycles: 2, Size: 1}
	Opcodes[0xC6] = Opcode{Name: "DEC", Op: opDEC, Mode: AddrDir, Cycles: 5, Size: 2}
	Opcodes[0xCE] = Opcode{Name: "DEC", Op: opDEC, Mode: AddrAbs, Cycles: 6, Size: 3}
	Opcodes[0xD6] = Opcode{Name: "DEC", Op: opDEC, Mode: AddrDirX, Cycles: 6, Size: 2}
	Opcodes[0xDE] = Opcode{Name: "DEC", Op: opDEC, Mode: AddrAbsX, Cycles: 7, Size: 3}

	Opcodes[0xE8] = Opcode{Name: "INX", Op: opINX, Mode: AddrImpl, Cycles: 2, Size: 1}
	Opcodes[0xC8] = Opcode{Name: "INY", Op: opINY, Mode: AddrImpl, Cycles: 2, Size: 1}
	Opcodes[0xCA] = Opcode{Name: "DEX", Op: opDEX, Mode: AddrImpl, Cycles: 2, Size: 1}
	Opcodes[0x88] = Opcode{Name: "DEY", Op: opDEY, Mode: AddrImpl, Cycles: 2, Size: 1}

	// Logic Instructions
	// AND
	Opcodes[0x29] = Opcode{Name: "AND", Op: opAND, Mode: AddrImm, Cycles: 2, Size: 2}
	Opcodes[0x25] = Opcode{Name: "AND", Op: opAND, Mode: AddrDir, Cycles: 3, Size: 2}
	Opcodes[0x2D] = Opcode{Name: "AND", Op: opAND, Mode: AddrAbs, Cycles: 4, Size: 3}
	Opcodes[0x35] = Opcode{Name: "AND", Op: opAND, Mode: AddrDirX, Cycles: 4, Size: 2}
	Opcodes[0x3D] = Opcode{Name: "AND", Op: opAND, Mode: AddrAbsX, Cycles: 4, Size: 3}
	Opcodes[0x39] = Opcode{Name: "AND", Op: opAND, Mode: AddrAbsY, Cycles: 4, Size: 3}
	Opcodes[0x21] = Opcode{Name: "AND", Op: opAND, Mode: AddrIndX, Cycles: 6, Size: 2}
	Opcodes[0x31] = Opcode{Name: "AND", Op: opAND, Mode: AddrIndY, Cycles: 5, Size: 2}
	Opcodes[0x32] = Opcode{Name: "AND", Op: opAND, Mode: AddrDirInd, Cycles: 5, Size: 2}
	Opcodes[0x37] = Opcode{Name: "AND", Op: opAND, Mode: AddrDirIndLIdxY, Cycles: 6, Size: 2} // [d],y
	Opcodes[0x27] = Opcode{Name: "AND", Op: opAND, Mode: AddrDirIndL, Cycles: 6, Size: 2}
	Opcodes[0x2F] = Opcode{Name: "AND", Op: opAND, Mode: AddrLong, Cycles: 5, Size: 4}
	Opcodes[0x3F] = Opcode{Name: "AND", Op: opAND, Mode: AddrLongX, Cycles: 5, Size: 4}
	Opcodes[0x23] = Opcode{Name: "AND", Op: opAND, Mode: AddrSr, Cycles: 4, Size: 2}
	Opcodes[0x33] = Opcode{Name: "AND", Op: opAND, Mode: AddrSrIndY, Cycles: 7, Size: 2}

	// ORA
	Opcodes[0x09] = Opcode{Name: "ORA", Op: opORA, Mode: AddrImm, Cycles: 2, Size: 2}
	Opcodes[0x05] = Opcode{Name: "ORA", Op: opORA, Mode: AddrDir, Cycles: 3, Size: 2}
	Opcodes[0x0D] = Opcode{Name: "ORA", Op: opORA, Mode: AddrAbs, Cycles: 4, Size: 3}
	Opcodes[0x15] = Opcode{Name: "ORA", Op: opORA, Mode: AddrDirX, Cycles: 4, Size: 2}
	Opcodes[0x1D] = Opcode{Name: "ORA", Op: opORA, Mode: AddrAbsX, Cycles: 4, Size: 3}
	Opcodes[0x19] = Opcode{Name: "ORA", Op: opORA, Mode: AddrAbsY, Cycles: 4, Size: 3}
	Opcodes[0x01] = Opcode{Name: "ORA", Op: opORA, Mode: AddrIndX, Cycles: 6, Size: 2}
	Opcodes[0x11] = Opcode{Name: "ORA", Op: opORA, Mode: AddrIndY, Cycles: 5, Size: 2}
	Opcodes[0x12] = Opcode{Name: "ORA", Op: opORA, Mode: AddrDirInd, Cycles: 5, Size: 2}
	Opcodes[0x07] = Opcode{Name: "ORA", Op: opORA, Mode: AddrDirIndL, Cycles: 6, Size: 2}
	Opcodes[0x0F] = Opcode{Name: "ORA", Op: opORA, Mode: AddrLong, Cycles: 5, Size: 4}
	Opcodes[0x1F] = Opcode{Name: "ORA", Op: opORA, Mode: AddrLongX, Cycles: 5, Size: 4}
	Opcodes[0x03] = Opcode{Name: "ORA", Op: opORA, Mode: AddrSr, Cycles: 4, Size: 2}
	Opcodes[0x13] = Opcode{Name: "ORA", Op: opORA, Mode: AddrSrIndY, Cycles: 7, Size: 2}
	Opcodes[0x17] = Opcode{Name: "ORA", Op: opORA, Mode: AddrDirIndLIdxY, Cycles: 6, Size: 2} // [d],y

	// EOR
	Opcodes[0x49] = Opcode{Name: "EOR", Op: opEOR, Mode: AddrImm, Cycles: 2, Size: 2}
	Opcodes[0x45] = Opcode{Name: "EOR", Op: opEOR, Mode: AddrDir, Cycles: 3, Size: 2}
	Opcodes[0x4D] = Opcode{Name: "EOR", Op: opEOR, Mode: AddrAbs, Cycles: 4, Size: 3}
	Opcodes[0x55] = Opcode{Name: "EOR", Op: opEOR, Mode: AddrDirX, Cycles: 4, Size: 2}
	Opcodes[0x5D] = Opcode{Name: "EOR", Op: opEOR, Mode: AddrAbsX, Cycles: 4, Size: 3}
	Opcodes[0x59] = Opcode{Name: "EOR", Op: opEOR, Mode: AddrAbsY, Cycles: 4, Size: 3}
	Opcodes[0x41] = Opcode{Name: "EOR", Op: opEOR, Mode: AddrIndX, Cycles: 6, Size: 2}
	Opcodes[0x51] = Opcode{Name: "EOR", Op: opEOR, Mode: AddrIndY, Cycles: 5, Size: 2}
	Opcodes[0x52] = Opcode{Name: "EOR", Op: opEOR, Mode: AddrDirInd, Cycles: 5, Size: 2}
	Opcodes[0x47] = Opcode{Name: "EOR", Op: opEOR, Mode: AddrDirIndL, Cycles: 6, Size: 2}
	Opcodes[0x57] = Opcode{Name: "EOR", Op: opEOR, Mode: AddrDirIndLIdxY, Cycles: 6, Size: 2}
	Opcodes[0x4F] = Opcode{Name: "EOR", Op: opEOR, Mode: AddrLong, Cycles: 5, Size: 4}
	Opcodes[0x5F] = Opcode{Name: "EOR", Op: opEOR, Mode: AddrLongX, Cycles: 5, Size: 4}
	Opcodes[0x43] = Opcode{Name: "EOR", Op: opEOR, Mode: AddrSr, Cycles: 4, Size: 2}
	Opcodes[0x53] = Opcode{Name: "EOR", Op: opEOR, Mode: AddrSrIndY, Cycles: 7, Size: 2}

	// BIT
	Opcodes[0x89] = Opcode{Name: "BIT", Op: opBIT, Mode: AddrImm, Cycles: 2, Size: 2}
	Opcodes[0x24] = Opcode{Name: "BIT", Op: opBIT, Mode: AddrDir, Cycles: 3, Size: 2}
	Opcodes[0x2C] = Opcode{Name: "BIT", Op: opBIT, Mode: AddrAbs, Cycles: 4, Size: 3}
	Opcodes[0x34] = Opcode{Name: "BIT", Op: opBIT, Mode: AddrDirX, Cycles: 4, Size: 2}
	Opcodes[0x3C] = Opcode{Name: "BIT", Op: opBIT, Mode: AddrAbsX, Cycles: 4, Size: 3}

	// TSB
	Opcodes[0x04] = Opcode{Name: "TSB", Op: opTSB, Mode: AddrDir, Cycles: 5, Size: 2}
	Opcodes[0x0C] = Opcode{Name: "TSB", Op: opTSB, Mode: AddrAbs, Cycles: 6, Size: 3}

	// TRB
	Opcodes[0x14] = Opcode{Name: "TRB", Op: opTRB, Mode: AddrDir, Cycles: 5, Size: 2}
	Opcodes[0x1C] = Opcode{Name: "TRB", Op: opTRB, Mode: AddrAbs, Cycles: 6, Size: 3}

	// Logic Instructions
	// ... (Existing Logic Instructions) ...

	// Shift Instructions
	// ASL
	Opcodes[0x0A] = Opcode{Name: "ASL", Op: opASL, Mode: AddrAcc, Cycles: 2, Size: 1}
	Opcodes[0x06] = Opcode{Name: "ASL", Op: opASL, Mode: AddrDir, Cycles: 5, Size: 2}
	Opcodes[0x0E] = Opcode{Name: "ASL", Op: opASL, Mode: AddrAbs, Cycles: 6, Size: 3}
	Opcodes[0x16] = Opcode{Name: "ASL", Op: opASL, Mode: AddrDirX, Cycles: 6, Size: 2}
	Opcodes[0x1E] = Opcode{Name: "ASL", Op: opASL, Mode: AddrAbsX, Cycles: 7, Size: 3}

	// LSR
	Opcodes[0x4A] = Opcode{Name: "LSR", Op: opLSR, Mode: AddrAcc, Cycles: 2, Size: 1}
	Opcodes[0x46] = Opcode{Name: "LSR", Op: opLSR, Mode: AddrDir, Cycles: 5, Size: 2}
	Opcodes[0x4E] = Opcode{Name: "LSR", Op: opLSR, Mode: AddrAbs, Cycles: 6, Size: 3}
	Opcodes[0x56] = Opcode{Name: "LSR", Op: opLSR, Mode: AddrDirX, Cycles: 6, Size: 2}
	Opcodes[0x5E] = Opcode{Name: "LSR", Op: opLSR, Mode: AddrAbsX, Cycles: 7, Size: 3}

	// ROL
	Opcodes[0x2A] = Opcode{Name: "ROL", Op: opROL, Mode: AddrAcc, Cycles: 2, Size: 1}
	Opcodes[0x26] = Opcode{Name: "ROL", Op: opROL, Mode: AddrDir, Cycles: 5, Size: 2}
	Opcodes[0x2E] = Opcode{Name: "ROL", Op: opROL, Mode: AddrAbs, Cycles: 6, Size: 3}
	Opcodes[0x36] = Opcode{Name: "ROL", Op: opROL, Mode: AddrDirX, Cycles: 6, Size: 2}
	Opcodes[0x3E] = Opcode{Name: "ROL", Op: opROL, Mode: AddrAbsX, Cycles: 7, Size: 3}

	// 0x62 PER
	Opcodes[0x62] = Opcode{Name: "PER", Op: opPER, Mode: AddrRelL, Cycles: 6, Size: 3}

	// ROR
	Opcodes[0x6A] = Opcode{Name: "ROR", Op: opROR, Mode: AddrAcc, Cycles: 2, Size: 1}
	Opcodes[0x66] = Opcode{Name: "ROR", Op: opROR, Mode: AddrDir, Cycles: 5, Size: 2}
	Opcodes[0x6E] = Opcode{Name: "ROR", Op: opROR, Mode: AddrAbs, Cycles: 6, Size: 3}
	Opcodes[0x76] = Opcode{Name: "ROR", Op: opROR, Mode: AddrDirX, Cycles: 6, Size: 2}
	Opcodes[0x7E] = Opcode{Name: "ROR", Op: opROR, Mode: AddrAbsX, Cycles: 7, Size: 3}

	// ADC
	Opcodes[0x69] = Opcode{Name: "ADC", Op: opADC, Mode: AddrImm, Cycles: 2, Size: 2}
	Opcodes[0x65] = Opcode{Name: "ADC", Op: opADC, Mode: AddrDir, Cycles: 3, Size: 2}
	Opcodes[0x6D] = Opcode{Name: "ADC", Op: opADC, Mode: AddrAbs, Cycles: 4, Size: 3}
	Opcodes[0x75] = Opcode{Name: "ADC", Op: opADC, Mode: AddrDirX, Cycles: 4, Size: 2}
	Opcodes[0x7D] = Opcode{Name: "ADC", Op: opADC, Mode: AddrAbsX, Cycles: 4, Size: 3}
	Opcodes[0x79] = Opcode{Name: "ADC", Op: opADC, Mode: AddrAbsY, Cycles: 4, Size: 3}
	Opcodes[0x61] = Opcode{Name: "ADC", Op: opADC, Mode: AddrIndX, Cycles: 6, Size: 2}
	Opcodes[0x71] = Opcode{Name: "ADC", Op: opADC, Mode: AddrIndY, Cycles: 5, Size: 2}
	Opcodes[0x72] = Opcode{Name: "ADC", Op: opADC, Mode: AddrDirInd, Cycles: 5, Size: 2}
	Opcodes[0x67] = Opcode{Name: "ADC", Op: opADC, Mode: AddrDirIndL, Cycles: 6, Size: 2}
	Opcodes[0x6F] = Opcode{Name: "ADC", Op: opADC, Mode: AddrLong, Cycles: 5, Size: 4}
	Opcodes[0x7F] = Opcode{Name: "ADC", Op: opADC, Mode: AddrLongX, Cycles: 5, Size: 4}
	Opcodes[0x63] = Opcode{Name: "ADC", Op: opADC, Mode: AddrSr, Cycles: 4, Size: 2}
	Opcodes[0x73] = Opcode{Name: "ADC", Op: opADC, Mode: AddrSrIndY, Cycles: 7, Size: 2}

	// SBC
	Opcodes[0xE9] = Opcode{Name: "SBC", Op: opSBC, Mode: AddrImm, Cycles: 2, Size: 2}
	Opcodes[0xE5] = Opcode{Name: "SBC", Op: opSBC, Mode: AddrDir, Cycles: 3, Size: 2}
	Opcodes[0xED] = Opcode{Name: "SBC", Op: opSBC, Mode: AddrAbs, Cycles: 4, Size: 3}
	Opcodes[0xF5] = Opcode{Name: "SBC", Op: opSBC, Mode: AddrDirX, Cycles: 4, Size: 2}
	Opcodes[0xFD] = Opcode{Name: "SBC", Op: opSBC, Mode: AddrAbsX, Cycles: 4, Size: 3}
	Opcodes[0xF9] = Opcode{Name: "SBC", Op: opSBC, Mode: AddrAbsY, Cycles: 4, Size: 3}
	Opcodes[0xE1] = Opcode{Name: "SBC", Op: opSBC, Mode: AddrIndX, Cycles: 6, Size: 2}
	Opcodes[0xF1] = Opcode{Name: "SBC", Op: opSBC, Mode: AddrIndY, Cycles: 5, Size: 2}
	Opcodes[0xF2] = Opcode{Name: "SBC", Op: opSBC, Mode: AddrDirInd, Cycles: 5, Size: 2}
	Opcodes[0xE7] = Opcode{Name: "SBC", Op: opSBC, Mode: AddrDirIndL, Cycles: 6, Size: 2}
	Opcodes[0xF7] = Opcode{Name: "SBC", Op: opSBC, Mode: AddrDirIndLIdxY, Cycles: 6, Size: 2}
	Opcodes[0xEF] = Opcode{Name: "SBC", Op: opSBC, Mode: AddrLong, Cycles: 5, Size: 4}
	Opcodes[0xFF] = Opcode{Name: "SBC", Op: opSBC, Mode: AddrLongX, Cycles: 5, Size: 4}
	Opcodes[0xE3] = Opcode{Name: "SBC", Op: opSBC, Mode: AddrSr, Cycles: 4, Size: 2}
	Opcodes[0xF3] = Opcode{Name: "SBC", Op: opSBC, Mode: AddrSrIndY, Cycles: 7, Size: 2}
}

func opNOP(c *CPU, mode AddressingMode) {
	// Do nothing, but consume internal cycle (6 master)
	c.AddCycles(6)
}

func init() {
	// 0x42 WDM
	Opcodes[0x42] = Opcode{Name: "WDM", Op: opWDM, Mode: AddrImm, Cycles: 2, Size: 2}
}

func opWDM(c *CPU, mode AddressingMode) {
	// 42: WDM (Reserved)
	// 2 bytes: Opcode + Signature
	c.fetchByte()
}

func opXCE(c *CPU, mode AddressingMode) {
	// Exchange Carry bit (bit 0 of P) with Emulation bit (E)
	c.AddCycles(6)

	carry := (c.P & 0x01) != 0
	emulation := c.E

	if carry {
		c.E = true
	} else {
		c.E = false
	}

	if emulation {
		c.P |= 0x01 // Set Carry
	} else {
		c.P &= 0xFE // Clear Carry
	}

	if c.E {
		// Switch to Emulation Mode
		c.P |= 0x30 // Force M=1, X=1 in Emulation Mode (Bits 4 and 5)
		c.X &= 0xFF
		c.Y &= 0xFF
		c.S = (c.S & 0xFF) | 0x0100 // Stack fixed to page 1
	} else {
		// Switch to Native Mode
		if emulation {
			// Switching from Emulation to Native forces M=1, X=1
			c.P |= 0x30
		}
		if (c.P & 0x10) != 0 {
			c.X &= 0xFF
			c.Y &= 0xFF
		}
	}
}

func opCLC(c *CPU, mode AddressingMode) {
	c.AddCycles(6)
	c.P &= 0xFE // Clear Carry
}

func opSEC(c *CPU, mode AddressingMode) {
	c.AddCycles(6)
	c.P |= 0x01 // Set Carry
}

func opREP(c *CPU, mode AddressingMode) {
	// Reset Status Bits (Clear bits specified by immediate operand)
	val := c.fetchByte()
	c.AddCycles(6) // Internal processing
	c.P &= ^val

	c.updateMXFlags()
}

func opSEP(c *CPU, mode AddressingMode) {
	// Set Status Bits (Set bits specified by immediate operand)
	val := c.fetchByte()
	c.AddCycles(6) // Internal processing
	c.P |= val

	c.updateMXFlags()
}

func opXBA(c *CPU, mode AddressingMode) {
	// Exchange B and A
	c.AddCycles(6)
	c.A = (c.A >> 8) | (c.A << 8)
	c.setNZ(uint8(c.A & 0xFF))
}

// Helpers

func (c *CPU) updateMXFlags() {
	if c.E {
		return // M and X are forced 1 in Emulation mode effectively
	}

	// If X flag (bit 4) is set (8-bit Index), clear high bytes of X and Y
	if (c.P & 0x10) != 0 {
		c.X &= 0xFF
		c.Y &= 0xFF
	}
	// M flag (bit 5) controls Accumulator size.
	// Switching M 1->0 doesn't clear high byte (hidden B is preserved).
	// Switching M 0->1 doesn't clear high byte (it becomes hidden B).
}

func (c *CPU) setNZ(val uint8) {
	if val == 0 {
		c.P |= 0x02 // Set Zero
	} else {
		c.P &= 0xFD // Clear Zero
	}

	if (val & 0x80) != 0 {
		c.P |= 0x80 // Set Negative
	} else {
		c.P &= 0x7F // Clear Negative
	}
}

func (c *CPU) setNZ16(val uint16) {
	if val == 0 {
		c.P |= 0x02
	} else {
		c.P &= 0xFD
	}

	if (val & 0x8000) != 0 {
		c.P |= 0x80
	} else {
		c.P &= 0x7F
	}
}
