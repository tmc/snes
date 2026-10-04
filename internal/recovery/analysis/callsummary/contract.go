package callsummary

import (
	"errors"
	"fmt"
)

// Standard errors returned during contract analysis and verification.
var (
	ErrUnbalancedStack   = errors.New("callsummary: unbalanced stack delta")
	ErrMismatchedReturn  = errors.New("callsummary: mismatched call and return opcode")
	ErrNoReturn          = errors.New("callsummary: no return instruction reached")
	ErrCyclicRoutine     = errors.New("callsummary: cyclic routine detected")
	ErrStackUnderflow    = errors.New("callsummary: stack underflow")
	ErrUnsummarizedCall  = errors.New("callsummary: unsummarized nested call")
)

// Common call and return opcodes for the 65816 processor.
const (
	CallJSR   byte = 0x20 // JSR abs (pushes 2-byte return address)
	CallJSL   byte = 0x22 // JSL long (pushes 3-byte return address: PB, PCH, PCL)
	ReturnRTS byte = 0x60 // RTS (pops 2-byte return address)
	ReturnRTL byte = 0x6B // RTL (pops 3-byte return address)
)

// Status describes whether a register or flag is preserved, guaranteed, or clobbered.
// The zero value is Clobbered, ensuring an uninitialized contract is safely conservative.
type Status int

const (
	Clobbered Status = iota
	Preserved
	Guaranteed
)

func (s Status) String() string {
	switch s {
	case Preserved:
		return "preserved"
	case Guaranteed:
		return "guaranteed"
	case Clobbered:
		return "clobbered"
	default:
		return "unknown"
	}
}

// Register identifies a 65816 CPU register.
type Register int

const (
	RegA Register = iota
	RegX
	RegY
	RegS
	RegDP
	RegDB
	RegPB
)

func (r Register) String() string {
	switch r {
	case RegA:
		return "A"
	case RegX:
		return "X"
	case RegY:
		return "Y"
	case RegS:
		return "S"
	case RegDP:
		return "DP"
	case RegDB:
		return "DB"
	case RegPB:
		return "PB"
	default:
		return fmt.Sprintf("Reg(%d)", int(r))
	}
}

// Flag identifies a 65816 processor status flag.
type Flag int

const (
	FlagM Flag = iota
	FlagX
	FlagC
	FlagZ
	FlagN
	FlagI
	FlagD
)

func (f Flag) String() string {
	switch f {
	case FlagM:
		return "M"
	case FlagX:
		return "X"
	case FlagC:
		return "C"
	case FlagZ:
		return "Z"
	case FlagN:
		return "N"
	case FlagI:
		return "I"
	case FlagD:
		return "D"
	default:
		return fmt.Sprintf("Flag(%d)", int(f))
	}
}

// RegisterState represents the post-call state of a register.
type RegisterState struct {
	Status Status
	Value  uint16 // Valid when Status == Guaranteed
}

func (rs RegisterState) String() string {
	switch rs.Status {
	case Preserved:
		return "preserved"
	case Guaranteed:
		return fmt.Sprintf("guaranteed($%04X)", rs.Value)
	default:
		return "clobbered"
	}
}

// FlagState represents the post-call state of a status flag.
type FlagState struct {
	Status Status
	Value  bool // Valid when Status == Guaranteed
}

func (fs FlagState) String() string {
	switch fs.Status {
	case Preserved:
		return "preserved"
	case Guaranteed:
		if fs.Value {
			return "guaranteed(1)"
		}
		return "guaranteed(0)"
	default:
		return "clobbered"
	}
}

// Registers summarizes the post-call status of 65816 registers.
type Registers struct {
	A  RegisterState
	X  RegisterState
	Y  RegisterState
	S  RegisterState
	DP RegisterState
	DB RegisterState
	PB RegisterState
}

// Flags summarizes the post-call status of 65816 processor status flags.
type Flags struct {
	M FlagState
	X FlagState
	C FlagState
	Z FlagState
	N FlagState
	I FlagState
	D FlagState
}

// CallContract models the caller-callee register preservation and stack discipline contract
// for a 65816 subroutine.
type CallContract struct {
	Registers   Registers
	Flags       Flags
	StackDelta  int
	ReturnsWith byte // 0x60 for RTS, 0x6B for RTL
}

// CalleeSummary is an alias for CallContract.
type CalleeSummary = CallContract

// PreservedContract returns a contract where all registers and flags are marked preserved,
// with a balanced stack delta and the specified return opcode.
func PreservedContract(returnsWith byte) CallContract {
	return CallContract{
		Registers: Registers{
			A:  RegisterState{Status: Preserved},
			X:  RegisterState{Status: Preserved},
			Y:  RegisterState{Status: Preserved},
			S:  RegisterState{Status: Preserved},
			DP: RegisterState{Status: Preserved},
			DB: RegisterState{Status: Preserved},
			PB: RegisterState{Status: Preserved},
		},
		Flags: Flags{
			M: FlagState{Status: Preserved},
			X: FlagState{Status: Preserved},
			C: FlagState{Status: Preserved},
			Z: FlagState{Status: Preserved},
			N: FlagState{Status: Preserved},
			I: FlagState{Status: Preserved},
			D: FlagState{Status: Preserved},
		},
		StackDelta:  0,
		ReturnsWith: returnsWith,
	}
}

