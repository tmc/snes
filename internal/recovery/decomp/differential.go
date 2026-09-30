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
	"strings"
	"testing"
	"time"

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
	State  CPUState      `json:"state"`
	NextPC uint32        `json:"next_pc"`
	Writes []MemoryWrite `json:"writes"`
}

// ComparisonReceipt records the verification outcome between C and emulator.
type ComparisonReceipt struct {
	CaseName    string     `json:"case_name"`
	Matched     bool       `json:"matched"`
	Initial     CPUState   `json:"initial_state"`
	Expected    ExecResult `json:"emulator_result,omitempty"`
	ActualC     ExecResult `json:"compiled_c_result,omitempty"`
	Discrepancy string     `json:"discrepancy,omitempty"`
}

// VerifyConfig controls verification parameters.
type VerifyConfig struct {
	Timeout         time.Duration
	EnforceContract bool
}

// DefaultVerifyConfig returns standard verification options (5s timeout, contract enforcement).
func DefaultVerifyConfig() VerifyConfig {
	return VerifyConfig{
		Timeout:         10 * time.Second,
		EnforceContract: true,
	}
}

// EnforceEntryContract verifies that the initial CPU state conforms to the block's entry context.
func EnforceEntryContract(ir *BlockIR, init CPUState) error {
	if ir == nil {
		return nil
	}
	ctx := ir.EntryContext

	// M flag: bit 5 (0x20) - Accumulator width (0=16-bit, 1=8-bit)
	if ctx.M == "set" || ctx.M == "1" {
		if (init.P & 0x20) == 0 {
			return fmt.Errorf("entry contract violation: expected M=1 (8-bit accumulator), got P=0x%02X (M=0)", init.P)
		}
	} else if ctx.M == "clear" || ctx.M == "0" {
		if (init.P & 0x20) != 0 {
			return fmt.Errorf("entry contract violation: expected M=0 (16-bit accumulator), got P=0x%02X (M=1)", init.P)
		}
	}

	// X flag: bit 4 (0x10) - Index register width (0=16-bit, 1=8-bit)
	if ctx.X == "set" || ctx.X == "1" {
		if (init.P & 0x10) == 0 {
			return fmt.Errorf("entry contract violation: expected X=1 (8-bit index), got P=0x%02X (X=0)", init.P)
		}
	} else if ctx.X == "clear" || ctx.X == "0" {
		if (init.P & 0x10) != 0 {
			return fmt.Errorf("entry contract violation: expected X=0 (16-bit index), got P=0x%02X (X=1)", init.P)
		}
	}

	// E flag: emulation mode
	if ctx.E == "set" || ctx.E == "1" {
		if !init.E {
			return fmt.Errorf("entry contract violation: expected E=true (emulation mode), got E=false")
		}
	} else if ctx.E == "clear" || ctx.E == "0" {
		if init.E {
			return fmt.Errorf("entry contract violation: expected E=false (native mode), got E=true")
		}
	}

	return nil
}

// formatFlags formats the 65816 processor status byte as NVMXDIZC.
func formatFlags(p uint8) string {
	var b strings.Builder
	flags := []struct {
		mask uint8
		name byte
	}{
		{0x80, 'N'}, {0x40, 'V'}, {0x20, 'M'}, {0x10, 'X'},
		{0x08, 'D'}, {0x04, 'I'}, {0x02, 'Z'}, {0x01, 'C'},
	}
	for _, f := range flags {
		if (p & f.mask) != 0 {
			b.WriteByte(f.name)
		} else {
			b.WriteByte('.')
		}
	}
	return b.String()
}

