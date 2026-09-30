package decomp

import (
	"fmt"

	"github.com/tmc/snes/internal/recovery"
)

// Width specifies operand bit width.
type Width int

const (
	Width8  Width = 8
	Width16 Width = 16
	Width24 Width = 24
)

// Register represents a 65816 CPU register.
type Register string

const (
	RegA  Register = "A"  // Accumulator
	RegX  Register = "X"  // Index X
	RegY  Register = "Y"  // Index Y
	RegS  Register = "S"  // Stack Pointer
	RegD  Register = "D"  // Direct Page
	RegDB Register = "DB" // Data Bank
	RegPB Register = "PB" // Program Bank
	RegP  Register = "P"  // Status Register
	RegPC Register = "PC" // Program Counter
)

// Flag represents a bit in the status register P.
type Flag string

const (
	FlagN Flag = "N" // Negative
	FlagV Flag = "V" // Overflow
	FlagM Flag = "M" // Memory/Accumulator select (0: 16-bit, 1: 8-bit)
	FlagX Flag = "X" // Index register select (0: 16-bit, 1: 8-bit)
	FlagD Flag = "D" // Decimal
	FlagI Flag = "I" // IRQ disable
	FlagZ Flag = "Z" // Zero
	FlagC Flag = "C" // Carry
)

// BinaryOp specifies an arithmetic or logical operation.
type BinaryOp string

const (
	OpAdd   BinaryOp = "+"
	OpSub   BinaryOp = "-"
	OpAnd   BinaryOp = "&"
	OpOr    BinaryOp = "|"
	OpXor   BinaryOp = "^"
	OpShl   BinaryOp = "<<"
	OpShr   BinaryOp = ">>"
	OpEqual BinaryOp = "=="
	OpNotEq BinaryOp = "!="
	OpLt    BinaryOp = "<"
	OpLte   BinaryOp = "<="
	OpGt    BinaryOp = ">"
	OpGte   BinaryOp = ">="
)

// UnaryOp specifies a unary operation.
type UnaryOp string

const (
	OpNot  UnaryOp = "~"
	OpNeg  UnaryOp = "-"
	OpLNot UnaryOp = "!"
)

// Expr represents a typed value expression.
type Expr interface {
	exprNode()
	String() string
}

type ConstExpr struct {
	Value uint32
	Width Width
}

func (e *ConstExpr) exprNode() {}
func (e *ConstExpr) String() string {
	switch e.Width {
	case Width8:
		return fmt.Sprintf("0x%02X", e.Value&0xFF)
	case Width16:
		return fmt.Sprintf("0x%04X", e.Value&0xFFFF)
	default:
		return fmt.Sprintf("0x%06X", e.Value&0xFFFFFF)
	}
}

type RegExpr struct {
	Reg   Register
	Width Width
}

func (e *RegExpr) exprNode() {}
func (e *RegExpr) String() string {
	if e.Reg == RegA && e.Width == Width8 {
		return "(A & 0xFF)"
	}
	return string(e.Reg)
}

type FlagExpr struct {
	Flag Flag
}

func (e *FlagExpr) exprNode() {}
func (e *FlagExpr) String() string {
	return "P." + string(e.Flag)
}

type TempExpr struct {
	Name  string
	Width Width
}

func (e *TempExpr) exprNode() {}
func (e *TempExpr) String() string {
	return e.Name
}

type BinaryExpr struct {
	Op    BinaryOp
	Left  Expr
	Right Expr
	Width Width
}

func (e *BinaryExpr) exprNode() {}
func (e *BinaryExpr) String() string {
	return fmt.Sprintf("(%s %s %s)", e.Left, e.Op, e.Right)
}

type UnaryExpr struct {
	Op    UnaryOp
	Expr  Expr
	Width Width
}

func (e *UnaryExpr) exprNode() {}
func (e *UnaryExpr) String() string {
	return fmt.Sprintf("(%s%s)", e.Op, e.Expr)
}

// WordAddressing selects how a word access advances to its high byte.
// The zero value carries through the 24-bit bus address. Direct-page,
// stack-relative and bank-zero pointer accesses wrap within bank zero.
type WordAddressing uint8

const (
	WordLinear24 WordAddressing = iota
	WordBankZero16
)

type MemReadExpr struct {
	WordAddressing WordAddressing
	Address        Expr
	Width          Width
	Space          string
}

func (e *MemReadExpr) exprNode() {}
func (e *MemReadExpr) String() string {
	return fmt.Sprintf("read%d(%s)", e.Width, e.Address)
}

// Statement represents a single machine-semantic IR statement.
type Statement struct {
	InstructionID string `json:"instruction_id"`
	Address       uint32 `json:"address"`
	Mnemonic      string `json:"mnemonic"`

	// Kind describes the statement: "assign_reg", "assign_temp", "store_mem",
	// "update_flags", "set_flag", "branch", "jump", "return", "unsupported".
	Kind string `json:"kind"`

	// Operands and effects depending on Kind:
	TargetReg      Register       `json:"target_reg,omitempty"`
	TargetTemp     string         `json:"target_temp,omitempty"`
	TargetFlag     Flag           `json:"target_flag,omitempty"`
	FlagVal        bool           `json:"flag_val,omitempty"`
	Width          Width          `json:"width,omitempty"`
	Expr           Expr           `json:"-"`
	ExprString     string         `json:"expr,omitempty"`
	WordAddressing WordAddressing `json:"word_addressing,omitempty"`
	MemAddress     Expr           `json:"-"`
	MemAddrStr     string         `json:"mem_address,omitempty"`
	Space          string         `json:"space,omitempty"`

	// Flag effects:
	AffectsN bool `json:"affects_n,omitempty"`
	AffectsZ bool `json:"affects_z,omitempty"`
	AffectsC bool `json:"affects_c,omitempty"`
	AffectsV bool `json:"affects_v,omitempty"`
	AffectsI bool `json:"affects_i,omitempty"`
	AffectsD bool `json:"affects_d,omitempty"`

	// Successors:
	Condition       Expr   `json:"-"`
	CondString      string `json:"condition,omitempty"`
	TargetAddr      uint32 `json:"target_addr,omitempty"`
	FallthroughAddr uint32 `json:"fallthrough_addr,omitempty"`

	// Unsupported details:
	Reason string `json:"reason,omitempty"`
}

// BlockIR represents the lowered machine-semantic IR for a basic block.
type BlockIR struct {
	BlockID          string                 `json:"block_id"`
	StartAddress     uint32                 `json:"start_address"`
	EndAddress       uint32                 `json:"end_address"`
	EntryContext     recovery.Context       `json:"entry_context"`
	Instructions     []recovery.Instruction `json:"instructions"`
	Statements       []Statement            `json:"statements"`
	Successors       []uint32               `json:"successors"`
	Assumptions      []string               `json:"assumptions"`
	LoweredCount     int                    `json:"lowered_count"`
	TotalCount       int                    `json:"total_count"`
	UnsupportedCount int                    `json:"unsupported_count"`
}
