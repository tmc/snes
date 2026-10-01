package decomp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/structure"
)

// TimedInstruction describes reviewed bus scheduling independently of semantics.
// Operand fetches follow IdleBefore; data operations are emitted by machine IR.
// TakenIdle applies when the generated successor differs from fallthrough.
type TimedInstruction struct {
	Opcode                           byte `json:"opcode"`
	OperandBytes                     int  `json:"operand_bytes"`
	IdleBefore, IdleAfter, TakenIdle int
}

// TimedPlan is an explicitly reviewed native instruction scheduling vocabulary.
// It does not establish cycle accuracy by its identity alone.
type TimedPlan struct {
	Instructions []TimedInstruction `json:"instructions"`
}

// TimedImmediateEdit replaces one pinned 8-bit immediate in the lifted IR.
// Runtime operand fetches still consume and verify the original ROM bytes.
type TimedImmediateEdit struct {
	Address               uint32 `json:"address"`
	Expected, Replacement byte
}

// TimedSource binds generated semantics and a separate timing plan.
// It grants no captured recovery eligibility.
type TimedSource struct {
	Source                                                                    string `json:"-"`
	SourceSHA256, IRSHA256, EditedIRSHA256, ROMSHA256, PlanSHA256, EditSHA256 string
	Instructions                                                              []uint32
}

