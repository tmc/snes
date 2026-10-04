package decomp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tmc/snes/internal/bus"
	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/structure"
)

// EdgeKind represents the control flow transition type between basic blocks.
type EdgeKind string

const (
	EdgeUnconditional     EdgeKind = "unconditional"
	EdgeBranchTaken       EdgeKind = "branch_taken"
	EdgeBranchFallthrough EdgeKind = "branch_fallthrough"
)

// DAGEdge represents a directed transition in a control-flow graph.
type DAGEdge struct {
	From uint32   `json:"from"`
	To   uint32   `json:"to"`
	Kind EdgeKind `json:"kind"`
}

// RoutineCFG represents a control flow graph of basic blocks and edges.
type RoutineCFG struct {
	EntryAddress uint32                  `json:"entry_address"`
	Blocks       []*structure.BasicBlock `json:"blocks"`
	Edges        []DAGEdge               `json:"edges,omitempty"`
}

// NewRoutineCFG creates a new RoutineCFG rooted at entryAddr.
func NewRoutineCFG(entryAddr uint32) *RoutineCFG {
	return &RoutineCFG{
		EntryAddress: entryAddr,
	}
}

// AddBlock appends a basic block to the CFG.
func (cfg *RoutineCFG) AddBlock(b *structure.BasicBlock) {
	if b != nil {
		cfg.Blocks = append(cfg.Blocks, b)
	}
}

// AddEdge records a directed control-flow edge in the CFG.
func (cfg *RoutineCFG) AddEdge(from, to uint32, kind EdgeKind) {
	cfg.Edges = append(cfg.Edges, DAGEdge{
		From: from,
		To:   to,
		Kind: kind,
	})
}

// Block returns the BasicBlock with the specified start address, or nil if not found.
func (cfg *RoutineCFG) Block(addr uint32) *structure.BasicBlock {
	for _, b := range cfg.Blocks {
		if b != nil && b.StartAddress == addr {
			return b
		}
	}
	return nil
}

// BlockDAG represents a lifted machine-semantic control flow graph of basic blocks.
type BlockDAG struct {
	EntryAddress uint32           `json:"entry_address"`
	EntryContext recovery.Context `json:"entry_context"`
	Blocks       []*BlockIR       `json:"blocks"`
	Edges        []DAGEdge        `json:"edges,omitempty"`
}

// Block returns the BlockIR with the specified start address, or nil if not found.
func (dag *BlockDAG) Block(addr uint32) *BlockIR {
	for _, b := range dag.Blocks {
		if b != nil && b.StartAddress == addr {
			return b
		}
	}
	return nil
}

// LiftDAG lowers the CFG into a BlockDAG, validating and propagating register state across edges.
func LiftDAG(cfg *RoutineCFG, ctx recovery.Context) (*BlockDAG, error) {
	if cfg == nil {
		return nil, fmt.Errorf("lift dag: nil RoutineCFG")
	}
	if len(cfg.Blocks) == 0 {
		return nil, fmt.Errorf("lift dag: CFG contains no basic blocks")
	}

	entryBlock := cfg.Block(cfg.EntryAddress)
	if entryBlock == nil {
		return nil, fmt.Errorf("lift dag: entry block $%06X not found in CFG", cfg.EntryAddress)
	}

	for _, edge := range cfg.Edges {
		if cfg.Block(edge.From) == nil {
			return nil, fmt.Errorf("lift dag: edge source block $%06X not found in CFG", edge.From)
		}
	}

	effEntry, err := ResolveEffectiveContext(entryBlock, ctx)
	if err != nil {
		return nil, fmt.Errorf("lift dag entry context: %w", err)
	}

	// Infer edges if not explicitly provided, or complete missing block edges
	edges := cfg.Edges
	if len(edges) == 0 {
		edges = inferCFGEdges(cfg)
	} else {
		edgeFromMap := make(map[uint32]bool)
		for _, e := range edges {
			edgeFromMap[e.From] = true
		}
		for _, b := range cfg.Blocks {
			if b != nil && !edgeFromMap[b.StartAddress] {
				edges = append(edges, inferBlockEdges(b)...)
			}
		}
	}

	// Propagate contexts across control-flow edges
	entryContexts := make(map[uint32]recovery.Context)
	entryContexts[cfg.EntryAddress] = effEntry

	succEdges := make(map[uint32][]DAGEdge)
	for _, edge := range edges {
		succEdges[edge.From] = append(succEdges[edge.From], edge)
	}

	worklist := []uint32{cfg.EntryAddress}
	visited := make(map[uint32]bool)

	for len(worklist) > 0 {
		currAddr := worklist[0]
		worklist = worklist[1:]
		if visited[currAddr] {
			continue
		}
		visited[currAddr] = true

		currBlock := cfg.Block(currAddr)
		if currBlock == nil {
			continue
		}

		currCtx := entryContexts[currAddr]
		exitCtx, err := propagateContextThroughBlock(currCtx, currBlock)
		if err != nil {
			return nil, fmt.Errorf("lift dag block $%06X context propagation: %w", currAddr, err)
		}

		for _, edge := range succEdges[currAddr] {
			destAddr := edge.To
			destBlock := cfg.Block(destAddr)
			if destBlock == nil {
				continue // External successor
			}

			if prevCtx, exists := entryContexts[destAddr]; exists {
				// Reconcile and assert width agreement
				if prevCtx.M != exitCtx.M {
					return nil, fmt.Errorf("lift dag: context conflict on M at block $%06X: incoming %q vs existing %q", destAddr, exitCtx.M, prevCtx.M)
				}
				if prevCtx.X != exitCtx.X {
					return nil, fmt.Errorf("lift dag: context conflict on X at block $%06X: incoming %q vs existing %q", destAddr, exitCtx.X, prevCtx.X)
				}
				if prevCtx.E != exitCtx.E {
					return nil, fmt.Errorf("lift dag: context conflict on E at block $%06X: incoming %q vs existing %q", destAddr, exitCtx.E, prevCtx.E)
				}
				if prevCtx.C != exitCtx.C {
					prevCtx.C = "unknown"
					entryContexts[destAddr] = prevCtx
				}
			} else {
				entryContexts[destAddr] = exitCtx
				worklist = append(worklist, destAddr)
			}
		}
	}

	var liftedBlocks []*BlockIR
	for _, b := range cfg.Blocks {
		bCtx, ok := entryContexts[b.StartAddress]
		if !ok {
			bCtx = effEntry
		}
		bir, err := LiftBlock(b, bCtx)
		if err != nil {
			return nil, fmt.Errorf("lift dag block $%06X: %w", b.StartAddress, err)
		}
		if len(bir.Successors) == 0 {
			for _, edge := range succEdges[b.StartAddress] {
				bir.Successors = append(bir.Successors, edge.To)
			}
		}
		liftedBlocks = append(liftedBlocks, bir)
	}

	return &BlockDAG{
		EntryAddress: cfg.EntryAddress,
		EntryContext: effEntry,
		Blocks:       liftedBlocks,
		Edges:        edges,
	}, nil
}

