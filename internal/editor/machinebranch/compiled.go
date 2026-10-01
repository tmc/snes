package machinebranch

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tmc/snes/internal/cpu"
)

// Compiled records a timed instruction runner and optional recovery identities.
// It grants no captured recovery qualification.
type Compiled struct {
	OriginalIRJSON  []byte `json:"-"`
	EditedIRJSON    []byte `json:"-"`
	Source          string `json:"source"`
	SemanticsOrigin string `json:"semantics_origin,omitempty"`
	IRSHA256        string `json:"ir_sha256,omitempty"`
	EditedIRSHA256  string `json:"edited_ir_sha256,omitempty"`
	ROMSHA256       string `json:"rom_sha256,omitempty"`
	PlanSHA256      string `json:"plan_sha256,omitempty"`
	EditSHA256      string `json:"edit_sha256,omitempty"`
	SourceSHA256    string `json:"source_sha256"`
	RunnerSHA256    string `json:"runner_sha256"`
	Compiler        string `json:"compiler"`
	Instructions    uint64 `json:"instructions"`
	Addend          uint8  `json:"addend"`
}

type compiledSession struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	lines  *bufio.Scanner
	dir    string
	report *Compiled
}

// This template implements only the measured native 8-bit rotation vocabulary.
// Every operand is fetched by the runtime; C supplies semantics and requests
// the same timed bus operations as the source opcode implementations.
const cTemplate = `#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
OPCODE_DISPATCH
static unsigned req(char op,unsigned addr,unsigned value) {
 unsigned reply; printf("%c %u %u\n",op,addr,value); fflush(stdout);
 if(scanf("%u",&reply)!=1) exit(2); return reply;
}
static unsigned fetch(void){return req('F',0,0);}
static unsigned word(void){unsigned l=fetch(); return l|(fetch()<<8);}
static void nz(unsigned *p,unsigned v){*p=(*p&~130u)|(v?0:2)|(v&128);}
int main(void){unsigned at,a,x,y,s,d,pc,db,pb,p;
 while(scanf("%u %u %u %u %u %u %u %u %u %u",&at,&a,&x,&y,&s,&d,&pc,&db,&pb,&p)==10){
 unsigned addr,v,r,lo,hi;
 switch(decode_op(at)){
 case 0xee:
 req('I',6,0);addr=(db<<16)|word();v=req('R',addr,0);v=(v+1)&255;req('W',addr,v);nz(&p,v);pc+=2;break;
 case 0xad:
 addr=(db<<16)|word();v=req('R',addr,0);a=(a&65280)|v;nz(&p,v);pc+=2;break;
 case 0xc9:
 v=fetch();r=((a&255)-v)&255;p=(p&~1u)|((a&255)>=v);nz(&p,r);pc++;break;
 case 0xd0:
 v=fetch();pc++;if(!(p&2)){pc=(pc+(int8_t)v)&65535;req('I',6,0);}break;
 case 0x18:req('I',6,0);p&=~1u;break;
 case 0x69:
 v=fetch();if(at==0x0cc46c)v=EDIT_ADDEND;
 lo=a&255;r=lo+v+(p&1);p=(p&~65u)|(r>255)|((~(lo^v)&(lo^r)&128)?64:0);a=(a&65280)|(r&255);nz(&p,r&255);pc++;break;
 case 0x8d:addr=(db<<16)|word();req('W',addr,a&255);pc+=2;break;
 case 0x60:
 req('I',12,0);s=(s+1)&65535;lo=req('R',s,0);s=(s+1)&65535;hi=req('R',s,0);req('I',6,0);pc=((hi<<8)|lo)+1;pc&=65535;break;
 default:return 3;
 }
 printf("D %u %u %u %u %u %u %u %u %u\n",a,x,y,s,d,pc,db,pb,p);fflush(stdout);
 }return 0;}
`

var rotationBytes = []byte{0xee, 1, 0x1e, 0xad, 1, 0x1e, 0xc9, 0x40, 0xd0, 3, 0xee, 0, 0x1e, 0xad, 5, 0x1f, 0x18, 0x69, 5, 0x8d, 5, 0x1f, 0xad, 4, 0x1f, 0x18, 0x69, 3, 0x8d, 4, 0x1f, 0x60}

func startCompiled(ctx context.Context, rom []byte, addend uint8) (*compiledSession, error) {
	const offset = 0x6445b
	if len(rom) < offset+len(rotationBytes) || string(rom[offset:offset+len(rotationBytes)]) != string(rotationBytes) {
		return nil, fmt.Errorf("rotation ROM vocabulary mismatch")
	}
	source := strings.ReplaceAll(cTemplate, "EDIT_ADDEND", fmt.Sprint(addend))
	dispatch, err := opcodeDispatch(rom[offset : offset+len(rotationBytes)])
	if err != nil {
		return nil, err
	}
	source = strings.ReplaceAll(source, "OPCODE_DISPATCH", dispatch)
	return compileTimedSource(ctx, source, addend, "")
}