// CompareExecResults checks whether the actual C execution result matches the expected emulator result across
// all registers (A, X, Y, S, D, DB, PB, PC), full processor flags P (all 8 bits), emulation mode E,
// NextPC, and all memory writes in exact sequence.
func CompareExecResults(expected, actual ExecResult) (bool, string) {
	if expected.State.A != actual.State.A {
		return false, fmt.Sprintf("A mismatch: emu=0x%04X, c=0x%04X", expected.State.A, actual.State.A)
	}
	if expected.State.X != actual.State.X {
		return false, fmt.Sprintf("X mismatch: emu=0x%04X, c=0x%04X", expected.State.X, actual.State.X)
	}
	if expected.State.Y != actual.State.Y {
		return false, fmt.Sprintf("Y mismatch: emu=0x%04X, c=0x%04X", expected.State.Y, actual.State.Y)
	}
	if expected.State.S != actual.State.S {
		return false, fmt.Sprintf("S mismatch: emu=0x%04X, c=0x%04X", expected.State.S, actual.State.S)
	}
	if expected.State.D != actual.State.D {
		return false, fmt.Sprintf("D mismatch: emu=0x%04X, c=0x%04X", expected.State.D, actual.State.D)
	}
	if expected.State.DB != actual.State.DB {
		return false, fmt.Sprintf("DB mismatch: emu=0x%02X, c=0x%02X", expected.State.DB, actual.State.DB)
	}
	if expected.State.PB != actual.State.PB {
		return false, fmt.Sprintf("PB mismatch: emu=0x%02X, c=0x%02X", expected.State.PB, actual.State.PB)
	}
	if expected.State.P != actual.State.P {
		return false, fmt.Sprintf("Flags mismatch: emu=0x%02X (%s), c=0x%02X (%s)",
			expected.State.P, formatFlags(expected.State.P), actual.State.P, formatFlags(actual.State.P))
	}
	if expected.State.E != actual.State.E {
		return false, fmt.Sprintf("E mismatch: emu=%v, c=%v", expected.State.E, actual.State.E)
	}
	if expected.NextPC != actual.NextPC {
		return false, fmt.Sprintf("NextPC mismatch: emu=0x%06X, c=0x%06X", expected.NextPC, actual.NextPC)
	}
	if len(expected.Writes) != len(actual.Writes) {
		return false, fmt.Sprintf("Write count mismatch: emu=%d, c=%d", len(expected.Writes), len(actual.Writes))
	}
	for i := range expected.Writes {
		if expected.Writes[i].Address != actual.Writes[i].Address || expected.Writes[i].Value != actual.Writes[i].Value {
			return false, fmt.Sprintf("Write[%d] mismatch: emu=(0x%06X: 0x%02X), c=(0x%06X: 0x%02X)",
				i, expected.Writes[i].Address, expected.Writes[i].Value, actual.Writes[i].Address, actual.Writes[i].Value)
		}
	}
	return true, ""
}