func inferCFGEdges(cfg *RoutineCFG) []DAGEdge {
	var edges []DAGEdge
	for _, b := range cfg.Blocks {
		if b == nil || len(b.Instructions) == 0 {
			continue
		}
		edges = append(edges, inferBlockEdges(b)...)
	}
	return edges
}

func inferBlockEdges(b *structure.BasicBlock) []DAGEdge {
	if b == nil || len(b.Instructions) == 0 {
		return nil
	}
	var edges []DAGEdge
	last := b.Instructions[len(b.Instructions)-1]
	rawBytes, _ := hex.DecodeString(last.Bytes)
	op := byte(0)
	if len(rawBytes) > 0 {
		op = rawBytes[0]
	}
	bank := last.Address & 0xFF0000
	fallthroughAddr := bank | uint32(uint16(last.Address)+uint16(len(rawBytes)))

	switch op {
	case 0x60, 0x6B: // RTS, RTL
		// Terminates routine, no internal successor edges

	case 0x80: // BRA
		if len(rawBytes) >= 2 {
			rel := int8(rawBytes[1])
			target := bank | uint32(uint16(int32(uint16(fallthroughAddr))+int32(rel)))
			edges = append(edges, DAGEdge{From: b.StartAddress, To: target, Kind: EdgeUnconditional})
		}

	case 0x82: // BRL
		if len(rawBytes) >= 3 {
			rel16 := int16(uint16(rawBytes[1]) | (uint16(rawBytes[2]) << 8))
			target := bank | uint32(uint16(int32(uint16(fallthroughAddr))+int32(rel16)))
			edges = append(edges, DAGEdge{From: b.StartAddress, To: target, Kind: EdgeUnconditional})
		}

	case 0x4C, 0x5C: // JMP, JML
		if len(rawBytes) >= 3 {
			target := uint32(uint16(rawBytes[1]) | (uint16(rawBytes[2]) << 8))
			if op == 0x5C && len(rawBytes) >= 4 {
				target |= uint32(rawBytes[3]) << 16
			} else {
				target |= bank
			}
			edges = append(edges, DAGEdge{From: b.StartAddress, To: target, Kind: EdgeUnconditional})
		}

	case 0x10, 0x30, 0x50, 0x70, 0x90, 0xB0, 0xD0, 0xF0: // Conditional branches
		if len(rawBytes) >= 2 {
			rel := int8(rawBytes[1])
			target := bank | uint32(uint16(int32(uint16(fallthroughAddr))+int32(rel)))
			edges = append(edges, DAGEdge{From: b.StartAddress, To: target, Kind: EdgeBranchTaken})
			edges = append(edges, DAGEdge{From: b.StartAddress, To: fallthroughAddr, Kind: EdgeBranchFallthrough})
		}

	default:
		for _, succ := range b.Successors {
			edges = append(edges, DAGEdge{From: b.StartAddress, To: succ, Kind: EdgeUnconditional})
		}
	}
	return edges
}

func propagateContextThroughBlock(entry recovery.Context, b *structure.BasicBlock) (recovery.Context, error) {
	eff, err := ResolveEffectiveContext(b, entry)
	if err != nil {
		return recovery.Context{}, err
	}
	curr := eff
	for _, inst := range b.Instructions {
		rawBytes, err := hex.DecodeString(inst.Bytes)
		if err != nil || len(rawBytes) == 0 {
			continue
		}
		op := rawBytes[0]
		switch op {
		case 0xC2: // REP #imm
			if len(rawBytes) >= 2 {
				imm := rawBytes[1]
				if imm&0x20 != 0 {
					curr.M = "clear"
				}
				if imm&0x10 != 0 {
					curr.X = "clear"
				}
			}
		case 0xE2: // SEP #imm
			if len(rawBytes) >= 2 {
				imm := rawBytes[1]
				if imm&0x20 != 0 {
					curr.M = "set"
				}
				if imm&0x10 != 0 {
					curr.X = "set"
				}
			}
		case 0x18: // CLC
			curr.C = "clear"
		case 0x38: // SEC
			curr.C = "set"
		}
	}
	return curr, nil
}

