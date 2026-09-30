package decomp

import (
	"fmt"
	"strings"
)

// SourceMapEntry maps a line in generated pseudo-C to an instruction.
type SourceMapEntry struct {
	Line          int    `json:"line"`
	InstructionID string `json:"instruction_id"`
	Address       uint32 `json:"address"`
	Mnemonic      string `json:"mnemonic"`
}

// GeneratePseudoC renders clean, readable pseudo-C with source map annotations.
func GeneratePseudoC(ir *BlockIR) (string, []SourceMapEntry) {
	if ir == nil {
		return "", nil
	}

	var sb strings.Builder
	var sm []SourceMapEntry
	lineNum := 1

	addLine := func(s string, instID string, addr uint32, mnem string) {
		sb.WriteString(s)
		sb.WriteByte('\n')
		if instID != "" {
			sm = append(sm, SourceMapEntry{
				Line:          lineNum,
				InstructionID: instID,
				Address:       addr,
				Mnemonic:      mnem,
			})
		}
		lineNum++
	}

	addLine(fmt.Sprintf("// Basic Block %s: $%06X - $%06X", ir.BlockID, ir.StartAddress, ir.EndAddress), "", 0, "")
	addLine(fmt.Sprintf("// Entry Context: [E:%s M:%s X:%s C:%s]", ir.EntryContext.E, ir.EntryContext.M, ir.EntryContext.X, ir.EntryContext.C), "", 0, "")
	if len(ir.Assumptions) > 0 {
		for _, a := range ir.Assumptions {
			addLine("// Assumption: "+a, "", 0, "")
		}
	}
	addLine(fmt.Sprintf("void block_%06x(void) {", ir.StartAddress), "", 0, "")

	currInstID := ""
	for _, stmt := range ir.Statements {
		if stmt.InstructionID != currInstID {
			currInstID = stmt.InstructionID
			addLine(fmt.Sprintf("    // $%06X: %s (%s)", stmt.Address, stmt.Mnemonic, stmt.InstructionID), "", 0, "")
		}

		cCode := renderStatementPseudoC(stmt)
		if cCode != "" {
			addLine("    "+cCode, stmt.InstructionID, stmt.Address, stmt.Mnemonic)
		}
	}

	addLine("}", "", 0, "")
	return sb.String(), sm
}

func renderStatementPseudoC(s Statement) string {
	switch s.Kind {
	case "assign_reg":
		if s.TargetReg == RegA && s.Width == Width8 {
			return fmt.Sprintf("A = (A & 0xFF00) | (%s & 0xFF);", s.ExprString)
		}
		if s.Width == Width8 {
			return fmt.Sprintf("%s = %s & 0xFF;", s.TargetReg, s.ExprString)
		}
		return fmt.Sprintf("%s = %s & 0xFFFF;", s.TargetReg, s.ExprString)

	case "store_mem":
		if s.Width == Width8 {
			return fmt.Sprintf("write8(%s, %s);", s.MemAddrStr, s.ExprString)
		}
		return fmt.Sprintf("write16(%s, %s);", s.MemAddrStr, s.ExprString)

	case "update_flags":
		var parts []string
		if s.AffectsZ {
			parts = append(parts, fmt.Sprintf("P.Z = ((%s) == 0)", s.ExprString))
		}
		if s.AffectsN {
			if s.Width == Width8 {
				parts = append(parts, fmt.Sprintf("P.N = (((%s) & 0x80) != 0)", s.ExprString))
			} else {
				parts = append(parts, fmt.Sprintf("P.N = (((%s) & 0x8000) != 0)", s.ExprString))
			}
		}
		if s.AffectsC {
			parts = append(parts, "P.C = (carry)")
		}
		if s.AffectsV {
			parts = append(parts, "P.V = (overflow)")
		}
		if len(parts) > 0 {
			return strings.Join(parts, "; ") + ";"
		}
		return ""

	case "set_flag":
		valStr := "0"
		if s.FlagVal {
			valStr = "1"
		}
		return fmt.Sprintf("P.%s = %s;", s.TargetFlag, valStr)

	case "branch":
		return fmt.Sprintf("if (%s) goto loc_%06x; else goto loc_%06x;", s.CondString, s.TargetAddr, s.FallthroughAddr)

	case "jump":
		return fmt.Sprintf("goto loc_%06x;", s.TargetAddr)

	case "return":
		return fmt.Sprintf("return /* %s */;", s.TargetTemp)

	case "nop":
		return "/* nop */;"

	case "unsupported":
		return fmt.Sprintf("/* UNSUPPORTED: %s */", s.Reason)

	default:
		return fmt.Sprintf("/* %s */", s.Kind)
	}
}

