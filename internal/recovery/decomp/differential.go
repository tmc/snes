package decomp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	PC uint16 `json:"pc"`
}

// MemoryWrite records a write to memory at its canonical bus address.
type MemoryWrite struct {
	Address uint32 `json:"address"`
	Value   uint8  `json:"value"`
}

// ExecResult captures the state and side effects after executing a block.
type ExecResult struct {
	State         CPUState      `json:"state"`
	NextPC        uint32        `json:"next_pc"`
	Writes        []MemoryWrite `json:"writes"`
	TotalWrites   uint32        `json:"total_writes,omitempty"`
	WriteOverflow bool          `json:"write_overflow,omitempty"`
	MissingRead   bool          `json:"missing_read,omitempty"`
	MissingAddr   uint32        `json:"missing_addr,omitempty"`
	MMIOAccess    bool          `json:"mmio_access,omitempty"`
	MMIOAddr      uint32        `json:"mmio_addr,omitempty"`
}

// ReceiptMetadata records artifact binding and provenance for a verification receipt.
type ReceiptMetadata struct {
	ProjectRevision string `json:"project_revision,omitempty"`
	ROMSHA256       string `json:"rom_sha256,omitempty"`
	BlockID         string `json:"block_id"`
	StartAddress    uint32 `json:"start_address"`
	CodeHash        string `json:"code_hash"`
	GeneratedCHash  string `json:"generated_c_hash"`
	Compiler        string `json:"compiler,omitempty"`
	Timestamp       string `json:"timestamp"`
	IsStale         bool   `json:"is_stale,omitempty"`
}

// ComparisonReceipt records the verification outcome between C and emulator.
type ComparisonReceipt struct {
	Metadata    ReceiptMetadata `json:"metadata"`
	CaseName    string          `json:"case_name"`
	Matched     bool            `json:"matched"`
	Initial     CPUState        `json:"initial_state"`
	InitialMem  map[string]int  `json:"initial_memory,omitempty"`
	Expected    ExecResult      `json:"emulator_result,omitempty"`
	ActualC     ExecResult      `json:"compiled_c_result,omitempty"`
	Discrepancy string          `json:"discrepancy,omitempty"`
}

// VerifyConfig controls verification parameters.
type VerifyConfig struct {
	Timeout         time.Duration
	EnforceContract bool
	ProjectRevision string
	ROMSHA256       string
}

// DefaultVerifyConfig returns standard verification options (10s timeout, contract enforcement).
func DefaultVerifyConfig() VerifyConfig {
	return VerifyConfig{
		Timeout:         10 * time.Second,
		EnforceContract: true,
	}
}

// BusCanonicalAddr maps mirrored SNES addresses to canonical WRAM address space.
func BusCanonicalAddr(addr uint32) uint32 {
	a := addr & 0xFFFFFF
	bank := uint8((a >> 16) & 0xFF)
	offset := uint16(a & 0xFFFF)
	if (bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) && offset < 0x2000 {
		return 0x7E0000 | uint32(offset)
	}
	return a
}

// IsMMIOAddr checks if an address belongs to hardware MMIO registers.
func IsMMIOAddr(addr uint32) bool {
	a := addr & 0xFFFFFF
	bank := uint8((a >> 16) & 0xFF)
	offset := uint16(a & 0xFFFF)
	if bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF) {
		if (offset >= 0x2100 && offset <= 0x21FF) || (offset >= 0x4200 && offset <= 0x43FF) {
			return true
		}
	}
	return false
}