// GenerateTimedRegionC emits a synchronous F/R/W/I/D instruction driver using
// the same statement lowering as GenerateCompilableC. Only native binary 8-bit
// absolute low-WRAM, immediate arithmetic, branch, flag and RTS forms are supported.
// The runtime supplies original operand bytes and services all timed requests.
func GenerateTimedRegionC(region *RegionIR, rom []byte, plan TimedPlan, edit *TimedImmediateEdit) (TimedSource, error) {
	var result TimedSource
	if region == nil || len(rom) == 0 || region.EntryContext.E != "clear" || region.EntryContext.M != "set" || region.EntryContext.X != "set" {
		return result, fmt.Errorf("timed C: unsupported entry context")
	}
	schedules := map[byte]TimedInstruction{}
	for _, p := range plan.Instructions {
		if _, ok := schedules[p.Opcode]; ok {
			return result, fmt.Errorf("timed C: duplicate timing opcode")
		}
		if p.OperandBytes < 0 || p.OperandBytes > 3 || p.IdleBefore < 0 || p.IdleAfter < 0 || p.TakenIdle < 0 || p.IdleBefore > 24 || p.IdleAfter > 24 || p.TakenIdle > 12 {
			return result, fmt.Errorf("timed C: invalid timing bounds")
		}
		want, ok := reviewedTimedSchedule[p.Opcode]
		if !ok || p != want {
			return result, fmt.Errorf("timed C: unsupported instruction schedule")
		}
		schedules[p.Opcode] = p
	}
	var blocks []*BlockIR
	seen := map[uint32]bool{}
	for _, block := range region.Blocks {
		if block == nil {
			return result, fmt.Errorf("timed C: nil block")
		}
		for _, in := range block.Instructions {
			if seen[in.Address] {
				return result, fmt.Errorf("timed C: duplicate instruction")
			}
			seen[in.Address] = true
			raw, err := hex.DecodeString(in.Bytes)
			if err != nil || len(raw) == 0 {
				return result, fmt.Errorf("timed C: invalid instruction bytes")
			}
			p, ok := schedules[in.Opcode]
			if !ok || len(raw) != 1+p.OperandBytes || raw[0] != in.Opcode {
				return result, fmt.Errorf("timed C: unsupported timing at $%06x", in.Address)
			}
			switch in.Opcode {
			case 0xee, 0xad, 0x8d:
				if len(raw) != 3 || (uint16(raw[1])|uint16(raw[2])<<8) >= 0x2000 {
					return result, fmt.Errorf("timed C: unsupported memory operand")
				}
			case 0xc9, 0x69, 0xd0, 0x18, 0x60:
			default:
				return result, fmt.Errorf("timed C: unsupported opcode $%02x", in.Opcode)
			}
			if in.Context.E != "clear" || in.Context.M != "set" || in.Context.X != "set" {
				return result, fmt.Errorf("timed C: unresolved instruction width")
			}
			for k, b := range raw {
				addr := (in.Address & 0xff0000) | uint32(uint16(in.Address+uint32(k)))
				off, ok := timedROMOffset(addr, len(rom))
				if !ok || rom[off] != b {
					return result, fmt.Errorf("timed C: instruction differs from pinned ROM")
				}
			}
			next := (in.Address & 0xff0000) | uint32(uint16(in.Address+uint32(len(raw))))
			b, err := LiftBlock(&structure.BasicBlock{ID: fmt.Sprintf("timed-%06x", in.Address), StartAddress: in.Address, EndAddress: in.Address + uint32(len(raw)), Instructions: []recovery.Instruction{in}, Successors: []uint32{next}}, in.Context)
			if err != nil {
				return result, err
			}
			blocks = append(blocks, b)
		}
	}
	if len(blocks) == 0 || len(blocks) > 64 {
		return result, fmt.Errorf("timed C: invalid instruction count")
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].StartAddress < blocks[j].StartAddress })
	original, _ := json.Marshal(blocks)
	result.IRSHA256 = timedHash(original)
	editFound := false
	if edit != nil {
		if edit.Expected == edit.Replacement {
			return result, fmt.Errorf("timed C: no-op immediate edit")
		}

		for _, b := range blocks {
			if b.StartAddress != edit.Address {
				continue
			}
			if b.Instructions[0].Opcode != 0x69 {
				return result, fmt.Errorf("timed C: edit is not an ADC immediate")
			}
			raw, _ := hex.DecodeString(b.Instructions[0].Bytes)
			if len(raw) != 2 || raw[1] != edit.Expected {
				return result, fmt.Errorf("timed C: edit expected operand mismatch")
			}
			count := 0
			for i := range b.Statements {
				expr, n := timedEditExpr(b.Statements[i].Expr, edit.Expected, edit.Replacement)
				b.Statements[i].Expr = expr
				count += n
			}
			if count != 1 {
				return result, fmt.Errorf("timed C: ADC immediate edit is ambiguous")
			}

			editFound = true
		}
		if !editFound {
			return result, fmt.Errorf("timed C: edit instruction absent")
		}
	}
	edited, _ := json.Marshal(blocks)
	result.EditedIRSHA256 = timedHash(edited)
	result.ROMSHA256 = timedHash(rom)
	ebytes, _ := json.Marshal(edit)
	result.EditSHA256 = timedHash(ebytes)
	pbytes, _ := json.Marshal(plan)
	result.PlanSHA256 = timedHash(pbytes)
	var cases strings.Builder
	for _, b := range blocks {
		in := b.Instructions[0]
		p := schedules[in.Opcode]
		body, err := compilableBody(b)
		if err != nil {
			return result, err
		}
		fmt.Fprintf(&cases, "case 0x%06x: {\n", in.Address)
		if p.IdleBefore != 0 {
			fmt.Fprintf(&cases, "req('I',%d,0);\n", p.IdleBefore)
		}
		raw, _ := hex.DecodeString(in.Bytes)
		for _, v := range raw[1:] {
			fmt.Fprintf(&cases, "if(req('F',0,0)!=%d) return 4;\n", v)
		}
		label := fmt.Sprintf("exit_%06x", in.Address)
		cases.WriteString(strings.ReplaceAll(body, "block_exit", label))
		fmt.Fprintf(&cases, "%s:;\n", label)
		if p.TakenIdle != 0 {
			fmt.Fprintf(&cases, "if(res.next_pc!=0x%06x) req('I',%d,0);\n", b.Successors[0], p.TakenIdle)
		}
		if p.IdleAfter != 0 {
			fmt.Fprintf(&cases, "req('I',%d,0);\n", p.IdleAfter)
		}
		cases.WriteString("if(res.has_next){s.pc=res.next_pc&65535;s.pb=res.next_pc>>16;} break;}\n")
		result.Instructions = append(result.Instructions, in.Address)
	}
	result.Source = timedPrelude + cases.String() + timedEpilogue
	result.SourceSHA256 = timedHash([]byte(result.Source))
	return result, nil
}
func timedHash(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func timedEditExpr(e Expr, old, new byte) (Expr, int) {
	switch x := e.(type) {
	case *ConstExpr:
		if x.Width == Width8 && x.Value == uint32(old) {
			return &ConstExpr{Value: uint32(new), Width: x.Width}, 1
		}
	case *BinaryExpr:
		y := *x
		var a, b int
		y.Left, a = timedEditExpr(x.Left, old, new)
		y.Right, b = timedEditExpr(x.Right, old, new)
		return &y, a + b
	}
	return e, 0
}

// Timing is reviewed separately from the shared machine-semantic lowering.
var reviewedTimedSchedule = map[byte]TimedInstruction{
	0xee: {Opcode: 0xee, OperandBytes: 2, IdleBefore: 6}, 0xad: {Opcode: 0xad, OperandBytes: 2},
	0xc9: {Opcode: 0xc9, OperandBytes: 1}, 0xd0: {Opcode: 0xd0, OperandBytes: 1, TakenIdle: 6},
	0x18: {Opcode: 0x18, IdleBefore: 6}, 0x69: {Opcode: 0x69, OperandBytes: 1},
	0x8d: {Opcode: 0x8d, OperandBytes: 2}, 0x60: {Opcode: 0x60, IdleBefore: 12, IdleAfter: 6},
}

const timedPrelude = `#include <stdio.h>
#include <stdint.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>
typedef struct {uint16_t a,x,y,s,d,pc;uint8_t db,pb,p;bool e;} cpu_state_t;
typedef struct {bool has_next;uint32_t next_pc;} exec_result_t;
static unsigned req(char op,unsigned addr,unsigned value){unsigned reply;printf("%c %u %u\n",op,addr,value);fflush(stdout);if(scanf("%u",&reply)!=1)exit(2);return reply;}
static uint32_t cache_addr[4];static uint8_t cache_value[4];static unsigned cache_n;
static void valid_addr(uint32_t addr){unsigned bank=addr>>16;if((addr&65535)>=8192||!(bank<=63||(bank>=128&&bank<=191)))exit(5);}
static uint8_t read8(uint32_t addr){valid_addr(addr);for(unsigned i=0;i<cache_n;i++)if(cache_addr[i]==addr)return cache_value[i];if(cache_n==4)exit(5);uint8_t value=req('R',addr,0);cache_addr[cache_n]=addr;cache_value[cache_n++]=value;return value;}
static void mem_write8(exec_result_t *res,uint32_t addr,uint8_t value){(void)res;valid_addr(addr);req('W',addr,value);for(unsigned i=0;i<cache_n;i++)if(cache_addr[i]==addr){cache_value[i]=value;return;}if(cache_n==4)exit(5);cache_addr[cache_n]=addr;cache_value[cache_n++]=value;}
#define P_C ((s.p&1)!=0)
#define P_Z ((s.p&2)!=0)
#define P_I ((s.p&4)!=0)
#define P_D ((s.p&8)!=0)
#define P_X ((s.p&16)!=0)
#define P_M ((s.p&32)!=0)
#define P_V ((s.p&64)!=0)
#define P_N ((s.p&128)!=0)
int main(void){unsigned at,a,x,y,st,d,pc,db,pb,p;while(scanf("%u %u %u %u %u %u %u %u %u %u",&at,&a,&x,&y,&st,&d,&pc,&db,&pb,&p)==10){
if(a>65535||x>255||y>255||st>65535||d!=0||pc>65535||db!=12||pb>255||p>255||(p&56)!=48||at!=((pb<<16)|((pc-1)&65535)))return 5;
cpu_state_t s={(uint16_t)a,(uint16_t)x,(uint16_t)y,(uint16_t)st,(uint16_t)d,(uint16_t)pc,(uint8_t)db,(uint8_t)pb,(uint8_t)p,false};exec_result_t res={0};cache_n=0;
switch(at){
`
const timedEpilogue = `default:return 3;}
printf("D %u %u %u %u %u %u %u %u %u\n",s.a,s.x,s.y,s.s,s.d,s.pc,s.db,s.pb,s.p);fflush(stdout);
}return 0;}
`

func timedROMOffset(address uint32, size int) (int, bool) {
	bank := address >> 16
	low := address & 65535
	if address > 0xffffff || low < 0x8000 || bank == 0x7e || bank == 0x7f {
		return 0, false
	}
	offset := int(bank&0x7f)*32768 + int(low&0x7fff)
	return offset, offset < size
}