// GenerateCompilableDAG_C renders a self-contained, standard-compliant C implementation of the BlockDAG.
func GenerateCompilableDAG_C(dag *BlockDAG) (string, error) {
	if dag == nil {
		return "", fmt.Errorf("generate DAG C: nil BlockDAG")
	}
	if len(dag.Blocks) == 0 {
		return "", fmt.Errorf("generate DAG C: BlockDAG has no blocks")
	}

	blockSet := make(map[uint32]bool)
	for _, b := range dag.Blocks {
		if b == nil {
			return "", fmt.Errorf("generate DAG C: nil basic block in DAG")
		}
		blockSet[b.StartAddress] = true
	}

	if !blockSet[dag.EntryAddress] {
		return "", fmt.Errorf("generate DAG C: entry block $%06X not found in DAG", dag.EntryAddress)
	}

	edgeMap := make(map[uint32][]DAGEdge)
	for _, edge := range dag.Edges {
		if !blockSet[edge.From] {
			return "", fmt.Errorf("generate DAG C: edge source $%06X is not a block in DAG", edge.From)
		}
		edgeMap[edge.From] = append(edgeMap[edge.From], edge)
	}

	var body strings.Builder
	for _, b := range dag.Blocks {
		body.WriteString(fmt.Sprintf("block_%06x:;\n", b.StartAddress))
		body.WriteString("    {\n")
		body.WriteString("        if (++steps > 100000) {\n")
		body.WriteString("            res.uninitialized_read = true;\n")
		body.WriteString("            goto dag_exit;\n")
		body.WriteString("        }\n")

		hasTerminator := false
		for _, stmt := range b.Statements {
			body.WriteString(fmt.Sprintf("        /* $%06X: %s (%s) */\n", stmt.Address, stmt.Mnemonic, stmt.InstructionID))
			if stmt.Kind == "branch" || stmt.Kind == "jump" || stmt.Kind == "return" {
				hasTerminator = true
			}
			err := lowerCompilableStatement(&body, stmt, "        ", func(s Statement) {
				cond := flagConditionC(s.Condition)
				body.WriteString(fmt.Sprintf("        if (%s) {\n", cond))
				if blockSet[s.TargetAddr] {
					body.WriteString(fmt.Sprintf("            goto block_%06x;\n", s.TargetAddr))
				} else {
					body.WriteString(fmt.Sprintf("            res.has_next = true;\n            res.next_pc = 0x%06X;\n            s.pc = (uint16_t)0x%04X;\n            s.pb = (uint8_t)0x%02X;\n            goto dag_exit;\n",
						s.TargetAddr, s.TargetAddr&0xFFFF, (s.TargetAddr>>16)&0xFF))
				}
				body.WriteString("        } else {\n")
				if blockSet[s.FallthroughAddr] {
					body.WriteString(fmt.Sprintf("            goto block_%06x;\n", s.FallthroughAddr))
				} else {
					body.WriteString(fmt.Sprintf("            res.has_next = true;\n            res.next_pc = 0x%06X;\n            s.pc = (uint16_t)0x%04X;\n            s.pb = (uint8_t)0x%02X;\n            goto dag_exit;\n",
						s.FallthroughAddr, s.FallthroughAddr&0xFFFF, (s.FallthroughAddr>>16)&0xFF))
				}
				body.WriteString("        }\n")
			}, func(s Statement) {
				if blockSet[s.TargetAddr] {
					body.WriteString(fmt.Sprintf("        goto block_%06x;\n", s.TargetAddr))
				} else {
					body.WriteString(fmt.Sprintf("        res.has_next = true;\n        res.next_pc = 0x%06X;\n        s.pc = (uint16_t)0x%04X;\n        s.pb = (uint8_t)0x%02X;\n        goto dag_exit;\n",
						s.TargetAddr, s.TargetAddr&0xFFFF, (s.TargetAddr>>16)&0xFF))
				}
			}, "dag_exit")
			if err != nil {
				return "", fmt.Errorf("block $%06X: %w", b.StartAddress, err)
			}
		}

		if !hasTerminator {
			outEdges := edgeMap[b.StartAddress]
			succs := b.Successors

			if len(outEdges) > 1 {
				return "", fmt.Errorf("generate DAG C: block $%06X has %d outgoing CFG edges without branch instruction", b.StartAddress, len(outEdges))
			}

			var nextTarget uint32
			hasNext := false

			if len(outEdges) == 1 {
				e := outEdges[0]
				if e.Kind != EdgeUnconditional {
					return "", fmt.Errorf("generate DAG C: block $%06X has non-unconditional CFG edge kind %q without branch instruction", b.StartAddress, e.Kind)
				}
				if len(succs) > 1 {
					return "", fmt.Errorf("generate DAG C: block $%06X has %d successors without branch instruction", b.StartAddress, len(succs))
				}
				if len(succs) == 1 && succs[0] != e.To {
					return "", fmt.Errorf("generate DAG C: block $%06X CFG edge to $%06X conflicts with block successor $%06X", b.StartAddress, e.To, succs[0])
				}
				nextTarget = e.To
				hasNext = true
				if len(b.Successors) == 0 {
					b.Successors = []uint32{e.To}
				}
			} else if len(succs) > 0 {
				if len(succs) > 1 {
					return "", fmt.Errorf("generate DAG C: block $%06X has %d successors without branch instruction", b.StartAddress, len(succs))
				}
				nextTarget = succs[0]
				hasNext = true
			}

			if hasNext {
				if blockSet[nextTarget] {
					body.WriteString(fmt.Sprintf("        goto block_%06x;\n", nextTarget))
				} else {
					body.WriteString(fmt.Sprintf("        res.has_next = true;\n        res.next_pc = 0x%06X;\n        s.pc = (uint16_t)0x%04X;\n        s.pb = (uint8_t)0x%02X;\n        goto dag_exit;\n",
						nextTarget, nextTarget&0xFFFF, (nextTarget>>16)&0xFF))
				}
			} else {
				// Terminal basic block with no successors: advance PC and NextPC to block's EndAddress
				endAddr := b.EndAddress
				if endAddr == 0 && len(b.Instructions) > 0 {
					last := b.Instructions[len(b.Instructions)-1]
					rawBytes, _ := hex.DecodeString(last.Bytes)
					endAddr = (last.Address & 0xFF0000) | uint32(uint16(last.Address)+uint16(len(rawBytes)))
				}
				body.WriteString(fmt.Sprintf("        res.has_next = false;\n        res.next_pc = 0x%06X;\n        s.pc = (uint16_t)0x%04X;\n        s.pb = (uint8_t)0x%02X;\n        goto dag_exit;\n",
					endAddr, endAddr&0xFFFF, (endAddr>>16)&0xFF))
			}
		} else {
			// Validate edges for terminating block if explicit edges were supplied
			outEdges := edgeMap[b.StartAddress]
			if len(outEdges) > 0 {
				var branchStmt *Statement
				var jumpStmt *Statement
				var returnStmt *Statement
				for i := range b.Statements {
					s := &b.Statements[i]
					if s.Kind == "branch" {
						branchStmt = s
					} else if s.Kind == "jump" {
						jumpStmt = s
					} else if s.Kind == "return" {
						returnStmt = s
					}
				}
				if returnStmt != nil {
					return "", fmt.Errorf("generate DAG C: block $%06X ends with return but has %d outgoing CFG edges", b.StartAddress, len(outEdges))
				}
				if jumpStmt != nil {
					if len(outEdges) > 1 {
						return "", fmt.Errorf("generate DAG C: block $%06X ends with unconditional jump but has %d outgoing CFG edges", b.StartAddress, len(outEdges))
					}
					if outEdges[0].Kind != EdgeUnconditional {
						return "", fmt.Errorf("generate DAG C: block $%06X jump has non-unconditional edge %q", b.StartAddress, outEdges[0].Kind)
					}
					if outEdges[0].To != jumpStmt.TargetAddr {
						return "", fmt.Errorf("generate DAG C: block $%06X jump edge target $%06X conflicts with statement target $%06X", b.StartAddress, outEdges[0].To, jumpStmt.TargetAddr)
					}
				}
				if branchStmt != nil {
					for _, edge := range outEdges {
						if edge.Kind == EdgeUnconditional {
							return "", fmt.Errorf("generate DAG C: block $%06X ends with conditional branch but has unconditional CFG edge", b.StartAddress)
						}
						if edge.Kind == EdgeBranchTaken && edge.To != branchStmt.TargetAddr {
							return "", fmt.Errorf("generate DAG C: block $%06X branch taken edge target $%06X conflicts with statement target $%06X", b.StartAddress, edge.To, branchStmt.TargetAddr)
						}
						if edge.Kind == EdgeBranchFallthrough && edge.To != branchStmt.FallthroughAddr {
							return "", fmt.Errorf("generate DAG C: block $%06X branch fallthrough edge target $%06X conflicts with statement fallthrough $%06X", b.StartAddress, edge.To, branchStmt.FallthroughAddr)
						}
					}
				}
			}
		}
		body.WriteString("    }\n\n")
	}

	cTemplate := `/* Machine-semantic C translation for BlockDAG at $%06X (%d blocks) */
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
    bool has_next;
    int num_writes;
    bool write_overflow;
    uint32_t total_writes;
    bool uninitialized_read;
    uint32_t uninitialized_addr;
    bool mmio_access;
    uint32_t mmio_addr;
    mem_write_t writes[256];
} exec_result_t;

typedef uint8_t (*mem_read_fn)(void *ctx, uint32_t addr, bool *missing);

__attribute__((unused)) static inline uint32_t bus_canonical_addr(uint32_t addr) {
    uint32_t a = addr & 0xFFFFFF;
    uint8_t bank = (uint8_t)((a >> 16) & 0xFF);
    uint16_t offset = (uint16_t)(a & 0xFFFF);
    /* In banks $00-$3F and $80-$BF, $0000-$1FFF mirrors WRAM $7E0000-$7E1FFF */
    if ((bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) && offset < 0x2000) {
        return 0x7E0000 | offset;
    }
    return a;
}

__attribute__((unused)) static inline bool is_mmio_addr(uint32_t addr) {
    uint32_t a = addr & 0xFFFFFF;
    uint8_t bank = (uint8_t)((a >> 16) & 0xFF);
    uint16_t offset = (uint16_t)(a & 0xFFFF);
    if (bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) {
        if ((offset >= 0x2100 && offset <= 0x21FF) || (offset >= 0x4200 && offset <= 0x43FF)) {
            return true;
        }
    }
    return false;
}

__attribute__((unused)) static inline void mem_write8(exec_result_t *res, uint32_t addr, uint8_t val) {
    uint32_t a = bus_canonical_addr(addr);
    if (is_mmio_addr(addr)) {
        res->mmio_access = true;
        res->mmio_addr = addr;
    }
    res->total_writes++;
    if (res->num_writes < 256) {
        res->writes[res->num_writes].address = a;
        res->writes[res->num_writes].value = val;
        res->num_writes++;
    } else {
        res->write_overflow = true;
    }
}

__attribute__((unused)) static inline void mem_write16(exec_result_t *res, uint32_t addr, uint16_t val) {
    mem_write8(res, addr, (uint8_t)(val & 0xFF));
    mem_write8(res, (addr + 1) & 0xFFFFFF, (uint8_t)((val >> 8) & 0xFF));
}

__attribute__((unused)) static inline void mem_write16_bank0(exec_result_t *res, uint32_t addr, uint16_t val) {
    mem_write8(res, addr & 0xFFFF, (uint8_t)val);
    mem_write8(res, (addr + 1) & 0xFFFF, (uint8_t)(val >> 8));
}

__attribute__((unused)) static inline uint8_t mem_read8_raw(exec_result_t *res, uint32_t addr, mem_read_fn read_cb, void *mem_ctx) {
    uint32_t a = bus_canonical_addr(addr);
    if (is_mmio_addr(addr)) {
        res->mmio_access = true;
        res->mmio_addr = addr;
    }
    for (int i = res->num_writes - 1; i >= 0; i--) {
        if (res->writes[i].address == a) {
            return res->writes[i].value;
        }
    }
    if (read_cb) {
        bool missing = false;
        uint8_t val = read_cb(mem_ctx, a, &missing);
        if (missing) {
            res->uninitialized_read = true;
            res->uninitialized_addr = a;
        }
        return val;
    }
    res->uninitialized_read = true;
    res->uninitialized_addr = a;
    return 0;
}

__attribute__((unused)) static inline uint16_t mem_read16_raw(exec_result_t *res, uint32_t addr, mem_read_fn read_cb, void *mem_ctx) {
    uint8_t low = mem_read8_raw(res, addr, read_cb, mem_ctx);
    uint8_t high = mem_read8_raw(res, (addr + 1) & 0xFFFFFF, read_cb, mem_ctx);
    return (uint16_t)low | ((uint16_t)high << 8);
}

__attribute__((unused)) static inline uint16_t mem_read16_bank0(exec_result_t *res, uint32_t addr, mem_read_fn read_cb, void *mem_ctx) {
    uint8_t low = mem_read8_raw(res, addr & 0xFFFF, read_cb, mem_ctx);
    uint8_t high = mem_read8_raw(res, (addr + 1) & 0xFFFF, read_cb, mem_ctx);
    return (uint16_t)low | ((uint16_t)high << 8);
}

exec_result_t execute_dag_%06x(cpu_state_t init_state, mem_read_fn read_cb, void *mem_ctx) {
    cpu_state_t s = init_state;
    exec_result_t res;
    memset(&res, 0, sizeof(res));

    #define read8(addr) mem_read8_raw(&res, (uint32_t)(addr), read_cb, mem_ctx)
    #define read16(addr) mem_read16_raw(&res, (uint32_t)(addr), read_cb, mem_ctx)
    #define read16_bank0(addr) mem_read16_bank0(&res, (uint32_t)(addr), read_cb, mem_ctx)
    #define P_C ((s.p & 0x01) != 0)
    #define P_Z ((s.p & 0x02) != 0)
    #define P_I ((s.p & 0x04) != 0)
    #define P_D ((s.p & 0x08) != 0)
    #define P_X ((s.p & 0x10) != 0)
    #define P_M ((s.p & 0x20) != 0)
    #define P_V ((s.p & 0x40) != 0)
    #define P_N ((s.p & 0x80) != 0)

    int steps = 0;
    goto block_%06x;

%s
dag_exit:
    #undef read8
    #undef read16
    #undef read16_bank0
    #undef P_C
    #undef P_Z
    #undef P_I
    #undef P_D
    #undef P_X
    #undef P_M
    #undef P_V
    #undef P_N
    if (res.has_next || res.next_pc != 0) {
        s.pc = (uint16_t)(res.next_pc & 0xFFFF);
        s.pb = (uint8_t)((res.next_pc >> 16) & 0xFF);
    }
    res.state = s;
    return res;
}

exec_result_t execute_block_%06x(cpu_state_t init_state, mem_read_fn read_cb, void *mem_ctx) {
    return execute_dag_%06x(init_state, read_cb, mem_ctx);
}
`
	return fmt.Sprintf(cTemplate, dag.EntryAddress, len(dag.Blocks), dag.EntryAddress, dag.EntryAddress, body.String(), dag.EntryAddress, dag.EntryAddress), nil
}

