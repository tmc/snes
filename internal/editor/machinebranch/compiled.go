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
	profile  RegionConfig
	code     []byte
	timeline *InstructionTimeline
	cmd      *exec.Cmd
	input    io.WriteCloser
	lines    *bufio.Scanner
	dir      string
	report   *Compiled
}

func startCompiled(ctx context.Context, rom []byte, addend uint8, profile RegionConfig) (*compiledSession, error) {
	pins, err := PrepareRecovered(rom, addend, profile)
	if err != nil {
		return nil, err
	}
	return startRecovered(ctx, rom, addend, pins, profile)
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
	dir, err := os.MkdirTemp("", "snes-compiled-")
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
	if at < c.profile.Start || uint64(at) >= uint64(c.profile.Start)+uint64(len(c.code)) {
		return nil
	}
	return func(t *cpu.InstructionIO) (cpu.Snapshot, error) {
		st := t.State()
		if st.E || st.P&0x30 != 0x30 || st.P&8 != 0 || st.DB != c.profile.DataBank || st.D != 0 {
			return st, fmt.Errorf("unsupported replacement CPU context")
		}
		offset := int(at - c.profile.Start)
		boundary := false
		for i := 0; i < len(c.code); {
			if i == offset {
				boundary = true
				break
			}
			n := instructionLength(c.code[i])
			if n == 0 {
				break
			}
			i += n
		}
		if !boundary {
			return st, fmt.Errorf("replacement entry is not an instruction boundary")
		}
		if opcode != c.code[offset] {
			return st, fmt.Errorf("replacement opcode mismatch")
		}
		if _, err := fmt.Fprintf(c.input, "%d %d %d %d %d %d %d %d %d %d\n", at, st.A, st.X, st.Y, st.S, st.D, st.PC, st.DB, st.PB, st.P); err != nil {
			return st, err
		}
		fetched := 1
		var operand byte
		var reads []byte
		plan := c.instructionPlan(at, opcode, st)
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
				if c.timeline != nil {
					if err := c.timeline.record(at); err != nil {
						return st, err
					}
				}
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
					if offset+fetched >= len(c.code) || reply != c.code[offset+fetched] {
						return st, fmt.Errorf("replacement operand mismatch")
					}
					operand = reply
					fetched++
				}
			case "R":
				if !c.allowedAddress(addr, st.S) {
					return st, fmt.Errorf("read outside replacement contract")
				}
				reply, err = t.Read(addr)
				if err == nil {
					reads = append(reads, reply)
				}
			case "W":
				if !c.allowedCell(addr) || value > 255 {
					return st, fmt.Errorf("write outside replacement contract")
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
func (c *compiledSession) allowedAddress(a uint32, s uint16) bool {
	return c.allowedCell(a) || a == uint32(uint16(s+1)) || a == uint32(uint16(s+2))
}

func (c *compiledSession) allowedCell(a uint32) bool {
	for _, cell := range c.profile.Cells {
		if a == cell {
			return true
		}
	}
	return false
}

type timedRequest struct {
	op      string
	address uint32
}

func (c *compiledSession) instructionPlan(at uint32, op uint8, st cpu.Snapshot) []timedRequest {
	f := timedRequest{"F", 0}
	i := func(n uint32) timedRequest { return timedRequest{"I", n} }
	offset := int(at - c.profile.Start)
	addr := uint32(st.DB) << 16
	if offset+2 < len(c.code) {
		addr |= uint32(c.code[offset+1]) | uint32(c.code[offset+2])<<8
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
func instructionLength(op byte) int {
	switch op {
	case 0xee, 0xad, 0x8d:
		return 3
	case 0xc9, 0xd0, 0x69:
		return 2
	case 0x18, 0x60:
		return 1
	}
	return 0
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
