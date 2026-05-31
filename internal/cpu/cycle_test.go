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
		{"SEI", 0x78, nil, "Impl", 14, nil}, // 1 Fetch(8) + 1 Internal(6)
		{"CLI", 0x58, nil, "Impl", 14, nil}, // 1 Fetch(8) + 1 Internal(6)
		{"CLD", 0xD8, nil, "Impl", 14, nil}, // 1 Fetch(8) + 1 Internal(6)
		{"SED", 0xF8, nil, "Impl", 14, nil}, // 1 Fetch(8) + 1 Internal(6)
		{"CLV", 0xB8, nil, "Impl", 14, nil}, // 1 Fetch(8) + 1 Internal(6)
		{"TAX", 0xAA, nil, "Impl", 14, nil}, // 1 Fetch(8) + 1 Internal(6)
		{"INY", 0xC8, nil, "Impl", 14, nil}, // 1 Fetch(8) + 1 Internal(6)
		{"DEX", 0xCA, nil, "Impl", 14, nil}, // 1 Fetch(8) + 1 Internal(6)
		{"PHP", 0x08, nil, "Impl", 22, nil}, // 1 Fetch(8) + 1 Internal(6) + 1 Stack Write(8)
		{"PHA", 0x48, nil, "Impl", 22, nil}, // 1 Fetch(8) + 1 Internal(6) + 1 Stack Write(8)
		{"PLP", 0x28, nil, "Impl", 28, func(cpu *CPU, wram *bus.RAMDevice) {
			cpu.S = 0x01FE
			wram.Write(0x01FF, 0x00)
		}},
		{"PLA", 0x68, nil, "Impl", 28, func(cpu *CPU, wram *bus.RAMDevice) {
			cpu.S = 0x01FE
			wram.Write(0x01FF, 0x34)
		}},
		{"PLD", 0x2B, nil, "Impl", 36, func(cpu *CPU, wram *bus.RAMDevice) {
			cpu.S = 0x01FE
			wram.Write(0x01FF, 0x34)
			wram.Write(0x0100, 0x12)
		}},
		{"RTS", 0x60, nil, "Impl", 42, func(cpu *CPU, wram *bus.RAMDevice) {
			cpu.S = 0x01FE
			wram.Write(0x01FF, 0x34)
			wram.Write(0x0100, 0x12)
		}},
		{"XBA", 0xEB, nil, "Impl", 20, func(cpu *CPU, wram *bus.RAMDevice) {
			cpu.E = false
			cpu.P &^= 0x20
			cpu.A = 0x1234
		}}, // 1 Fetch(8) + 2 Internal(6)
		{"WAI", 0xCB, nil, "Impl", 20, nil}, // 1 Fetch(8) + 2 Internal(6)
		{"STP", 0xDB, nil, "Impl", 20, nil}, // 1 Fetch(8) + 2 Internal(6)

		// Accumulator
		{"INC A", 0x1A, nil, "Acc", 14, nil}, // 1 Fetch(8) + 1 Internal(6)
		{"DEC A", 0x3A, nil, "Acc", 14, nil}, // 1 Fetch(8) + 1 Internal(6)
		{"ROL A", 0x2A, nil, "Acc", 14, func(cpu *CPU, wram *bus.RAMDevice) {
			cpu.P = 0
			cpu.A = 0x0001
		}},

		// Immediate
		{"LDA #00", 0xA9, []uint8{0x00}, "Imm", 16, nil}, // 1 FetchOp(8) + 1 FetchArg(8). Total 16. (2 CPU)

		// Absolute (Read)
		{"LDA $0010", 0xAD, []uint8{0x10, 0x00}, "Abs", 32, nil},
		// Fetch Op(8), Fetch AL(8), Fetch AH(8), Read Data(8). Total 4*8=32. (4 CPU)
		{"JSR $1010", 0x20, []uint8{0x10, 0x10}, "Abs", 46, func(cpu *CPU, wram *bus.RAMDevice) {
			cpu.S = 0x01FF
		}},
		{"JSL $801234", 0x22, []uint8{0x34, 0x12, 0x80}, "Abs", 62, func(cpu *CPU, wram *bus.RAMDevice) {
			cpu.S = 0x01FF
		}},
		{"PEA $1234", 0xF4, []uint8{0x34, 0x12}, "Abs", 40, func(cpu *CPU, wram *bus.RAMDevice) {
			cpu.S = 0x01FF
		}},
		{"STZ $2100,X", 0x9E, []uint8{0x00, 0x21}, "Abs", 36, func(cpu *CPU, wram *bus.RAMDevice) {
			cpu.P |= 0x30
			cpu.X = 1
		}},

		// Absolute (R-M-W)
		{"INC $0010", 0xEE, []uint8{0x10, 0x00}, "Abs", 46, nil},
		// FetchOp(8), FetchAL(8), FetchAH(8), ReadData(8), WriteData(8).
		// Bus = 5*8 = 40.
		// Total CPU = 6 cycles. 6*8 = 48? No, internal is 6.
		// 5 Bus Accesses (40) + 1 Internal (6) = 46.
		{"DEC $0010", 0xCE, []uint8{0x10, 0x00}, "Abs", 46, nil},
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