// RunCompiledDAG compiles and executes the BlockDAG C code in an isolated directory.
func RunCompiledDAG(ctx context.Context, dag *BlockDAG, init CPUState, mem map[uint32]uint8) (ExecResult, error) {
	if dag == nil {
		return ExecResult{}, fmt.Errorf("nil BlockDAG")
	}
	if len(dag.Blocks) == 0 {
		return ExecResult{}, fmt.Errorf("empty BlockDAG")
	}

	initAddr := (uint32(init.PB) << 16) | uint32(init.PC)
	if initAddr != dag.EntryAddress {
		return ExecResult{}, fmt.Errorf("entry contract violation: initial CPU PC $%06X does not match DAG entry address $%06X", initAddr, dag.EntryAddress)
	}

	if err := enforceContextContract(dag.EntryContext, init); err != nil {
		return ExecResult{}, fmt.Errorf("entry contract violation: %w", err)
	}

	cCode, err := GenerateCompilableDAG_C(dag)
	if err != nil {
		return ExecResult{}, fmt.Errorf("generate DAG C: %w", err)
	}

	var sortedAddrs []uint32
	for addr := range mem {
		sortedAddrs = append(sortedAddrs, addr)
	}
	sort.Slice(sortedAddrs, func(i, j int) bool { return sortedAddrs[i] < sortedAddrs[j] })

	var memCells bytes.Buffer
	memCount := 0
	seen := make(map[uint32]uint8)
	for _, addr := range sortedAddrs {
		val := mem[addr]
		cAddr := BusCanonicalAddr(addr)
		if prevVal, ok := seen[cAddr]; ok {
			if prevVal != val {
				return ExecResult{}, fmt.Errorf("conflicting initial memory values for canonical address $%06X: 0x%02X vs 0x%02X", cAddr, prevVal, val)
			}
			continue
		}
		seen[cAddr] = val
		fmt.Fprintf(&memCells, "    m.cells[%d].addr = 0x%06X; m.cells[%d].val = 0x%02X;\n", memCount, cAddr, memCount, val)
		memCount++
	}

	runnerSrc := fmt.Sprintf(`%s
#include <unistd.h>

typedef struct {
    uint32_t addr;
    uint8_t val;
} mem_cell_t;

typedef struct {
    mem_cell_t cells[%d];
    int count;
    exec_result_t *res;
} runner_mem_t;

static uint8_t test_read_cb(void *ctx, uint32_t addr, bool *missing) {
    runner_mem_t *m = (runner_mem_t*)ctx;
    uint32_t c_addr = bus_canonical_addr(addr);
    for (int i = 0; i < m->count; i++) {
        if (m->cells[i].addr == c_addr) {
            if (missing) *missing = false;
            return m->cells[i].val;
        }
    }
    if (missing) *missing = true;
    return 0;
}

int main(void) {
    alarm(5);

    runner_mem_t m;
    m.count = %d;
%s
    cpu_state_t init_state = {
        .a = 0x%04X,
        .x = 0x%04X,
        .y = 0x%04X,
        .s = 0x%04X,
        .pc = 0x%04X,
        .d = 0x%04X,
        .db = 0x%02X,
        .pb = 0x%02X,
        .p = 0x%02X,
        .e = %d,
    };

    exec_result_t res = execute_dag_%06x(init_state, test_read_cb, &m);

    printf("{\"state\":{\"a\":%%u,\"x\":%%u,\"y\":%%u,\"s\":%%u,\"pc\":%%u,\"d\":%%u,\"db\":%%u,\"pb\":%%u,\"p\":%%u,\"e\":%%s},"
           "\"next_pc\":%%u,\"total_writes\":%%u,\"write_overflow\":%%s,\"missing_read\":%%s,\"missing_addr\":%%u,\"mmio_access\":%%s,\"mmio_addr\":%%u,\"writes\":[",
           res.state.a, res.state.x, res.state.y, res.state.s, res.state.pc, res.state.d,
           res.state.db, res.state.pb, res.state.p, res.state.e ? "true" : "false",
           res.next_pc, res.total_writes, res.write_overflow ? "true" : "false",
           res.uninitialized_read ? "true" : "false", res.uninitialized_addr,
           res.mmio_access ? "true" : "false", res.mmio_addr);

    for (int i = 0; i < res.num_writes; i++) {
        if (i > 0) printf(",");
        printf("{\"address\":%%u,\"value\":%%u}", res.writes[i].address, res.writes[i].value);
    }
    printf("]}\n");
    return 0;
}
`, cCode, memCount+1, memCount, memCells.String(), init.A, init.X, init.Y, init.S, init.PC, init.D, init.DB, init.PB, init.P, b2i(init.E), dag.EntryAddress)

	home, err := os.UserHomeDir()
	if err != nil {
		return ExecResult{}, fmt.Errorf("user home dir: %w", err)
	}
	baseTmp := filepath.Join(home, "tmp", "snes-auto-jpdasm", "20261003-multiblock")
	if err := os.MkdirAll(baseTmp, 0755); err != nil {
		return ExecResult{}, fmt.Errorf("create base tmp: %w", err)
	}

	var nonce [8]byte
	rand.Read(nonce[:])
	runDir := filepath.Join(baseTmp, fmt.Sprintf("run-%06x-%s", dag.EntryAddress, hex.EncodeToString(nonce[:])))
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return ExecResult{}, fmt.Errorf("create isolated run dir: %w", err)
	}
	defer os.RemoveAll(runDir)

	srcFile := filepath.Join(runDir, "runner.c")
	binFile := filepath.Join(runDir, "runner")

	if err := os.WriteFile(srcFile, []byte(runnerSrc), 0644); err != nil {
		return ExecResult{}, fmt.Errorf("write runner src: %w", err)
	}

	compileCtx, cancelCompile := context.WithTimeout(ctx, 10*time.Second)
	defer cancelCompile()
	cmdCompile := exec.CommandContext(compileCtx, "cc", "-O0", "-Wall", "-Werror", "-Wno-unused-function", "-Wno-unused-label", srcFile, "-o", binFile)
	if out, err := cmdCompile.CombinedOutput(); err != nil {
		return ExecResult{}, fmt.Errorf("compile DAG C failed: %w (output: %s)", err, string(out))
	}

	runCtx, cancelRun := context.WithTimeout(ctx, 5*time.Second)
	defer cancelRun()
	cmdRun := exec.CommandContext(runCtx, binFile)
	out, err := cmdRun.CombinedOutput()
	if err != nil {
		return ExecResult{}, fmt.Errorf("run DAG C executable failed: %w (output: %s)", err, string(out))
	}

	var res ExecResult
	if err := json.Unmarshal(out, &res); err != nil {
		return ExecResult{}, fmt.Errorf("unmarshal DAG C output %q: %w", string(out), err)
	}

	if res.MMIOAccess {
		return ExecResult{}, fmt.Errorf("unsupported MMIO access to address $%06X", res.MMIOAddr)
	}
	if res.MissingRead {
		return ExecResult{}, fmt.Errorf("read from uninitialized memory address $%06X", res.MissingAddr)
	}

	return res, nil
}