// EnforceEntryContract verifies that the initial CPU state conforms to the block's entry context.
func EnforceEntryContract(ir *BlockIR, init CPUState) error {
	if ir == nil {
		return fmt.Errorf("enforce entry contract: nil BlockIR")
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
	} else {
		return fmt.Errorf("unresolved entry context: M is unknown or unspecified")
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
	} else {
		return fmt.Errorf("unresolved entry context: X is unknown or unspecified")
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
	} else {
		return fmt.Errorf("unresolved entry context: E is unknown or unspecified")
	}

	// Optional C flag constraint
	if ctx.C == "set" || ctx.C == "1" {
		if (init.P & 0x01) == 0 {
			return fmt.Errorf("entry contract violation: expected C=1 (carry set), got C=0")
		}
	} else if ctx.C == "clear" || ctx.C == "0" {
		if (init.P & 0x01) != 0 {
			return fmt.Errorf("entry contract violation: expected C=0 (carry clear), got C=1")
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
	if expected.State.PC != actual.State.PC {
		return false, fmt.Sprintf("PC mismatch: emu=0x%04X, c=0x%04X", expected.State.PC, actual.State.PC)
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
	if expected.WriteOverflow != actual.WriteOverflow {
		return false, fmt.Sprintf("Write overflow mismatch: emu=%v, c=%v", expected.WriteOverflow, actual.WriteOverflow)
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

// ComputeBlockCodeHash computes a SHA-256 fingerprint over a block's instruction bytes and mnemonics.
func ComputeBlockCodeHash(ir *BlockIR) string {
	h := sha256.New()
	for _, inst := range ir.Instructions {
		fmt.Fprintf(h, "%d:%s:%s;", inst.Address, inst.Mnemonic, inst.Bytes)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ComputeCHash computes a SHA-256 fingerprint over generated C code.
func ComputeCHash(cCode string) string {
	sum := sha256.Sum256([]byte(cCode))
	return hex.EncodeToString(sum[:])
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
	b.Map(0x800000, 0x801FFF, wram)

	// Map direct page / low RAM if needed
	ram := bus.NewRAMDevice(64 * 1024)
	b.Map(0x002000, 0x00FFFF, ram)

	// Track initialized memory addresses
	initializedMem := make(map[uint32]bool)

	// Populate initial memory
	for addr, val := range mem {
		cAddr := BusCanonicalAddr(addr)
		if IsMMIOAddr(addr) {
			return ExecResult{}, fmt.Errorf("unsupported MMIO access to address $%06X in initial memory", addr)
		}
		b.Write(cAddr, val)
		initializedMem[cAddr] = true
		initializedMem[addr] = true
	}

	// Dynamically map banks required by instructions and write instruction bytes
	mappedBanks := make(map[uint8]bool)
	mappedBanks[0x00] = true
	mappedBanks[0x7E] = true
	mappedBanks[0x7F] = true
	mappedBanks[0x80] = true

	for _, inst := range ir.Instructions {
		bank := uint8(inst.Address >> 16)
		if !mappedBanks[bank] {
			bankBase := uint32(bank) << 16
			bankDev := bus.NewRAMDevice(64 * 1024)
			b.Map(bankBase, bankBase|0xFFFF, bankDev)
			mappedBanks[bank] = true
		}
		bytes, err := decodeHexBytes(inst.Bytes)
		if err != nil {
			return ExecResult{}, fmt.Errorf("decode instruction bytes: %w", err)
		}
		for offset, byteVal := range bytes {
			instAddr := inst.Address + uint32(offset)
			b.Write(instAddr, byteVal)
			initializedMem[instAddr] = true
		}
	}

	// Also ensure init.PB bank is mapped
	if !mappedBanks[init.PB] {
		bankBase := uint32(init.PB) << 16
		bankDev := bus.NewRAMDevice(64 * 1024)
		b.Map(bankBase, bankBase|0xFFFF, bankDev)
		mappedBanks[init.PB] = true
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
		if len(recordedWrites) < 256 {
			recordedWrites = append(recordedWrites, MemoryWrite{
				Address: cAddr,
				Value:   value,
			})
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

	// Step instructions with step bound and context check
	for range ir.Instructions {
		if err := ctx.Err(); err != nil {
			return ExecResult{}, fmt.Errorf("emulator execution timed out or cancelled: %w", err)
		}
		c.Step()
	}

	if mmioAccess {
		return ExecResult{}, fmt.Errorf("unsupported MMIO access to address $%06X", mmioAddr)
	}
	if missingRead {
		return ExecResult{}, fmt.Errorf("read from uninitialized memory address $%06X (missing input data)", missingAddr)
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

// RunCompiledCBlock compiles and executes the generated C block with isolated artifacts and bounded execution.
func RunCompiledCBlock(ctx context.Context, ir *BlockIR, init CPUState, mem map[uint32]uint8) (ExecResult, error) {
	cCode, err := GenerateCompilableC(ir)
	if err != nil {
		return ExecResult{}, fmt.Errorf("generate C: %w", err)
	}

	return RunRawCompilableC(ctx, ir.StartAddress, cCode, init, mem)
}

// RunRawCompilableC compiles and executes given C code string in an isolated directory.
func RunRawCompilableC(ctx context.Context, startAddr uint32, cCode string, init CPUState, mem map[uint32]uint8) (ExecResult, error) {
	// Build runner memory table
	var memCells bytes.Buffer
	memCount := 0
	for addr, val := range mem {
		cAddr := BusCanonicalAddr(addr)
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
    for (int i = 0; i < m->count; i++) {
        if (m->cells[i].addr == addr) {
            if (missing) *missing = false;
            return m->cells[i].val;
        }
    }
    if (missing) *missing = true;
    return 0;
}

int main(void) {
    /* 3-second watchdog timer to bound execution */
    alarm(3);

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

    exec_result_t res = execute_block_%06x(init_state, test_read_cb, &m);

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
`, cCode, memCount+1, memCount, memCells.String(), init.A, init.X, init.Y, init.S, init.PC, init.D, init.DB, init.PB, init.P, b2i(init.E), startAddr)

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
	runDir := filepath.Join(baseTmp, fmt.Sprintf("snes-decomp-%06x-%s", startAddr, hex.EncodeToString(nonce[:])))
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

	if res.MMIOAccess {
		return ExecResult{}, fmt.Errorf("unsupported MMIO access to address $%06X", res.MMIOAddr)
	}
	if res.MissingRead {
		return ExecResult{}, fmt.Errorf("read from uninitialized memory address $%06X (missing input data)", res.MissingAddr)
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

	cCode, _ := GenerateCompilableC(ir)

	receipt := ComparisonReceipt{
		Metadata: ReceiptMetadata{
			ProjectRevision: cfg.ProjectRevision,
			ROMSHA256:       cfg.ROMSHA256,
			BlockID:         "",
			StartAddress:    0,
			CodeHash:        "",
			GeneratedCHash:  ComputeCHash(cCode),
			Compiler:        "cc (clang)",
			Timestamp:       time.Now().UTC().Format(time.RFC3339),
		},
		CaseName: caseName,
		Initial:  init,
	}

	if ir != nil {
		receipt.Metadata.BlockID = ir.BlockID
		receipt.Metadata.StartAddress = ir.StartAddress
		receipt.Metadata.CodeHash = ComputeBlockCodeHash(ir)
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

// SaveReceipt writes a ComparisonReceipt atomically to disk as JSON.
func SaveReceipt(path string, receipt ComparisonReceipt) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create receipt dir: %w", err)
	}
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create receipt temp file: %w", err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(receipt); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("encode receipt: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close receipt temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename receipt file: %w", err)
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

// ValidateReceiptFreshness checks whether a loaded receipt matches the current block code and generated C code.
func ValidateReceiptFreshness(receipt *ComparisonReceipt, ir *BlockIR, cCode string) {
	if receipt == nil || ir == nil {
		return
	}
	expectedCodeHash := ComputeBlockCodeHash(ir)
	expectedCHash := ComputeCHash(cCode)
	if receipt.Metadata.CodeHash != expectedCodeHash || receipt.Metadata.GeneratedCHash != expectedCHash {
		receipt.Metadata.IsStale = true
	}
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
