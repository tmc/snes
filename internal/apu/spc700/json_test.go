package spc700

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"testing"
)

// SpcTest represents a single test case from a JSON suite.
type SpcTest struct {
	Name    string   `json:"name"`
	Initial SpcState `json:"initial"`
	Final   SpcState `json:"final"`
	Cycles  [][]any  `json:"cycles"`
}

type SpcState struct {
	PC  uint16  `json:"pc"`
	A   uint8   `json:"a"`
	X   uint8   `json:"x"`
	Y   uint8   `json:"y"`
	SP  uint8   `json:"sp"`
	PSW uint8   `json:"psw"`
	RAM [][]int `json:"ram"`
}

type SimpleBus struct {
	Mem [65536]uint8
}

func (b *SimpleBus) Read(addr uint16) uint8 {
	return b.Mem[addr]
}

func (b *SimpleBus) Write(addr uint16, val uint8) {
	b.Mem[addr] = val
}

// RunSpcTests executes a JSON SPC700 suite.
func RunSpcTests(t *testing.T, path string) {
	runSpcTests(t, path, nil, -1)
}

func runSpcTests(t *testing.T, path string, basenames []string, limit int) {
	t.Helper()

	var files []string
	if len(basenames) == 0 {
		var err error
		files, err = filepath.Glob(filepath.Join(path, "*.json"))
		if err != nil {
			t.Fatalf("glob spc700 tests: %v", err)
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
			runSpcTestFile(t, file, limit)
		})
	}
}

func runSpcTestFile(t *testing.T, path string, limit int) {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open spc700 test file: %v", err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("read spc700 test file header: %v", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		t.Fatalf("spc700 test file %s is not a JSON array", path)
	}

	count := 0
	for dec.More() {
		if limit >= 0 && count >= limit {
			break
		}

		var test SpcTest
		if err := dec.Decode(&test); err != nil {
			t.Fatalf("decode spc700 test case: %v", err)
		}

		t.Run(test.Name, func(t *testing.T) {
			runSpcTestCase(t, test)
		})
		count++
	}

	if count == 0 {
		t.Fatalf("no spc700 tests executed from %s", path)
	}
}

func runSpcTestCase(t *testing.T, test SpcTest) {
	t.Helper()

	bus := &SimpleBus{}
	c := New(bus)

	c.PC = test.Initial.PC
	c.A = test.Initial.A
	c.X = test.Initial.X
	c.Y = test.Initial.Y
	c.SP = test.Initial.SP
	c.SetPSW(test.Initial.PSW)

	for _, entry := range test.Initial.RAM {
		bus.Write(uint16(entry[0]), uint8(entry[1]))
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic while executing %s: %v", test.Name, r)
		}
	}()
	c.Step()

	if c.PC != test.Final.PC {
		t.Errorf("PC mismatch: want %04X, got %04X", test.Final.PC, c.PC)
	}
	if c.A != test.Final.A {
		t.Errorf("A mismatch: want %02X, got %02X", test.Final.A, c.A)
	}
	if c.X != test.Final.X {
		t.Errorf("X mismatch: want %02X, got %02X", test.Final.X, c.X)
	}
	if c.Y != test.Final.Y {
		t.Errorf("Y mismatch: want %02X, got %02X", test.Final.Y, c.Y)
	}
	if c.SP != test.Final.SP {
		t.Errorf("SP mismatch: want %02X, got %02X", test.Final.SP, c.SP)
	}
	if got := c.GetPSW(); got != test.Final.PSW {
		t.Errorf("PSW mismatch: want %02X, got %02X", test.Final.PSW, got)
	}

	for _, entry := range test.Final.RAM {
		addr := uint16(entry[0])
		want := uint8(entry[1])
		if got := bus.Read(addr); got != want {
			t.Errorf("RAM[%04X] mismatch: want %02X, got %02X", addr, want, got)
		}
	}
}

func spc700TestsPath(t *testing.T, parts ...string) string {
	t.Helper()

	if root := os.Getenv("SNES_PROCESSORTESTS_ROOT"); root != "" {
		path := filepath.Join(append([]string{root}, parts...)...)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve spc700 tests path: runtime.Caller failed")
	}

	path := filepath.Join(append([]string{filepath.Dir(file), "../../../../ProcessorTests"}, parts...)...)
	if _, err := os.Stat(path); err != nil {
		t.Skipf("spc700 tests not available at %s", path)
	}
	return path
}

func spc700TestLimit(t *testing.T, fullEnv, casesEnv string, defaultLimit int) int {
	t.Helper()

	if os.Getenv("SNES_LOCAL_VALIDATION") != "" {
		return -1
	}
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

func TestSPC700_JSON(t *testing.T) {
	RunSpcTests(t, "testdata")
}

func TestSPC700_ProcessorTests(t *testing.T) {
	path := spc700TestsPath(t, "spc700", "v1")
	limit := spc700TestLimit(t, "SNES_PROCESSORTESTS_SPC700_FULL", "SNES_PROCESSORTESTS_SPC700_CASES", 1)
	files := spc700ProcessorSmokeFiles
	if limit < 0 {
		files = nil
	}
	runSpcTests(t, path, files, limit)
}

func TestSPC700TestLimitLocalValidation(t *testing.T) {
	t.Setenv("SNES_LOCAL_VALIDATION", "1")
	if got := spc700TestLimit(t, "X", "Y", 1); got != -1 {
		t.Fatalf("spc700TestLimit with SNES_LOCAL_VALIDATION = %d, want -1", got)
	}
}

var spc700ProcessorSmokeFiles = []string{
	"00.json",
	"02.json",
	"04.json",
	"08.json",
	"10.json",
	"11.json",
	"12.json",
	"1d.json",
	"1f.json",
	"20.json",
	"21.json",
	"22.json",
	"24.json",
	"28.json",
	"2f.json",
	"30.json",
}
