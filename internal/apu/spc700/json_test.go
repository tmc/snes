package spc700

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// SpcTest represents a single test case from the JSON suite
type SpcTest struct {
	Name    string   `json:"name"`
	Initial SpcState `json:"initial"`
	Final   SpcState `json:"final"`
	Cycles  [][]any  `json:"cycles"` // [addr, value, type]
}

type SpcState struct {
	PC  uint16  `json:"pc"`
	A   uint8   `json:"a"`
	X   uint8   `json:"x"`
	Y   uint8   `json:"y"`
	SP  uint8   `json:"sp"`
	PSW uint8   `json:"psw"`
	RAM [][]int `json:"ram"` // [addr, value]
}

// SimpleBus for testing
type SimpleBus struct {
	Mem [65536]uint8
}

func (b *SimpleBus) Read(addr uint16) uint8 {
	return b.Mem[addr]
}

func (b *SimpleBus) Write(addr uint16, val uint8) {
	b.Mem[addr] = val
}

// RunSpcTests executes standard JSON processor tests
// path is a directory containing .json files
func RunSpcTests(t *testing.T, path string) {
	files, err := filepath.Glob(filepath.Join(path, "*.json"))
	if err != nil {
		t.Fatalf("Failed to glob tests: %v", err)
	}
	if len(files) == 0 {
		t.Skipf("No test files found in %s", path)
	}

	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			runSpcTestFile(t, file)
		})
	}
}

func runSpcTestFile(t *testing.T, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read test file: %v", err)
	}

	var tests []SpcTest
	if err := json.Unmarshal(data, &tests); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			runSpcTestCase(t, test)
		})
	}
}

func runSpcTestCase(t *testing.T, test SpcTest) {
	// Setup System
	bus := &SimpleBus{}
	c := New(bus)

	// Initialize State
	c.PC = test.Initial.PC
	c.A = test.Initial.A
	c.X = test.Initial.X
	c.Y = test.Initial.Y
	c.SP = test.Initial.SP
	c.SetPSW(test.Initial.PSW)

	// Load RAM
	for _, entry := range test.Initial.RAM {
		addr := uint16(entry[0])
		val := uint8(entry[1])
		bus.Write(addr, val)
	}

	// Step CPU
	c.Step()

	// Verify State
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
	if gotPSW := c.GetPSW(); gotPSW != test.Final.PSW {
		t.Errorf("PSW mismatch: want %02X, got %02X", test.Final.PSW, gotPSW)
	}

	// Verify Output RAM
	for _, entry := range test.Final.RAM {
		addr := uint16(entry[0])
		val := uint8(entry[1])
		if got := bus.Read(addr); got != val {
			t.Errorf("RAM[%04X] mismatch: want %02X, got %02X", addr, val, got)
		}
	}
}

func TestSPC700_JSON(t *testing.T) {
	RunSpcTests(t, "testdata")
}
