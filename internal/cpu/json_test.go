package cpu

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"testing"

	"github.com/tmc/snes/internal/bus"
)

// ProcessorTest represents a single test case from a JSON suite.
type ProcessorTest struct {
	Name    string      `json:"name"`
	Initial SystemState `json:"initial"`
	Final   SystemState `json:"final"`
	Cycles  [][]any     `json:"cycles"`
}

type SystemState struct {
	PC  uint16  `json:"pc"`
	S   uint16  `json:"s"`
	A   uint16  `json:"a"`
	X   uint16  `json:"x"`
	Y   uint16  `json:"y"`
	D   uint16  `json:"d"`
	P   uint8   `json:"p"`
	PBR uint8   `json:"pbr"`
	DBR uint8   `json:"dbr"`
	E   uint8   `json:"e"`
	RAM [][]int `json:"ram"`
}

type sparseRAM struct {
	mem map[uint32]uint8
}

func newSparseRAM() *sparseRAM {
	return &sparseRAM{mem: make(map[uint32]uint8)}
}

func (m *sparseRAM) Read(address uint32) uint8 {
	return m.mem[address&0xFFFFFF]
}

func (m *sparseRAM) Write(address uint32, value uint8) {
	m.mem[address&0xFFFFFF] = value
}

func (m *sparseRAM) BlockRead(address uint32, length int) []byte {
	data := make([]byte, length)
	for i := range data {
		data[i] = m.Read(address + uint32(i))
	}
	return data
}

// RunProcessorTests executes a JSON processor suite.
func RunProcessorTests(t *testing.T, path string) {
	runProcessorTests(t, path, nil, -1)
}

func runProcessorTests(t *testing.T, path string, basenames []string, limit int) {
	t.Helper()

	var files []string
	if len(basenames) == 0 {
		var err error
		files, err = filepath.Glob(filepath.Join(path, "*.json"))
		if err != nil {
			t.Fatalf("glob processor tests: %v", err)
		}
	} else {
		files = make([]string, 0, len(basenames))
		for _, name := range basenames {
			files = append(files, filepath.Join(path, name))
		}
	}
	if len(files) == 0 {
		t.Skipf("no test files found in %s", path)
	}
	sort.Strings(files)

	for _, file := range files {
		file := file
		t.Run(filepath.Base(file), func(t *testing.T) {
			runProcessorTestFile(t, file, limit)
		})
	}
}

func runProcessorTestFile(t *testing.T, path string, limit int) {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open processor test file: %v", err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("read processor test file header: %v", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		t.Fatalf("processor test file %s is not a JSON array", path)
	}

	count := 0
	for dec.More() {
		if limit >= 0 && count >= limit {
			break
		}

		var test ProcessorTest
		if err := dec.Decode(&test); err != nil {
			t.Fatalf("decode processor test case: %v", err)
		}

		t.Run(test.Name, func(t *testing.T) {
			runProcessorTestCase(t, test)
		})
		count++
	}

	if count == 0 {
		t.Fatalf("no processor tests executed from %s", path)
	}
}

func runProcessorTestCase(t *testing.T, test ProcessorTest) {
	t.Helper()

	b := bus.NewBus()
	mem := newSparseRAM()
	b.Map(0x000000, 0xFFFFFF, mem)
	cpu := NewCPU(b)

	cpu.PC = test.Initial.PC
	cpu.S = test.Initial.S
	cpu.A = test.Initial.A
	cpu.X = test.Initial.X
	cpu.Y = test.Initial.Y
	cpu.D = test.Initial.D
	cpu.P = test.Initial.P
	cpu.PB = test.Initial.PBR
	cpu.DB = test.Initial.DBR
	cpu.E = test.Initial.E != 0

	for _, entry := range test.Initial.RAM {
		mem.Write(uint32(entry[0]), uint8(entry[1]))
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic while executing %s: %v", test.Name, r)
		}
	}()
	cpu.Step()

	if cpu.PC != test.Final.PC {
		t.Errorf("PC mismatch: want %04X, got %04X", test.Final.PC, cpu.PC)
	}
	if cpu.S != test.Final.S {
		t.Errorf("S mismatch: want %04X, got %04X", test.Final.S, cpu.S)
	}
	if cpu.A != test.Final.A {
		t.Errorf("A mismatch: want %04X, got %04X", test.Final.A, cpu.A)
	}
	if cpu.X != test.Final.X {
		t.Errorf("X mismatch: want %04X, got %04X", test.Final.X, cpu.X)
	}
	if cpu.Y != test.Final.Y {
		t.Errorf("Y mismatch: want %04X, got %04X", test.Final.Y, cpu.Y)
	}
	if cpu.D != test.Final.D {
		t.Errorf("D mismatch: want %04X, got %04X", test.Final.D, cpu.D)
	}
	if cpu.P != test.Final.P {
		t.Errorf("P mismatch: want %02X, got %02X", test.Final.P, cpu.P)
	}
	if cpu.PB != test.Final.PBR {
		t.Errorf("PB mismatch: want %02X, got %02X", test.Final.PBR, cpu.PB)
	}
	if cpu.DB != test.Final.DBR {
		t.Errorf("DB mismatch: want %02X, got %02X", test.Final.DBR, cpu.DB)
	}
	if gotE := boolToUint8(cpu.E); gotE != test.Final.E {
		t.Errorf("E mismatch: want %d, got %d", test.Final.E, gotE)
	}

	for _, entry := range test.Final.RAM {
		addr := uint32(entry[0])
		want := uint8(entry[1])
		if got := mem.Read(addr); got != want {
			t.Errorf("RAM[%06X] mismatch: want %02X, got %02X", addr, want, got)
		}
	}
}