// RunEmulatorDAG executes the BlockDAG on the reference Go 65816 CPU emulator across multi-block boundaries.
func RunEmulatorDAG(ctx context.Context, dag *BlockDAG, init CPUState, mem map[uint32]uint8) (ExecResult, error) {
	if err := ctx.Err(); err != nil {
		return ExecResult{}, err
	}
	if dag == nil {
		return ExecResult{}, fmt.Errorf("nil BlockDAG")
	}
	if len(dag.Blocks) == 0 {
		return ExecResult{}, fmt.Errorf("empty BlockDAG")
	}

	initAddr := (uint32(init.PB) << 16) | uint32(init.PC)
	if initAddr != dag.EntryAddress {
		return ExecResult{}, fmt.Errorf("entry contract violation: initial CPU PC $%06X does not match DAG entry address $%06X", initAddr, dag.EntryAddress)
	}

	if err := enforceContextContract(dag.EntryContext, init); err != nil {
		return ExecResult{}, fmt.Errorf("entry contract violation: %w", err)
	}

	b := bus.NewBus()

	// 1. Map WRAM $7E0000-$7FFFFF
	wram := bus.NewWRAMDevice()
	b.Map(0x7E0000, 0x7FFFFF, wram)

	// 2. Map mirror Low RAM $0000-$1FFF
	for bank := uint32(0x00); bank <= 0x3F; bank++ {
		b.Map(bank<<16, (bank<<16)|0x1FFF, wram)
	}
	for bank := uint32(0x80); bank <= 0xBF; bank++ {
		b.Map(bank<<16, (bank<<16)|0x1FFF, wram)
	}

	// 3. Map bank 0 upper range $002000-$00FFFF
	b.Map(0x002000, 0x00FFFF, bus.NewRAMDevice(64*1024))

	// 4. Map referenced banks
	referencedBanks := make(map[uint8]bool)
	referencedBanks[0x00] = true
	referencedBanks[init.PB] = true
	for _, blk := range dag.Blocks {
		for _, inst := range blk.Instructions {
			referencedBanks[uint8(inst.Address>>16)] = true
		}
	}
	for addr := range mem {
		cAddr := BusCanonicalAddr(addr)
		referencedBanks[uint8(cAddr>>16)] = true
		referencedBanks[uint8(addr>>16)] = true
	}

	for bank := range referencedBanks {
		if bank == 0x00 || bank == 0x7E || bank == 0x7F {
			continue
		}
		bankBase := uint32(bank) << 16
		bankDev := bus.NewRAMDevice(64 * 1024)
		if bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF) {
			b.Map(bankBase|0x2000, bankBase|0xFFFF, bankDev)
		} else {
			b.Map(bankBase, bankBase|0xFFFF, bankDev)
		}
	}

	// 5. Populate initial memory
	initializedMem := make(map[uint32]bool)
	var sortedAddrs []uint32
	for addr := range mem {
		sortedAddrs = append(sortedAddrs, addr)
	}
	sort.Slice(sortedAddrs, func(i, j int) bool { return sortedAddrs[i] < sortedAddrs[j] })

	seenCanonical := make(map[uint32]struct {
		addr uint32
		val  uint8
	})

	for _, addr := range sortedAddrs {
		val := mem[addr]
		if IsMMIOAddr(addr) {
			return ExecResult{}, fmt.Errorf("unsupported MMIO access to address $%06X in initial memory", addr)
		}
		cAddr := BusCanonicalAddr(addr)
		if prev, exists := seenCanonical[cAddr]; exists {
			if prev.val != val {
				return ExecResult{}, fmt.Errorf("conflicting initial memory values for canonical address $%06X: $%06X has 0x%02X, $%06X has 0x%02X", cAddr, prev.addr, prev.val, addr, val)
			}
		} else {
			seenCanonical[cAddr] = struct {
				addr uint32
				val  uint8
			}{addr: addr, val: val}
		}
		b.Write(cAddr, val)
		initializedMem[cAddr] = true
		initializedMem[addr] = true
	}

	// 6. Populate instructions from all blocks
	dagInsnAddrs := make(map[uint32]bool)
	instByteAddrs := make(map[uint32]bool)
	for _, blk := range dag.Blocks {
		for _, inst := range blk.Instructions {
			dagInsnAddrs[inst.Address] = true
			rawBytes, err := decodeHexBytes(inst.Bytes)
			if err != nil {
				return ExecResult{}, fmt.Errorf("decode instruction bytes at $%06X: %w", inst.Address, err)
			}
			for offset, byteVal := range rawBytes {
				instAddr := inst.Address + uint32(offset)
				b.Write(instAddr, byteVal)
				initializedMem[instAddr] = true
				initializedMem[BusCanonicalAddr(instAddr)] = true
				instByteAddrs[instAddr] = true
				instByteAddrs[BusCanonicalAddr(instAddr)] = true
			}
		}
	}

	var (
		recordedWrites []MemoryWrite
		totalWrites    uint32
		writeOverflow  bool
		missingRead    bool
		missingAddr    uint32
		mmioAccess     bool
		mmioAddr       uint32
		writtenAddrs   = make(map[uint32]bool)
	)

	b.WriteHook = func(address uint32, value uint8) {
		if IsMMIOAddr(address) {
			mmioAccess = true
			mmioAddr = address
		}
		totalWrites++
		cAddr := BusCanonicalAddr(address)
		writtenAddrs[cAddr] = true
		writtenAddrs[address] = true
		mw := MemoryWrite{
			Address: cAddr,
			Value:   value,
		}
		if len(recordedWrites) < 256 {
			recordedWrites = append(recordedWrites, mw)
		} else {
			writeOverflow = true
		}
	}

	b.ReadHook = func(address uint32, value uint8) {
		if IsMMIOAddr(address) {
			mmioAccess = true
			mmioAddr = address
		}
		cAddr := BusCanonicalAddr(address)
		if !initializedMem[address] && !initializedMem[cAddr] && !writtenAddrs[address] && !writtenAddrs[cAddr] {
			missingRead = true
			missingAddr = address
		}
	}

	c := cpu.NewCPU(b)
	c.A = init.A
	c.X = init.X
	c.Y = init.Y
	c.S = init.S
	c.D = init.D
	c.DB = init.DB
	c.PB = init.PB
	c.PC = init.PC
	c.P = init.P
	c.E = init.E

	maxSteps := 100000
	steps := 0
	for {
		if err := ctx.Err(); err != nil {
			return ExecResult{}, fmt.Errorf("emulator execution timed out or cancelled: %w", err)
		}
		curPC := (uint32(c.PB) << 16) | uint32(c.PC)
		if !dagInsnAddrs[curPC] {
			// Stepped outside DAG boundaries
			break
		}
		if steps >= maxSteps {
			return ExecResult{}, fmt.Errorf("emulator step limit (%d) exceeded in DAG", maxSteps)
		}
		steps++
		c.Step()
	}

	if mmioAccess {
		return ExecResult{}, fmt.Errorf("unsupported MMIO access to address $%06X", mmioAddr)
	}
	if missingRead {
		return ExecResult{}, fmt.Errorf("read from uninitialized memory address $%06X", missingAddr)
	}

	nextPC := (uint32(c.PB) << 16) | uint32(c.PC)
	finalState := CPUState{
		A:  c.A,
		X:  c.X,
		Y:  c.Y,
		S:  c.S,
		D:  c.D,
		DB: c.DB,
		PB: c.PB,
		P:  c.P,
		E:  c.E,
		PC: c.PC,
	}

	return ExecResult{
		State:         finalState,
		NextPC:        nextPC,
		Writes:        recordedWrites,
		TotalWrites:   totalWrites,
		WriteOverflow: writeOverflow,
	}, nil
}

