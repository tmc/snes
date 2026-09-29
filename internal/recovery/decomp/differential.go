package decomp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/bus"
	"github.com/tmc/snes/internal/cpu"
)

// CPUState holds CPU register and flag state.
type CPUState struct {
	A  uint16 `json:"a"`
	X  uint16 `json:"x"`
	Y  uint16 `json:"y"`
	S  uint16 `json:"s"`
	D  uint16 `json:"d"`
	DB uint8  `json:"db"`
	PB uint8  `json:"pb"`
	P  uint8  `json:"p"`
	E  bool   `json:"e"`
	PC uint32 `json:"pc"`
}

// MemoryWrite records a write to memory.
type MemoryWrite struct {
	Address uint32 `json:"address"`
	Value   uint8  `json:"value"`
}

// ExecResult captures the state and side effects after executing a block.
type ExecResult struct {
	State   CPUState      `json:"state"`
	NextPC  uint32        `json:"next_pc"`
	Writes  []MemoryWrite `json:"writes"`
}

// ComparisonReceipt records the verification outcome between C and emulator.
type ComparisonReceipt struct {
	CaseName   string     `json:"case_name"`
	Matched    bool       `json:"matched"`
	Initial    CPUState   `json:"initial_state"`
	Expected   ExecResult `json:"emulator_result"`
	ActualC    ExecResult `json:"compiled_c_result"`
	Discrepancy string    `json:"discrepancy,omitempty"`
}

// RunEmulatorBlock executes the block using the reference Go 65816 emulator.
func RunEmulatorBlock(ir *BlockIR, init CPUState, mem map[uint32]uint8) (ExecResult, error) {
	b := bus.NewBus()

	// Map 128KB WRAM
	wram := bus.NewWRAMDevice()
	b.Map(0x7E0000, 0x7FFFFF, wram)
	b.Map(0x000000, 0x001FFF, wram)

	// Map direct page / low RAM if needed
	ram := bus.NewRAMDevice(64 * 1024)
	b.Map(0x002000, 0x00FFFF, ram)

	// Populate initial memory
	for addr, val := range mem {
		b.Write(addr, val)
	}

	// Write block instruction bytes into memory at their addresses
	for _, inst := range ir.Instructions {
		bytes, err := decodeHexBytes(inst.Bytes)
		if err != nil {
			return ExecResult{}, fmt.Errorf("decode instruction bytes: %w", err)
		}
		for offset, byteVal := range bytes {
			b.Write(inst.Address+uint32(offset), byteVal)
		}
	}

	var recordedWrites []MemoryWrite
	b.WriteHook = func(address uint32, value uint8) {
		recordedWrites = append(recordedWrites, MemoryWrite{
			Address: address,
			Value:   value,
		})
	}

	c := cpu.NewCPU(b)
	c.A = init.A
	c.X = init.X
	c.Y = init.Y
	c.S = init.S
	c.D = init.D
	c.DB = init.DB
	c.PB = uint8(init.PC >> 16)
	c.PC = uint16(init.PC)
	c.P = init.P
	c.E = init.E

	// Step instructions
	for range ir.Instructions {
		c.Step()
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
		PC: nextPC,
	}

	return ExecResult{
		State:  finalState,
		NextPC: nextPC,
		Writes: recordedWrites,
	}, nil
}

// RunCompiledCBlock compiles and executes the generated C block using clang/cc.
func RunCompiledCBlock(ctx context.Context, ir *BlockIR, init CPUState, mem map[uint32]uint8) (ExecResult, error) {
	cCode, err := GenerateCompilableC(ir)
	if err != nil {
		return ExecResult{}, fmt.Errorf("generate C: %w", err)
	}

	// Create test runner C source
	var memInitBuf bytes.Buffer
	for addr, val := range mem {
		fmt.Fprintf(&memInitBuf, "    if (addr == 0x%06X) return 0x%02X;\n", addr, val)
	}

	runnerSrc := fmt.Sprintf(`%s

static uint8_t test_read_cb(void *ctx, uint32_t addr) {
    (void)ctx;
%s
    return 0;
}

int main(void) {
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

    exec_result_t res = execute_block_%06x(init_state, test_read_cb, NULL);

    printf("{\"state\":{\"a\":%%u,\"x\":%%u,\"y\":%%u,\"s\":%%u,\"pc\":%%u,\"d\":%%u,\"db\":%%u,\"pb\":%%u,\"p\":%%u,\"e\":%%s},\"next_pc\":%%u,\"writes\":[",
           res.state.a, res.state.x, res.state.y, res.state.s, res.state.pc, res.state.d,
           res.state.db, res.state.pb, res.state.p, res.state.e ? "true" : "false", res.next_pc);

    for (int i = 0; i < res.num_writes; i++) {
        if (i > 0) printf(",");
        printf("{\"address\":%%u,\"value\":%%u}", res.writes[i].address, res.writes[i].value);
    }
    printf("]}\n");
    return 0;
}
`, cCode, memInitBuf.String(), init.A, init.X, init.Y, init.S, uint16(init.PC), init.D, init.DB, init.PB, init.P, b2i(init.E), ir.StartAddress)

	// Persist to ~/tmp/
	home, err := os.UserHomeDir()
	if err != nil {
		return ExecResult{}, err
	}
	tmpDir := filepath.Join(home, "tmp", "snes-decomp-test")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return ExecResult{}, err
	}

	srcFile := filepath.Join(tmpDir, fmt.Sprintf("runner_%06x.c", ir.StartAddress))
	binFile := filepath.Join(tmpDir, fmt.Sprintf("runner_%06x", ir.StartAddress))

	if err := os.WriteFile(srcFile, []byte(runnerSrc), 0644); err != nil {
		return ExecResult{}, err
	}
	defer os.Remove(srcFile)
	defer os.Remove(binFile)

	// Compile with cc
	cmdCompile := exec.CommandContext(ctx, "cc", "-O2", "-Wall", "-Werror", "-Wno-unused-function", srcFile, "-o", binFile)
	if out, err := cmdCompile.CombinedOutput(); err != nil {
		return ExecResult{}, fmt.Errorf("compile C failed: %w (output: %s)", err, string(out))
	}

	// Execute runner
	cmdRun := exec.CommandContext(ctx, binFile)
	out, err := cmdRun.CombinedOutput()
	if err != nil {
		return ExecResult{}, fmt.Errorf("run C executable failed: %w (output: %s)", err, string(out))
	}

	var res ExecResult
	if err := json.Unmarshal(out, &res); err != nil {
		return ExecResult{}, fmt.Errorf("unmarshal C runner output %q: %w", string(out), err)
	}

	return res, nil
}