func TestOpcodeCycles_STAIndirectYStoreIdle(t *testing.T) {
	tests := []struct {
		name       string
		p          uint8
		a          uint16
		storeBytes int
		wantBus    uint64
		wantCycles uint64
	}{
		{
			name:       "native 16-bit accumulator",
			p:          0x04,
			a:          0x1234,
			storeBytes: 2,
			wantBus:    44,
			wantCycles: 50,
		},
		{
			name:       "native 8-bit accumulator",
			p:          0x24,
			a:          0x1234,
			storeBytes: 1,
			wantBus:    36,
			wantCycles: 42,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := bus.NewBus()
			b.WriteMEMSEL(1)
			ram := NewSimpleRAM()
			b.Map(0x000000, 0xFFFFFF, ram)

			cpu := NewCPU(b)
			cpu.E = false
			cpu.P = tt.p
			cpu.A = tt.a
			cpu.Y = 4
			cpu.PB = 0xC0
			cpu.PC = 0x8151

			const (
				opAddr      = 0xC08151
				operandAddr = 0xC08152
				dpAddr      = 0x000008
				targetAddr  = 0x002004
			)
			ram.Write(opAddr, 0x91)
			ram.Write(operandAddr, 0x08)
			ram.Write(dpAddr+0, 0x00)
			ram.Write(dpAddr+1, 0x20)

			busCycles := b.GetWaitStates(opAddr) +
				b.GetWaitStates(operandAddr) +
				b.GetWaitStates(dpAddr+0) +
				b.GetWaitStates(dpAddr+1)
			for i := 0; i < tt.storeBytes; i++ {
				busCycles += b.GetWaitStates(targetAddr + uint32(i))
			}
			if busCycles != tt.wantBus {
				t.Fatalf("bus cycles = %d, want %d", busCycles, tt.wantBus)
			}

			cpu.Step()

			if cpu.Cycles != tt.wantCycles {
				t.Fatalf("cycles = %d, want %d", cpu.Cycles, tt.wantCycles)
			}
			if internal := cpu.Cycles - busCycles; internal != 6 {
				t.Fatalf("internal cycles = %d, want 6", internal)
			}
			if got := ram.Read(targetAddr); got != uint8(tt.a) {
				t.Fatalf("low store = %02X, want %02X", got, uint8(tt.a))
			}
			if tt.storeBytes == 2 {
				if got := ram.Read(targetAddr + 1); got != uint8(tt.a>>8) {
					t.Fatalf("high store = %02X, want %02X", got, uint8(tt.a>>8))
				}
			}
		})
	}
}

func TestOpcodeCycles_BranchTaken(t *testing.T) {
	b := bus.NewBus()
	b.InitializeWaitStates()
	wram := bus.NewRAMDevice(0x2000)
	b.Map(0x000000, 0x001FFF, wram)

	tests := []struct {
		name     string
		opcode   uint8
		p        uint8
		pc       uint16
		offset   uint8
		expected uint64
	}{
		{
			name:     "BRA same page",
			opcode:   0x80,
			pc:       0x1000,
			offset:   0x02,
			expected: 22, // fetch opcode+operand + taken branch internal cycle
		},
		{
			name:     "BNE same page",
			opcode:   0xD0,
			pc:       0x1000,
			offset:   0x02,
			expected: 22, // fetch opcode+operand + taken branch internal cycle
		},
		{
			name:     "BNE page cross",
			opcode:   0xD0,
			pc:       0x10FE,
			offset:   0xFF,
			expected: 28, // same-page cost + emulation-mode page-cross branch penalty
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := NewCPU(b)
			cpu.Cycles = 0
			cpu.PC = tt.pc
			cpu.PB = 0
			cpu.P = tt.p
			cpu.E = true

			wram.Write(uint32(tt.pc), tt.opcode)
			wram.Write(uint32(tt.pc+1), tt.offset)

			cpu.Step()

			if cpu.Cycles != tt.expected {
				t.Fatalf("%s cycles = %d, want %d", tt.name, cpu.Cycles, tt.expected)
			}
		})
	}
}

func TestOpcodeCycles_BRL(t *testing.T) {
	b := bus.NewBus()
	b.InitializeWaitStates()
	wram := bus.NewRAMDevice(0x2000)
	b.Map(0x000000, 0x001FFF, wram)

	cpu := NewCPU(b)
	cpu.Cycles = 0
	cpu.PC = 0x1000
	cpu.PB = 0

	wram.Write(0x1000, 0x82)
	wram.Write(0x1001, 0x34)
	wram.Write(0x1002, 0x12)

	cpu.Step()

	if cpu.Cycles != 30 {
		t.Fatalf("BRL cycles = %d, want 30", cpu.Cycles)
	}
	if cpu.PC != 0x2237 {
		t.Fatalf("PC = %04X, want 2237", cpu.PC)
	}
}

func TestNMICyclesIncludeInternalOverhead(t *testing.T) {
	b := bus.NewBus()
	b.InitializeWaitStates()
	wram := bus.NewRAMDevice(0x10000)
	b.Map(0x000000, 0x00FFFF, wram)
	wram.Write(0xFFEA, 0x34)
	wram.Write(0xFFEB, 0x12)
	wram.Write(0xFFFA, 0x78)
	wram.Write(0xFFFB, 0x56)

	tests := []struct {
		name   string
		e      bool
		vector uint16
		want   uint64
	}{
		{name: "emulation", e: true, vector: 0x5678, want: 48},
		{name: "native", e: false, vector: 0x1234, want: 56},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpu := NewCPU(b)
			cpu.Cycles = 0
			cpu.E = tt.e
			cpu.PB = 0x80
			cpu.PC = 0x8000
			cpu.P = 0
			cpu.S = 0x01FF
			cpu.TriggerNMI()

			cpu.Step()

			if cpu.Cycles != tt.want {
				t.Fatalf("cycles = %d, want %d", cpu.Cycles, tt.want)
			}
			if cpu.PC != tt.vector || cpu.PB != 0 {
				t.Fatalf("vector = %02X:%04X, want 00:%04X", cpu.PB, cpu.PC, tt.vector)
			}
			if cpu.NMIPending {
				t.Fatalf("NMIPending still set after NMI")
			}
		})
	}
}
