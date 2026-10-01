package decomp

import (
	"encoding/json"
	"fmt"
)

// timedExprIdentity records actual typed semantics, never cached display text.
type timedExprIdentity struct {
	Kind                       string         `json:"kind"`
	Width                      Width          `json:"width,omitempty"`
	Value                      uint32         `json:"value,omitempty"`
	Register                   Register       `json:"register,omitempty"`
	Flag                       Flag           `json:"flag,omitempty"`
	Name                       string         `json:"name,omitempty"`
	Binary                     BinaryOp       `json:"binary,omitempty"`
	Unary                      UnaryOp        `json:"unary,omitempty"`
	WordAddressing             WordAddressing `json:"word_addressing,omitempty"`
	Space                      string         `json:"space,omitempty"`
	Left, Right, Expr, Address *timedExprIdentity
}

type timedStatementIdentity struct {
	Metadata                             Statement `json:"metadata"`
	Expression, MemoryAddress, Condition *timedExprIdentity
}
type timedBlockIdentity struct {
	Metadata   BlockIR                  `json:"metadata"`
	Statements []timedStatementIdentity `json:"statements"`
}

func timedSemanticIR(blocks []*BlockIR) ([]byte, error) {
	identity := struct {
		Schema string               `json:"schema"`
		Blocks []timedBlockIdentity `json:"blocks"`
	}{Schema: "timed-machine-ir-v1"}
	for _, block := range blocks {
		if block == nil {
			return nil, fmt.Errorf("timed IR: nil block")
		}
		b := timedBlockIdentity{Metadata: *block}
		b.Metadata.Statements = nil
		for _, s := range block.Statements {
			m := s
			m.ExprString = ""
			m.MemAddrStr = ""
			m.CondString = ""
			var err error
			n := timedStatementIdentity{Metadata: m}
			if n.Expression, err = timedExpression(s.Expr, 0); err != nil {
				return nil, err
			}
			if n.MemoryAddress, err = timedExpression(s.MemAddress, 0); err != nil {
				return nil, err
			}
			if n.Condition, err = timedExpression(s.Condition, 0); err != nil {
				return nil, err
			}
			b.Statements = append(b.Statements, n)
		}
		identity.Blocks = append(identity.Blocks, b)
	}
	b, err := json.Marshal(identity)
	if err != nil {
		return nil, fmt.Errorf("encode timed IR: %w", err)
	}
	return b, nil
}
func timedExpression(e Expr, depth int) (*timedExprIdentity, error) {
	if e == nil {
		return nil, nil
	}
	if depth >= 64 {
		return nil, fmt.Errorf("timed IR: expression depth exceeded")
	}
	n := &timedExprIdentity{}
	var err error
	switch x := e.(type) {
	case *ConstExpr:
		if x == nil {
			return nil, fmt.Errorf("timed IR: nil constant")
		}
		n.Kind = "constant"
		n.Value = x.Value
		n.Width = x.Width
	case *RegExpr:
		if x == nil {
			return nil, fmt.Errorf("timed IR: nil register")
		}
		n.Kind = "register"
		n.Register = x.Reg
		n.Width = x.Width
	case *FlagExpr:
		if x == nil {
			return nil, fmt.Errorf("timed IR: nil flag")
		}
		n.Kind = "flag"
		n.Flag = x.Flag
	case *TempExpr:
		if x == nil {
			return nil, fmt.Errorf("timed IR: nil temporary")
		}
		n.Kind = "temporary"
		n.Name = x.Name
		n.Width = x.Width
	case *BinaryExpr:
		if x == nil {
			return nil, fmt.Errorf("timed IR: nil binary")
		}
		n.Kind = "binary"
		n.Binary = x.Op
		n.Width = x.Width
		n.Left, err = timedExpression(x.Left, depth+1)
		if err != nil {
			return nil, err
		}
		n.Right, err = timedExpression(x.Right, depth+1)
	case *UnaryExpr:
		if x == nil {
			return nil, fmt.Errorf("timed IR: nil unary")
		}
		n.Kind = "unary"
		n.Unary = x.Op
		n.Width = x.Width
		n.Expr, err = timedExpression(x.Expr, depth+1)
	case *MemReadExpr:
		if x == nil {
			return nil, fmt.Errorf("timed IR: nil memory read")
		}
		n.Kind = "memory_read"
		n.WordAddressing = x.WordAddressing
		n.Space = x.Space
		n.Width = x.Width
		n.Address, err = timedExpression(x.Address, depth+1)
	default:
		return nil, fmt.Errorf("timed IR: unsupported expression type %T", e)
	}
	if err != nil {
		return nil, err
	}
	return n, nil
}