// RunEmulatorBlock executes the block using the reference Go 65816 emulator with bounded execution.
func RunEmulatorBlock(ctx context.Context, ir *BlockIR, init CPUState, mem map[uint32]uint8) (ExecResult, error) {
	if err := ctx.Err(); err != nil {
		return ExecResult{}, err
	}

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
		if len(recordedWrites) < 256 {
			recordedWrites = append(recordedWrites, MemoryWrite{
				Address: address,
				Value:   value,
			})
		}
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

	// Step instructions with step bound and context check
	for range ir.Instructions {
		if err := ctx.Err(); err != nil {
			return ExecResult{}, fmt.Errorf("emulator execution timed out or cancelled: %w", err)
		}
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

// RunCompiledCBlock compiles and executes the generated C block with isolated artifacts and bounded execution.
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
#include <unistd.h>

static uint8_t test_read_cb(void *ctx, uint32_t addr) {
    (void)ctx;
%s
    return 0;
}

int main(void) {
    /* 3-second watchdog timer to bound execution */
    alarm(3);

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

	// Isolate runner artifacts into unique temporary directory under ~/tmp/
	home, err := os.UserHomeDir()
	if err != nil {
		return ExecResult{}, fmt.Errorf("user home dir: %w", err)
	}
	baseTmp := filepath.Join(home, "tmp")
	if err := os.MkdirAll(baseTmp, 0755); err != nil {
		return ExecResult{}, fmt.Errorf("create base tmp: %w", err)
	}

	var nonce [8]byte
	rand.Read(nonce[:])
	runDir := filepath.Join(baseTmp, fmt.Sprintf("snes-decomp-%06x-%s", ir.StartAddress, hex.EncodeToString(nonce[:])))
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return ExecResult{}, fmt.Errorf("create isolated run dir: %w", err)
	}
	defer os.RemoveAll(runDir)

	srcFile := filepath.Join(runDir, "runner.c")
	binFile := filepath.Join(runDir, "runner")

	if err := os.WriteFile(srcFile, []byte(runnerSrc), 0644); err != nil {
		return ExecResult{}, fmt.Errorf("write runner src: %w", err)
	}

	// Compile with cc under compile timeout
	compileCtx, cancelCompile := context.WithTimeout(ctx, 10*time.Second)
	defer cancelCompile()
	cmdCompile := exec.CommandContext(compileCtx, "cc", "-O0", "-Wall", "-Werror", "-Wno-unused-function", "-Wno-unused-label", srcFile, "-o", binFile)
	if out, err := cmdCompile.CombinedOutput(); err != nil {
		return ExecResult{}, fmt.Errorf("compile C failed: %w (output: %s)", err, string(out))
	}

	// Execute runner under execution timeout
	runCtx, cancelRun := context.WithTimeout(ctx, 3*time.Second)
	defer cancelRun()
	cmdRun := exec.CommandContext(runCtx, binFile)
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

// Compare is the unified comparison API used across tests, CLI, and HTTP server.
func Compare(ctx context.Context, ir *BlockIR, caseName string, init CPUState, mem map[uint32]uint8, cfg VerifyConfig) ComparisonReceipt {
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	receipt := ComparisonReceipt{
		CaseName: caseName,
		Initial:  init,
	}

	if cfg.EnforceContract {
		if err := EnforceEntryContract(ir, init); err != nil {
			receipt.Matched = false
			receipt.Discrepancy = err.Error()
			return receipt
		}
	}

	emuRes, emuErr := RunEmulatorBlock(ctx, ir, init, mem)
	if emuErr != nil {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("emulator error: %v", emuErr)
		return receipt
	}
	receipt.Expected = emuRes

	cRes, cErr := RunCompiledCBlock(ctx, ir, init, mem)
	if cErr != nil {
		receipt.Matched = false
		receipt.Discrepancy = fmt.Sprintf("compiled C error: %v", cErr)
		return receipt
	}
	receipt.ActualC = cRes

	matched, discrepancy := CompareExecResults(emuRes, cRes)
	receipt.Matched = matched
	receipt.Discrepancy = discrepancy
	return receipt
}

// CompareBlockExecution runs both emulator and C versions and verifies exact parity in unit tests.
func CompareBlockExecution(t *testing.T, ir *BlockIR, caseName string, init CPUState, mem map[uint32]uint8) ComparisonReceipt {
	t.Helper()
	receipt := Compare(context.Background(), ir, caseName, init, mem, DefaultVerifyConfig())
	if !receipt.Matched {
		t.Errorf("[%s] %s", caseName, receipt.Discrepancy)
	}
	return receipt
}

// ReceiptPath returns the canonical path for a block's verification receipt.
func ReceiptPath(projectDir, blockID string) string {
	return filepath.Join(projectDir, "verification", "pseudoc", fmt.Sprintf("receipt_%s.json", blockID))
}

// SaveReceipt writes a ComparisonReceipt to disk as JSON.
func SaveReceipt(path string, receipt ComparisonReceipt) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create receipt dir: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create receipt file: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(receipt); err != nil {
		return fmt.Errorf("encode receipt: %w", err)
	}
	return nil
}

// LoadReceipt reads a ComparisonReceipt from disk.
func LoadReceipt(path string) (ComparisonReceipt, error) {
	f, err := os.Open(path)
	if err != nil {
		return ComparisonReceipt{}, fmt.Errorf("open receipt file: %w", err)
	}
	defer f.Close()
	var r ComparisonReceipt
	if err := json.NewDecoder(f).Decode(&r); err != nil {
		return ComparisonReceipt{}, fmt.Errorf("decode receipt: %w", err)
	}
	return r, nil
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
