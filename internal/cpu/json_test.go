package cpu

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/snes/internal/bus"
)

// ProcessorTest represents a single test case from the JSON suite
type ProcessorTest struct {
	Name    string      `json:"name"`
	Initial SystemState `json:"initial"`
	Final   SystemState `json:"final"`
	Cycles  [][]any     `json:"cycles"` // [addr, value, type]
}

type SystemState struct {
	PC  uint16  `json:"pc"`
	S   uint16  `json:"s"`
	A   uint16  `json:"a"`
	X   uint16  `json:"x"`
	Y   uint16  `json:"y"`
	P   uint8   `json:"p"`
	PBR uint8   `json:"pbr"`
	DBR uint8   `json:"dbr"`
	RAM [][]int `json:"ram"` // [addr, value]
}

// RunProcessorTests executes standard JSON processor tests
// path is a directory containing .json files
func RunProcessorTests(t *testing.T, path string) {
	files, err := filepath.Glob(filepath.Join(path, "*.json"))
	if err != nil {
		t.Fatalf("Failed to glob tests: %v", err)
	}
	if len(files) == 0 {
		t.Skipf("No test files found in %s", path)
	}

	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			runTestFile(t, file)
		})
	}
}

func runTestFile(t *testing.T, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read test file: %v", err)
	}

	var tests []ProcessorTest
	if err := json.Unmarshal(data, &tests); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			runTestCase(t, test)
		})
	}
}

func runTestCase(t *testing.T, test ProcessorTest) {
	// Setup System
	b := bus.NewBus()
	cpu := NewCPU(b)

	// Initialize State
	cpu.PC = test.Initial.PC
	cpu.S = test.Initial.S
	cpu.A = test.Initial.A // Accumulator (C)
	cpu.X = test.Initial.X
	cpu.Y = test.Initial.Y
	cpu.P = test.Initial.P
	cpu.PB = test.Initial.PBR
	cpu.DB = test.Initial.DBR
	cpu.E = (test.Initial.P & 0x10) != 0 // Assuming P contains X/M bit logic matching 65816 or default to 0
	// For standard 65816 tests, we might need to handle E more carefully.
	// But let's stick to simple field assignment.
	cpu.E = false // Default to Native for 65816 tests

	// Load RAM
	for _, entry := range test.Initial.RAM {
		addr := uint32(entry[0])
		val := uint8(entry[1])
		b.Write(addr, val)
	}

	// Step CPU
	// We need to execute exactly one instruction.
	// Our Step() executes one instruction.
	cpu.Step()

	// Verify State
	if cpu.PC != test.Final.PC {
		t.Errorf("PC mismatch: want %04X, got %04X", test.Final.PC, cpu.PC)
	}
	if cpu.S != test.Final.S {
		t.Errorf("SP mismatch: want %04X, got %04X", test.Final.S, cpu.S)
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
	if cpu.P != test.Final.P {
		t.Errorf("P mismatch: want %02X, got %02X", test.Final.P, cpu.P)
	}

	// Verify Output RAM
	for _, entry := range test.Final.RAM {
		addr := uint32(entry[0])
		val := uint8(entry[1])
		if got := b.Read(addr); got != val {
			t.Errorf("RAM[%06X] mismatch: want %02X, got %02X", addr, val, got)
		}
	}
}

func TestCPU_JSON(t *testing.T) {
	RunProcessorTests(t, "testdata")
}