// GenerateCompilableC renders a self-contained, standard-compliant C implementation of the block.
func GenerateCompilableC(ir *BlockIR) (string, error) {
	if ir == nil {
		return "", fmt.Errorf("generate C: nil BlockIR")
	}

	var body strings.Builder

	for _, stmt := range ir.Statements {
		body.WriteString(fmt.Sprintf("    /* $%06X: %s (%s) */\n", stmt.Address, stmt.Mnemonic, stmt.InstructionID))
		switch stmt.Kind {
		case "assign_reg":
			reg := strings.ToLower(string(stmt.TargetReg))
			cExpr := exprToCompilableC(stmt.Expr, stmt.Width)
			if stmt.TargetReg == RegA && stmt.Width == Width8 {
				body.WriteString(fmt.Sprintf("    s.a = (s.a & 0xFF00) | ((%s) & 0xFF);\n", cExpr))
			} else if stmt.Width == Width8 {
				body.WriteString(fmt.Sprintf("    s.%s = (%s) & 0xFF;\n", reg, cExpr))
			} else {
				body.WriteString(fmt.Sprintf("    s.%s = (%s) & 0xFFFF;\n", reg, cExpr))
			}

		case "store_mem":
			addrExpr := exprToCompilableC(stmt.MemAddress, Width24)
			valExpr := exprToCompilableC(stmt.Expr, stmt.Width)
			if stmt.Width == Width8 {
				body.WriteString(fmt.Sprintf("    mem_write8(&res, %s, (%s) & 0xFF);\n", addrExpr, valExpr))
			} else {
				body.WriteString(fmt.Sprintf("    mem_write16(&res, %s, (%s) & 0xFFFF);\n", addrExpr, valExpr))
			}

		case "update_flags":
			exprStr := exprToCompilableC(stmt.Expr, stmt.Width)
			if stmt.Width == Width8 {
				body.WriteString(fmt.Sprintf("    {\n        uint8_t _v = (uint8_t)(%s);\n", exprStr))
				if stmt.AffectsZ {
					body.WriteString("        if (_v == 0) s.p |= 0x02; else s.p &= ~0x02;\n")
				}
				if stmt.AffectsN {
					body.WriteString("        if (_v & 0x80) s.p |= 0x80; else s.p &= ~0x80;\n")
				}
				if stmt.AffectsC {
					if bin, ok := stmt.Expr.(*BinaryExpr); ok && bin.Op == OpSub {
						leftStr := exprToCompilableC(bin.Left, stmt.Width)
						rightStr := exprToCompilableC(bin.Right, stmt.Width)
						body.WriteString(fmt.Sprintf("        if ((uint8_t)(%s) >= (uint8_t)(%s)) s.p |= 0x01; else s.p &= ~0x01;\n", leftStr, rightStr))
					}
				}
				body.WriteString("    }\n")
			} else {
				body.WriteString(fmt.Sprintf("    {\n        uint16_t _v = (uint16_t)(%s);\n", exprStr))
				if stmt.AffectsZ {
					body.WriteString("        if (_v == 0) s.p |= 0x02; else s.p &= ~0x02;\n")
				}
				if stmt.AffectsN {
					body.WriteString("        if (_v & 0x8000) s.p |= 0x80; else s.p &= ~0x80;\n")
				}
				if stmt.AffectsC {
					if bin, ok := stmt.Expr.(*BinaryExpr); ok && bin.Op == OpSub {
						leftStr := exprToCompilableC(bin.Left, stmt.Width)
						rightStr := exprToCompilableC(bin.Right, stmt.Width)
						body.WriteString(fmt.Sprintf("        if ((uint16_t)(%s) >= (uint16_t)(%s)) s.p |= 0x01; else s.p &= ~0x01;\n", leftStr, rightStr))
					}
				}
				body.WriteString("    }\n")
			}

		case "set_flag":
			mask := flagMask(stmt.TargetFlag)
			if stmt.FlagVal {
				body.WriteString(fmt.Sprintf("    s.p |= 0x%02X; /* set %s */\n", mask, stmt.TargetFlag))
			} else {
				body.WriteString(fmt.Sprintf("    s.p &= ~0x%02X; /* clear %s */\n", mask, stmt.TargetFlag))
			}

		case "branch":
			cond := flagConditionC(stmt.Condition)
			body.WriteString(fmt.Sprintf("    if (%s) {\n", cond))
			body.WriteString(fmt.Sprintf("        res.next_pc = 0x%06X;\n", stmt.TargetAddr))
			body.WriteString("        goto block_exit;\n")
			body.WriteString("    } else {\n")
			body.WriteString(fmt.Sprintf("        res.next_pc = 0x%06X;\n", stmt.FallthroughAddr))
			body.WriteString("        goto block_exit;\n")
			body.WriteString("    }\n")

		case "jump":
			body.WriteString(fmt.Sprintf("    res.next_pc = 0x%06X;\n", stmt.TargetAddr))
			body.WriteString("    goto block_exit;\n")

		case "return":
			body.WriteString("    res.next_pc = 0; /* return */\n")
			body.WriteString("    goto block_exit;\n")

		case "nop":
			body.WriteString("    /* nop */;\n")

		case "unsupported":
			return "", fmt.Errorf("cannot generate compilable C with unsupported instruction: %s", stmt.Reason)
		}
	}

	// Default fallthrough if no terminator
	if len(ir.Successors) > 0 {
		body.WriteString(fmt.Sprintf("    res.next_pc = 0x%06X;\n", ir.Successors[0]))
	}

	cTemplate := `/* Machine-semantic C translation for block %s ($%06X-$%06X) */
#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include <stdio.h>

typedef struct {
    uint16_t a;
    uint16_t x;
    uint16_t y;
    uint16_t s;
    uint16_t pc;
    uint16_t d;
    uint8_t db;
    uint8_t pb;
    uint8_t p;
    bool e;
} cpu_state_t;

typedef struct {
    uint32_t address;
    uint8_t value;
} mem_write_t;

typedef struct {
    cpu_state_t state;
    uint32_t next_pc;
    int num_writes;
    mem_write_t writes[256];
} exec_result_t;

typedef uint8_t (*mem_read_fn)(void *ctx, uint32_t addr);

__attribute__((unused)) static inline void mem_write8(exec_result_t *res, uint32_t addr, uint8_t val) {
    if (res->num_writes < 256) {
        res->writes[res->num_writes].address = addr & 0xFFFFFF;
        res->writes[res->num_writes].value = val;
        res->num_writes++;
    }
}

__attribute__((unused)) static inline void mem_write16(exec_result_t *res, uint32_t addr, uint16_t val) {
    mem_write8(res, addr, (uint8_t)(val & 0xFF));
    mem_write8(res, (addr + 1) & 0xFFFFFF, (uint8_t)((val >> 8) & 0xFF));
}

__attribute__((unused)) static inline uint8_t mem_read8_raw(exec_result_t *res, uint32_t addr, mem_read_fn read_cb, void *mem_ctx) {
    uint32_t a = addr & 0xFFFFFF;
    for (int i = res->num_writes - 1; i >= 0; i--) {
        if (res->writes[i].address == a) {
            return res->writes[i].value;
        }
    }
    if (read_cb) return read_cb(mem_ctx, a);
    return 0;
}

__attribute__((unused)) static inline uint16_t mem_read16_raw(exec_result_t *res, uint32_t addr, mem_read_fn read_cb, void *mem_ctx) {
    uint8_t low = mem_read8_raw(res, addr, read_cb, mem_ctx);
    uint8_t high = mem_read8_raw(res, (addr + 1) & 0xFFFFFF, read_cb, mem_ctx);
    return (uint16_t)low | ((uint16_t)high << 8);
}

exec_result_t execute_block_%06x(cpu_state_t init_state, mem_read_fn read_cb, void *mem_ctx) {
    cpu_state_t s = init_state;
    exec_result_t res;
    memset(&res, 0, sizeof(res));

    /* Helper macros for memory read with read-after-write support */
    #define read8(addr) mem_read8_raw(&res, (uint32_t)(addr), read_cb, mem_ctx)
    #define read16(addr) mem_read16_raw(&res, (uint32_t)(addr), read_cb, mem_ctx)
    #define P_C ((s.p & 0x01) != 0)
    #define P_Z ((s.p & 0x02) != 0)
    #define P_I ((s.p & 0x04) != 0)
    #define P_D ((s.p & 0x08) != 0)
    #define P_X ((s.p & 0x10) != 0)
    #define P_M ((s.p & 0x20) != 0)
    #define P_V ((s.p & 0x40) != 0)
    #define P_N ((s.p & 0x80) != 0)

%s

block_exit:
    #undef read8
    #undef read16
    #undef P_C
    #undef P_Z
    #undef P_I
    #undef P_D
    #undef P_X
    #undef P_M
    #undef P_V
    #undef P_N
    res.state = s;
    return res;
}
`
	return fmt.Sprintf(cTemplate, ir.BlockID, ir.StartAddress, ir.EndAddress, ir.StartAddress, body.String()), nil
}