// ClobberedContract returns a conservative contract where all registers and flags are clobbered.
func ClobberedContract(returnsWith byte) CallContract {
	return CallContract{
		ReturnsWith: returnsWith,
	}
}

// Preserves reports whether register r is guaranteed preserved across the call.
func (c CallContract) Preserves(r Register) bool {
	return c.Register(r).Status == Preserved
}

// PreservesFlag reports whether flag f is guaranteed preserved across the call.
func (c CallContract) PreservesFlag(f Flag) bool {
	return c.Flag(f).Status == Preserved
}

// IsBalanced reports whether the call maintains balanced stack discipline (StackDelta == 0).
func (c CallContract) IsBalanced() bool {
	return c.StackDelta == 0 && c.Registers.S.Status == Preserved
}

// Register returns the state of the specified register.
func (c CallContract) Register(r Register) RegisterState {
	switch r {
	case RegA:
		return c.Registers.A
	case RegX:
		return c.Registers.X
	case RegY:
		return c.Registers.Y
	case RegS:
		return c.Registers.S
	case RegDP:
		return c.Registers.DP
	case RegDB:
		return c.Registers.DB
	case RegPB:
		return c.Registers.PB
	default:
		return RegisterState{Status: Clobbered}
	}
}

// SetRegister sets the state of the specified register.
func (c *CallContract) SetRegister(r Register, s RegisterState) {
	switch r {
	case RegA:
		c.Registers.A = s
	case RegX:
		c.Registers.X = s
	case RegY:
		c.Registers.Y = s
	case RegS:
		c.Registers.S = s
	case RegDP:
		c.Registers.DP = s
	case RegDB:
		c.Registers.DB = s
	case RegPB:
		c.Registers.PB = s
	}
}

// Flag returns the state of the specified status flag.
func (c CallContract) Flag(f Flag) FlagState {
	switch f {
	case FlagM:
		return c.Flags.M
	case FlagX:
		return c.Flags.X
	case FlagC:
		return c.Flags.C
	case FlagZ:
		return c.Flags.Z
	case FlagN:
		return c.Flags.N
	case FlagI:
		return c.Flags.I
	case FlagD:
		return c.Flags.D
	default:
		return FlagState{Status: Clobbered}
	}
}

// SetFlag sets the state of the specified status flag.
func (c *CallContract) SetFlag(f Flag, s FlagState) {
	switch f {
	case FlagM:
		c.Flags.M = s
	case FlagX:
		c.Flags.X = s
	case FlagC:
		c.Flags.C = s
	case FlagZ:
		c.Flags.Z = s
	case FlagN:
		c.Flags.N = s
	case FlagI:
		c.Flags.I = s
	case FlagD:
		c.Flags.D = s
	}
}

func (c CallContract) String() string {
	return fmt.Sprintf("CallContract{DB:%s DP:%s S:%s(delta=%d) A:%s X:%s Y:%s PB:%s M:%s X:%s ret:0x%02X}",
		c.Registers.DB, c.Registers.DP, c.Registers.S, c.StackDelta,
		c.Registers.A, c.Registers.X, c.Registers.Y, c.Registers.PB,
		c.Flags.M, c.Flags.X, c.ReturnsWith)
}

// VerifyDiscipline verifies that callOp and contract satisfy caller-callee stack discipline:
// JSR (0x20) must pair with RTS (0x60, 2-byte return address), JSL (0x22) must pair with RTL
// (0x6B, 3-byte return address), and StackDelta must be zero.
func VerifyDiscipline(callOp byte, contract CallContract) error {
	switch callOp {
	case CallJSR:
		if contract.ReturnsWith != ReturnRTS {
			return fmt.Errorf("call 0x%02X (JSR) returned with 0x%02X, want 0x%02X (RTS): %w",
				callOp, contract.ReturnsWith, ReturnRTS, ErrMismatchedReturn)
		}
	case CallJSL:
		if contract.ReturnsWith != ReturnRTL {
			return fmt.Errorf("call 0x%02X (JSL) returned with 0x%02X, want 0x%02X (RTL): %w",
				callOp, contract.ReturnsWith, ReturnRTL, ErrMismatchedReturn)
		}
	default:
		return fmt.Errorf("unsupported call opcode 0x%02X", callOp)
	}

	if contract.StackDelta != 0 {
		return fmt.Errorf("stack delta is %d, want 0: %w", contract.StackDelta, ErrUnbalancedStack)
	}
	return nil
}
