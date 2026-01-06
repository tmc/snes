package cpu

// AddressingMode represents the 65816 addressing modes.
type AddressingMode int

const (
	AddrImpl        AddressingMode = iota // Implied
	AddrAcc                               // Accumulator
	AddrImm                               // Immediate #const
	AddrAbs                               // Absolute addr
	AddrAbsX                              // Absolute, X-indexed addr,X
	AddrAbsY                              // Absolute, Y-indexed addr,Y
	AddrDir                               // Direct Page dp
	AddrDirX                              // Direct Page, X-indexed dp,X
	AddrDirY                              // Direct Page, Y-indexed dp,Y
	AddrInd                               // Computed Indirect (addr)
	AddrIndX                              // Indexed Indirect (dp,X)
	AddrIndY                              // Indirect Indexed (dp),Y
	AddrLong                              // Long long
	AddrLongX                             // Long, X-indexed long,X
	AddrSr                                // Stack Relative sr
	AddrSrIndY                            // Stack Relative Indirect Indexed (sr,S),Y
	AddrRel                               // Relative nearlabel
	AddrRelL                              // Relative Long longlabel
	AddrDirInd                            // Direct Indirect (dp)
	AddrDirIndL                           // Direct Indirect Long [dp]
	AddrAbsInd                            // Absolute Indirect (addr)
	AddrAbsIndX                           // Absolute Indexed Indirect (addr,X)
	AddrAbsIndLong                        // Absolute Indirect Long [addr]
	AddrBlock                             // Block Move dest,src
	AddrDirIndLIdxY                       // Direct Indirect Long Indexed [dp],Y
)

// Instruction represents the logic for a single opcode.
type Instruction func(c *CPU, mode AddressingMode)

// Opcode represents a single entry in the instruction set.
type Opcode struct {
	Name   string
	Op     Instruction
	Mode   AddressingMode
	Cycles uint8
	Size   uint8 // Instruction size in bytes
}

// Opcodes is the lookup table for the 256 instructions.
var Opcodes [256]Opcode