func compileTimedSource(ctx context.Context, source string, addend uint8, origin string) (*compiledSession, error) {
	compiler, err := exec.LookPath("cc")
	if err != nil {
		return nil, err
	}
	version, err := exec.CommandContext(ctx, compiler, "--version").Output()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(filepath.Join(home, "tmp"), "snes-compiled-")
	if err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(dir)
		}
	}()
	src := filepath.Join(dir, "runner.c")
	bin := filepath.Join(dir, "runner")
	if err = os.WriteFile(src, []byte(source), 0600); err != nil {
		return nil, err
	}
	if out, e := exec.CommandContext(ctx, compiler, "-std=c99", "-O2", "-Wall", "-Wextra", src, "-o", bin).CombinedOutput(); e != nil {
		return nil, fmt.Errorf("compile runner: %w: %s", e, out)
	}
	bytes, err := os.ReadFile(bin)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	scan := bufio.NewScanner(out)
	scan.Buffer(make([]byte, 4096), 4096)
	cleanup = false
	return &compiledSession{cmd: cmd, input: in, lines: scan, dir: dir, report: &Compiled{Source: source, SemanticsOrigin: origin, SourceSHA256: digest([]byte(source)), RunnerSHA256: digest(bytes), Compiler: strings.TrimSpace(string(version)), Addend: addend}}, nil
}
func (c *compiledSession) close() {
	c.input.Close()
	c.cmd.Process.Kill()
	c.cmd.Wait()
	os.RemoveAll(c.dir)
}
func (c *compiledSession) selectInstruction(at uint32, opcode uint8) cpu.InstructionExecutor {
	if at < 0x0cc45b || at >= 0x0cc47b {
		return nil
	}
	return func(t *cpu.InstructionIO) (cpu.Snapshot, error) {
		st := t.State()
		if st.E || st.P&0x30 != 0x30 || st.P&8 != 0 || st.DB != 0x0c || st.D != 0 {
			return st, fmt.Errorf("unsupported rotation CPU context")
		}
		switch at {
		case 0x0cc45b, 0x0cc45e, 0x0cc461, 0x0cc463, 0x0cc465, 0x0cc468, 0x0cc46b, 0x0cc46c, 0x0cc46e, 0x0cc471, 0x0cc474, 0x0cc475, 0x0cc477, 0x0cc47a:
		default:
			return st, fmt.Errorf("rotation entry is not an instruction boundary")
		}
		offset := int(at - 0x0cc45b)
		if opcode != rotationBytes[offset] {
			return st, fmt.Errorf("rotation opcode mismatch")
		}
		if _, err := fmt.Fprintf(c.input, "%d %d %d %d %d %d %d %d %d %d\n", at, st.A, st.X, st.Y, st.S, st.D, st.PC, st.DB, st.PB, st.P); err != nil {
			return st, err
		}
		fetched := 1
		var operand byte
		var reads []byte
		plan := instructionPlan(at, opcode, st)
		operation := 0
		for n := 0; n < 32; n++ {
			if !c.lines.Scan() {
				return st, fmt.Errorf("compiled runner ended: %v", c.lines.Err())
			}
			line := c.lines.Text()
			fields := strings.Fields(line)
			if len(fields) == 0 {
				return st, fmt.Errorf("empty compiled response")
			}
			var op string
			var addr, value uint32
			if fields[0] == "D" {
				if len(fields) != 10 || operation != len(plan) {
					return st, fmt.Errorf("incomplete compiled instruction or malformed done")
				}
				if err := numericFields(fields[1:]); err != nil {
					return st, err
				}
				var end string
				var a, x, y, s, d, pc, db, pb, p uint32
				count, err := fmt.Sscan(line, &end, &a, &x, &y, &s, &d, &pc, &db, &pb, &p)
				if err != nil || count != 10 || a > 65535 || x > 255 || y > 255 || s > 65535 || d != uint32(st.D) || pc > 65535 || db != uint32(st.DB) || pb != uint32(st.PB) || p > 255 {
					return st, fmt.Errorf("invalid compiled successor")
				}
				if c.report.SemanticsOrigin == "generic_machine_ir" {
					if p&0x38 != 0x30 {
						return st, fmt.Errorf("recovered successor violates native width or binary contract")
					}
					if x != uint32(st.X) || y != uint32(st.Y) {
						return st, fmt.Errorf("recovered successor changes index registers")
					}
					wantPC, wantS, err := recoveredSuccessor(opcode, st, t.State().PC, operand, reads)
					if err != nil {
						return st, err
					}
					if pc != uint32(wantPC) || s != uint32(wantS) {
						return st, fmt.Errorf("recovered successor violates control-flow or stack contract")
					}
				}
				st.A, st.X, st.Y, st.S, st.PC, st.P = uint16(a), uint16(x), uint16(y), uint16(s), uint16(pc), uint8(p)
				st.Cycles = t.State().Cycles
				c.report.Instructions++
				return st, nil
			}
			if len(fields) != 3 {
				return st, fmt.Errorf("malformed compiled request")
			}
			if err := numericFields(fields[1:]); err != nil {
				return st, err
			}
			count, err := fmt.Sscan(line, &op, &addr, &value)
			if err != nil || count != 3 {
				return st, fmt.Errorf("invalid compiled request")
			}
			if operation >= len(plan) || op != plan[operation].op || addr != plan[operation].address || (op != "W" && value != 0) {
				return st, fmt.Errorf("compiled operation differs from timing plan")
			}
			operation++
			var reply uint8
			switch op {
			case "F":
				reply, err = t.Fetch()
				if err == nil {
					if offset+fetched >= len(rotationBytes) || reply != rotationBytes[offset+fetched] {
						return st, fmt.Errorf("rotation operand mismatch")
					}
					operand = reply
					fetched++
				}
			case "R":
				if !rotationAddress(addr, st.S) {
					return st, fmt.Errorf("read outside rotation contract")
				}
				reply, err = t.Read(addr)
				if err == nil {
					reads = append(reads, reply)
				}
			case "W":
				if !rotationCell(addr) || value > 255 {
					return st, fmt.Errorf("write outside rotation contract")
				}
				err = t.Write(addr, uint8(value))
			case "I":
				err = t.Idle(uint64(addr))
			default:
				return st, fmt.Errorf("unknown compiled operation")
			}
			if err != nil {
				return st, err
			}
			if _, err = fmt.Fprintf(c.input, "%d\n", reply); err != nil {
				return st, err
			}
		}
		return st, fmt.Errorf("compiled instruction operation limit")
	}
}
func rotationAddress(a uint32, s uint16) bool {
	return rotationCell(a) || a == uint32(uint16(s+1)) || a == uint32(uint16(s+2))
}