func boolToUint8(v bool) uint8 {
	if v {
		return 1
	}
	return 0
}

func processorTestsPath(t *testing.T, parts ...string) string {
	t.Helper()

	if root := os.Getenv("SNES_PROCESSORTESTS_ROOT"); root != "" {
		path := filepath.Join(append([]string{root}, parts...)...)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve processor tests path: runtime.Caller failed")
	}

	path := filepath.Join(append([]string{filepath.Dir(file), "../../../ProcessorTests"}, parts...)...)
	if _, err := os.Stat(path); err != nil {
		t.Skipf("processor tests not available at %s", path)
	}
	return path
}

func processorTestLimit(t *testing.T, fullEnv, casesEnv string, defaultLimit int) int {
	t.Helper()

	if os.Getenv("SNES_PROCESSORTESTS_FULL") != "" || os.Getenv(fullEnv) != "" {
		return -1
	}
	if testing.Short() {
		return 1
	}
	if s := os.Getenv(casesEnv); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			t.Fatalf("parse %s: %v", casesEnv, err)
		}
		return n
	}
	return defaultLimit
}

func TestCPU_JSON(t *testing.T) {
	RunProcessorTests(t, "testdata")
}

func TestCPU_ProcessorTests(t *testing.T) {
	path := processorTestsPath(t, "65816", "v1")
	limit := processorTestLimit(t, "SNES_PROCESSORTESTS_65816_FULL", "SNES_PROCESSORTESTS_65816_CASES", 1)
	files := cpuProcessorSmokeFiles
	if limit < 0 {
		files = nil
	}
	runProcessorTests(t, path, files, limit)
}

var cpuProcessorSmokeFiles = []string{
	"00.e.json",
	"00.n.json",
	"01.e.json",
	"01.n.json",
	"02.e.json",
	"02.n.json",
	"03.e.json",
	"03.n.json",
	"04.e.json",
	"04.n.json",
	"05.e.json",
	"05.n.json",
	"06.e.json",
	"06.n.json",
	"07.e.json",
	"07.n.json",
	"08.e.json",
	"08.n.json",
	"09.e.json",
	"09.n.json",
	"0a.e.json",
	"0a.n.json",
	"0b.e.json",
	"0b.n.json",
	"0c.e.json",
	"0c.n.json",
	"0d.e.json",
	"0d.n.json",
	"0e.e.json",
	"0e.n.json",
	"0f.e.json",
	"0f.n.json",
	"10.e.json",
	"10.n.json",
	"11.e.json",
	"11.n.json",
	"12.e.json",
	"12.n.json",
	"13.e.json",
	"13.n.json",
	"14.e.json",
	"14.n.json",
	"15.e.json",
	"15.n.json",
	"16.e.json",
	"16.n.json",
	"17.e.json",
	"17.n.json",
	"18.e.json",
	"18.n.json",
	"19.e.json",
	"19.n.json",
	"1a.e.json",
	"1a.n.json",
	"1b.e.json",
	"1b.n.json",
	"1c.e.json",
	"1c.n.json",
	"1d.e.json",
	"1d.n.json",
	"1e.e.json",
	"1e.n.json",
	"1f.e.json",
	"1f.n.json",
	"20.e.json",
	"20.n.json",
	"21.e.json",
	"21.n.json",
	"22.e.json",
	"22.n.json",
	"23.e.json",
	"23.n.json",
	"24.e.json",
	"24.n.json",
	"25.e.json",
	"25.n.json",
	"26.e.json",
	"26.n.json",
	"27.e.json",
	"27.n.json",
	"28.e.json",
	"28.n.json",
	"29.e.json",
	"29.n.json",
	"2a.e.json",
	"2a.n.json",
	"2b.e.json",
	"2b.n.json",
	"2c.e.json",
	"2c.n.json",
	"2d.e.json",
	"2d.n.json",
	"2e.e.json",
	"2e.n.json",
	"2f.e.json",
	"2f.n.json",
	"30.e.json",
	"30.n.json",
	"31.e.json",
	"31.n.json",
	"32.e.json",
	"32.n.json",
	"33.e.json",
	"33.n.json",
	"34.e.json",
	"34.n.json",
	"35.e.json",
	"35.n.json",
	"36.e.json",
	"36.n.json",
	"37.e.json",
	"37.n.json",
	"38.e.json",
	"38.n.json",
	"39.e.json",
	"39.n.json",
	"3a.e.json",
	"3a.n.json",
	"3b.e.json",
	"3b.n.json",
	"3c.e.json",
	"3c.n.json",
	"3d.e.json",
	"3d.n.json",
	"3e.e.json",
	"3e.n.json",
	"3f.e.json",
	"3f.n.json",
	"40.e.json",
	"40.n.json",
}
