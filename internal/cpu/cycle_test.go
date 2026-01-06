package cpu

import (
	"testing"

	"github.com/tmc/snes/internal/bus"
)

func TestOpcodeCycles_NOP(t *testing.T) {
	b := bus.NewBus()
	b.InitializeWaitStates()

	// Map WRAM at 00:0000 (WaitWRAM = 8)
	// Actually 00:0000-00:1FFF is Mirror of WRAM (8 cycles)
	// Let's rely on InitializeWaitStates setting Bank 00 to SlowROM (8) or WRAM (8).
	// wait_states.go: "WaitWRAM (Banks 7E-7F) is always 8 cycles".
	// "Fill everything with SlowROM (8 bytes)".
	// So Bank 00 is 8 cycles.

	wram := bus.NewRAMDevice(0x2000)
	b.Map(0x000000, 0x001FFF, wram)

	cpu := NewCPU(b)
	cpu.Cycles = 0
	cpu.PC = 0x1000
	cpu.PB = 0x00

	// Write NOP ($EA) at $00:1000
	wram.Write(0x1000, 0xEA)

	// Step
	cpu.Step()

	// Expected:
	// NOP is 2 CPU cycles.
	// 1. Fetch Opcode: 1 Bus Access (8 Master Cycles).
	// 2. Internal Operation: 1 Internal Cycle (6 Master Cycles).
	// Total = 14 Master Cycles.

	// Current Implementation likely:
	// Fetch Opcode: 8 Master Cycles.
	// Exec NOP: 0.
	// Total = 8.

	expected := uint64(14)
	if cpu.Cycles != expected {
		t.Errorf("NOP Cycles mismatch. Expected %d, got %d", expected, cpu.Cycles)
	}
}

func TestOpcodeCycles_Batch(t *testing.T) {
	tests := []struct {
		name     string
		opcode   uint8
		operand  []uint8
		mode     string // "Impl", "Imm", "Abs"
		expected uint64 // Master cycles
		setup    func(cpu *CPU, wram *bus.RAMDevice)
	}{
		// Implied
		{"NOP", 0xEA, nil, "Impl", 14, nil}, // 1 Fetch(8) + 1 Internal(6)
		{"CLC", 0x18, nil, "Impl", 14, nil}, // 1 Fetch(8) + 1 Internal(6)
		{"TAX", 0xAA, nil, "Impl", 14, nil}, // 1 Fetch(8) + 1 Internal(6)

		// Accumulator
		{"INC A", 0x1A, nil, "Acc", 14, nil}, // 1 Fetch(8) + 1 Internal(6)

		// Immediate
		{"LDA #00", 0xA9, []uint8{0x00}, "Imm", 16, nil}, // 1 FetchOp(8) + 1 FetchArg(8). Total 16. (2 CPU)

		// Absolute (Read)
		{"LDA $0010", 0xAD, []uint8{0x10, 0x00}, "Abs", 32, nil},
		// Fetch Op(8), Fetch AL(8), Fetch AH(8), Read Data(8). Total 4*8=32. (4 CPU)

		// Absolute (R-M-W)
		{"INC $0010", 0xEE, []uint8{0x10, 0x00}, "Abs", 46, nil},
		// FetchOp(8), FetchAL(8), FetchAH(8), ReadData(8), WriteData(8).
		// Bus = 5*8 = 40.
		// Total CPU = 6 cycles. 6*8 = 48? No, internal is 6.
		// 5 Bus Accesses (40) + 1 Internal (6) = 46.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := bus.NewBus()
			b.InitializeWaitStates()
			wram := bus.NewRAMDevice(0x2000)
			b.Map(0x000000, 0x001FFF, wram)

			cpu := NewCPU(b)
			cpu.Cycles = 0
			cpu.PC = 0x1000
			cpu.PB = 0x00

			// Write Opcode
			wram.Write(0x1000, tt.opcode)
			for i, v := range tt.operand {
				wram.Write(uint32(0x1001+i), v)
			}

			if tt.setup != nil {
				tt.setup(cpu, wram)
			}

			cpu.Step()

			if cpu.Cycles != tt.expected {
				t.Errorf("Mismatch for %s. Expected %d, got %d", tt.name, tt.expected, cpu.Cycles)
			}
		})
	}
}