func rotationCell(a uint32) bool {
	return a == 0x0c1e00 || a == 0x0c1e01 || a == 0x0c1f04 || a == 0x0c1f05
}

type timedRequest struct {
	op      string
	address uint32
}

func instructionPlan(at uint32, op uint8, st cpu.Snapshot) []timedRequest {
	f := timedRequest{"F", 0}
	i := func(n uint32) timedRequest { return timedRequest{"I", n} }
	offset := int(at - 0x0cc45b)
	addr := uint32(st.DB) << 16
	if offset+2 < len(rotationBytes) {
		addr |= uint32(rotationBytes[offset+1]) | uint32(rotationBytes[offset+2])<<8
	}
	r, w := timedRequest{"R", addr}, timedRequest{"W", addr}
	switch op {
	case 0xee:
		return []timedRequest{i(6), f, f, r, w}
	case 0xad:
		return []timedRequest{f, f, r}
	case 0xc9, 0x69:
		return []timedRequest{f}
	case 0xd0:
		if st.P&2 == 0 {
			return []timedRequest{f, i(6)}
		}
		return []timedRequest{f}
	case 0x18:
		return []timedRequest{i(6)}
	case 0x8d:
		return []timedRequest{f, f, w}
	case 0x60:
		return []timedRequest{i(12), {"R", uint32(uint16(st.S + 1))}, {"R", uint32(uint16(st.S + 2))}, i(6)}
	}
	return nil
}
func numericFields(fields []string) error {
	for _, s := range fields {
		if s == "" {
			return fmt.Errorf("missing numeric field")
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return fmt.Errorf("invalid numeric field")
			}
		}
		if _, err := strconv.ParseUint(s, 10, 32); err != nil {
			return fmt.Errorf("invalid numeric field: %w", err)
		}
	}
	return nil
}
func opcodeDispatch(code []byte) (string, error) {
	var b strings.Builder
	b.WriteString("static unsigned decode_op(unsigned at){switch(at){\n")
	for offset := 0; offset < len(code); {
		op := code[offset]
		n := 0
		switch op {
		case 0xee, 0xad, 0x8d:
			n = 3
		case 0xc9, 0xd0, 0x69:
			n = 2
		case 0x18, 0x60:
			n = 1
		default:
			return "", fmt.Errorf("unsupported template opcode")
		}
		if offset+n > len(code) {
			return "", fmt.Errorf("truncated template instruction")
		}
		fmt.Fprintf(&b, "case %d:return %d;\n", 0x0cc45b+offset, op)
		offset += n
	}
	b.WriteString("default:return 0;}}\n")
	return b.String(), nil
}

// recoveredSuccessor checks only protocol control flow, not ALU semantics.
func recoveredSuccessor(op byte, entry cpu.Snapshot, fetchedPC uint16, operand byte, reads []byte) (uint16, uint16, error) {
	pc, s := fetchedPC, entry.S
	switch op {
	case 0xd0:
		if entry.P&2 == 0 {
			pc = uint16(int32(pc) + int32(int8(operand)))
		}
	case 0x60:
		if len(reads) != 2 {
			return 0, 0, fmt.Errorf("recovered RTS requires two stack reads")
		}
		pc = (uint16(reads[0]) | uint16(reads[1])<<8) + 1
		s += 2
	}
	return pc, s, nil
}