func exprToCompilableC(e Expr, w Width) string {
	if e == nil {
		return "0"
	}
	switch ex := e.(type) {
	case *ConstExpr:
		return fmt.Sprintf("0x%X", ex.Value)
	case *RegExpr:
		reg := strings.ToLower(string(ex.Reg))
		if ex.Reg == RegA && ex.Width == Width8 {
			return "(s.a & 0xFF)"
		}
		return "s." + reg
	case *FlagExpr:
		return "P_" + string(ex.Flag)
	case *TempExpr:
		return ex.Name
	case *BinaryExpr:
		left := exprToCompilableC(ex.Left, ex.Width)
		right := exprToCompilableC(ex.Right, ex.Width)
		return fmt.Sprintf("(%s %s %s)", left, ex.Op, right)
	case *UnaryExpr:
		inner := exprToCompilableC(ex.Expr, ex.Width)
		return fmt.Sprintf("(%s%s)", ex.Op, inner)
	case *MemReadExpr:
		addr := exprToCompilableC(ex.Address, Width24)
		if ex.Width == Width8 {
			return fmt.Sprintf("read8(%s)", addr)
		}
		return fmt.Sprintf("read16(%s)", addr)
	default:
		return ex.String()
	}
}

func flagMask(f Flag) uint8 {
	switch f {
	case FlagC:
		return 0x01
	case FlagZ:
		return 0x02
	case FlagI:
		return 0x04
	case FlagD:
		return 0x08
	case FlagX:
		return 0x10
	case FlagM:
		return 0x20
	case FlagV:
		return 0x40
	case FlagN:
		return 0x80
	default:
		return 0
	}
}

func flagConditionC(cond Expr) string {
	if cond == nil {
		return "true"
	}
	switch c := cond.(type) {
	case *BinaryExpr:
		if flagEx, ok := c.Left.(*FlagExpr); ok {
			if constEx, ok := c.Right.(*ConstExpr); ok {
				if constEx.Value == 0 && c.Op == OpEqual {
					return "!P_" + string(flagEx.Flag)
				}
				if constEx.Value == 1 && c.Op == OpEqual {
					return "P_" + string(flagEx.Flag)
				}
			}
		}
	}
	return cond.String()
}