// CompareBlockExecution runs both emulator and C versions and verifies exact parity.
func CompareBlockExecution(t *testing.T, ir *BlockIR, caseName string, init CPUState, mem map[uint32]uint8) ComparisonReceipt {
	t.Helper()
	ctx := context.Background()

	emuRes, err := RunEmulatorBlock(ir, init, mem)
	if err != nil {
		t.Fatalf("[%s] emulator run failed: %v", caseName, err)
	}

	cRes, err := RunCompiledCBlock(ctx, ir, init, mem)
	if err != nil {
		t.Fatalf("[%s] compiled C run failed: %v", caseName, err)
	}

	receipt := ComparisonReceipt{
		CaseName: caseName,
		Initial:  init,
		Expected: emuRes,
		ActualC:  cRes,
		Matched:  true,
	}

	// Compare Registers
	if emuRes.State.A != cRes.State.A {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("A mismatch: emu=0x%04X, c=0x%04X", emuRes.State.A, cRes.State.A)
	}
	if emuRes.State.X != cRes.State.X {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("X mismatch: emu=0x%04X, c=0x%04X", emuRes.State.X, cRes.State.X)
	}
	if emuRes.State.Y != cRes.State.Y {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("Y mismatch: emu=0x%04X, c=0x%04X", emuRes.State.Y, cRes.State.Y)
	}

	// Compare active flags (N, V, Z, C):
	// Note: in 65816, flag mask is N(0x80), V(0x40), Z(0x02), C(0x01)
	flagMask := uint8(0x80 | 0x40 | 0x02 | 0x01)
	if (emuRes.State.P & flagMask) != (cRes.State.P & flagMask) {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("Flags mismatch: emu=0x%02X, c=0x%02X (masked: emu=0x%02X, c=0x%02X)",
			emuRes.State.P, cRes.State.P, emuRes.State.P&flagMask, cRes.State.P&flagMask)
	}

	// Compare Successor PC
	if emuRes.NextPC != cRes.NextPC {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("NextPC mismatch: emu=0x%06X, c=0x%06X", emuRes.NextPC, cRes.NextPC)
	}

	// Compare Memory Writes
	if len(emuRes.Writes) != len(cRes.Writes) {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("Write count mismatch: emu=%d, c=%d", len(emuRes.Writes), len(cRes.Writes))
	} else {
		for i := range emuRes.Writes {
			if emuRes.Writes[i].Address != cRes.Writes[i].Address || emuRes.Writes[i].Value != cRes.Writes[i].Value {
				receipt.Matched = false
				receipt.Discrepancy = fmt.Sprintf("Write[%d] mismatch: emu=(0x%06X: 0x%02X), c=(0x%06X: 0x%02X)",
					i, emuRes.Writes[i].Address, emuRes.Writes[i].Value, cRes.Writes[i].Address, cRes.Writes[i].Value)
				break
			}
		}
	}

	if !receipt.Matched {
		t.Errorf("[%s] %s", caseName, receipt.Discrepancy)
	}

	return receipt
}

func decodeHexBytes(s string) ([]byte, error) {
	s = strings.TrimPrefix(s, "0x")
	var res []byte
	for i := 0; i+1 < len(s); i += 2 {
		var b uint8
		fmt.Sscanf(s[i:i+2], "%02x", &b)
		res = append(res, b)
	}
	return res, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