// RunDAG executes the DAG on both the compiled C runner and the Go 65816 CPU emulator,
// and asserts complete parity across final PC, CPU state, and ordered memory writes.
func RunDAG(ctx context.Context, dag *BlockDAG, init CPUState, mem map[uint32]uint8) (ExecResult, ExecResult, error) {
	cRes, cErr := RunCompiledDAG(ctx, dag, init, mem)
	if cErr != nil {
		return ExecResult{}, ExecResult{}, fmt.Errorf("compiled DAG runner error: %w", cErr)
	}

	emuRes, emuErr := RunEmulatorDAG(ctx, dag, init, mem)
	if emuErr != nil {
		return cRes, ExecResult{}, fmt.Errorf("emulator DAG runner error: %w", emuErr)
	}

	matched, discrepancy := CompareExecResults(emuRes, cRes)
	if !matched {
		return cRes, emuRes, fmt.Errorf("dual-backend discrepancy: %s", discrepancy)
	}

	return cRes, emuRes, nil
}

// NewCompiledDAGRunner compiles the BlockDAG into an isolated, reusable multi-case runner executable.
func NewCompiledDAGRunner(ctx context.Context, dag *BlockDAG) (*CompiledRunner, error) {
	if dag == nil {
		return nil, fmt.Errorf("nil BlockDAG")
	}

	cCode, err := GenerateCompilableDAG_C(dag)
	if err != nil {
		return nil, fmt.Errorf("generate compilable DAG C: %w", err)
	}
	cHash := ComputeCHash(cCode)

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("user home dir: %w", err)
	}
	baseTmp := filepath.Join(home, "tmp", "snes-auto-jpdasm", "20261003-multiblock")
	if err := os.MkdirAll(baseTmp, 0755); err != nil {
		return nil, fmt.Errorf("create base tmp: %w", err)
	}

	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("rand nonce: %w", err)
	}
	runDir := filepath.Join(baseTmp, fmt.Sprintf("runner-%06x-%s", dag.EntryAddress, hex.EncodeToString(nonce[:])))
	if err := os.MkdirAll(runDir, 0700); err != nil {
		return nil, fmt.Errorf("create runner dir: %w", err)
	}

	srcPath := filepath.Join(runDir, "runner.c")
	binPath := filepath.Join(runDir, "runner")

	runnerSrc := GenerateMultiCaseRunnerC(cCode, dag.EntryAddress)
	if err := os.WriteFile(srcPath, []byte(runnerSrc), 0644); err != nil {
		os.RemoveAll(runDir)
		return nil, fmt.Errorf("write runner src: %w", err)
	}

	compileCtx, cancelCompile := context.WithTimeout(ctx, 10*time.Second)
	defer cancelCompile()

	compilerFlags := "cc -O0 -Wall -Werror -Wno-unused-function -Wno-unused-label"
	cmdCompile := exec.CommandContext(compileCtx, "cc", "-O0", "-Wall", "-Werror", "-Wno-unused-function", "-Wno-unused-label", srcPath, "-o", binPath)
	if out, err := cmdCompile.CombinedOutput(); err != nil {
		os.RemoveAll(runDir)
		return nil, fmt.Errorf("compile DAG runner: %w (output: %s)", err, string(out))
	}

	return &CompiledRunner{
		BlockID:        fmt.Sprintf("dag-%06x", dag.EntryAddress),
		StartAddress:   dag.EntryAddress,
		GeneratedCHash: cHash,
		Context:        dag.EntryContext,
		Compiler:       getObservedCompiler(),
		CompilerFlags:  compilerFlags,
		RunDir:         runDir,
		BinPath:        binPath,
	}, nil
}
