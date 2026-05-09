package apu

import "testing"

func TestWritePortPatchesPendingCMPY(t *testing.T) {
	a := NewAPU()
	a.Control = 0
	a.RAM[0x0200] = 0x7E // CMP Y, dp
	a.RAM[0x0201] = 0xF4
	a.Processor.PC = 0x0200
	a.Processor.Y = 0x10
	a.InPorts[0] = 0x10
	a.SetPortComparePatch(true)

	a.Run()
	if a.pending == 0 {
		t.Fatal("cmp instruction retired before port write")
	}
	if !a.Processor.Z || !a.Processor.C {
		t.Fatalf("initial cmp flags Z=%v C=%v, want true true", a.Processor.Z, a.Processor.C)
	}

	a.WritePort(0, 0x11)
	if a.Processor.Z || !a.Processor.N || a.Processor.C {
		t.Fatalf("patched cmp flags Z=%v N=%v C=%v, want false true false", a.Processor.Z, a.Processor.N, a.Processor.C)
	}
}

func TestWritePortPatchesPendingCMPA(t *testing.T) {
	a := NewAPU()
	a.Control = 0
	a.RAM[0x0200] = 0x64 // CMP A, dp
	a.RAM[0x0201] = 0xF4
	a.Processor.PC = 0x0200
	a.Processor.A = 0x10
	a.InPorts[0] = 0x10
	a.SetPortComparePatch(true)

	a.Run()
	if a.pending == 0 {
		t.Fatal("cmp instruction retired before port write")
	}
	if !a.Processor.Z || !a.Processor.C {
		t.Fatalf("initial cmp flags Z=%v C=%v, want true true", a.Processor.Z, a.Processor.C)
	}

	a.WritePort(0, 0x11)
	if a.Processor.Z || !a.Processor.N || a.Processor.C {
		t.Fatalf("patched cmp flags Z=%v N=%v C=%v, want false true false", a.Processor.Z, a.Processor.N, a.Processor.C)
	}
}

func TestWritePortPatchesPendingCMPX(t *testing.T) {
	a := NewAPU()
	a.Control = 0
	a.RAM[0x0200] = 0x3E // CMP X, dp
	a.RAM[0x0201] = 0xF4
	a.Processor.PC = 0x0200
	a.Processor.X = 0x10
	a.InPorts[0] = 0x10
	a.SetPortComparePatch(true)

	a.Run()
	if a.pending == 0 {
		t.Fatal("cmp instruction retired before port write")
	}
	if !a.Processor.Z || !a.Processor.C {
		t.Fatalf("initial cmp flags Z=%v C=%v, want true true", a.Processor.Z, a.Processor.C)
	}

	a.WritePort(0, 0x11)
	if a.Processor.Z || !a.Processor.N || a.Processor.C {
		t.Fatalf("patched cmp flags Z=%v N=%v C=%v, want false true false", a.Processor.Z, a.Processor.N, a.Processor.C)
	}
}

func TestWritePortPatchesPendingCMPAbsolutePorts(t *testing.T) {
	tests := []struct {
		name   string
		opcode uint8
		setReg func(*APU)
	}{
		{
			name:   "CMP A, abs",
			opcode: 0x65,
			setReg: func(a *APU) {
				a.Processor.A = 0x10
			},
		},
		{
			name:   "CMP X, abs",
			opcode: 0x1E,
			setReg: func(a *APU) {
				a.Processor.X = 0x10
			},
		},
		{
			name:   "CMP Y, abs",
			opcode: 0x5E,
			setReg: func(a *APU) {
				a.Processor.Y = 0x10
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewAPU()
			a.Control = 0
			a.RAM[0x0200] = tt.opcode
			a.RAM[0x0201] = 0xF4
			a.RAM[0x0202] = 0x00
			a.Processor.PC = 0x0200
			tt.setReg(a)
			a.InPorts[0] = 0x10
			a.SetPortComparePatch(true)

			a.Run()
			// Path B slice 2 (opcode 0x5E = CMP Y, !abs targeting
			// $00F4): the absolute-port read goes through the
			// cycle-split microOp; flag commit is deferred to
			// cycle 3. The other test cases (CMP A,abs at 0x65 and
			// CMP X,abs at 0x1E) still use atomic-retire and the
			// original shim-based timing.
			inMicroOp := a.microOp.active
			if !inMicroOp {
				if a.pending == 0 {
					t.Fatal("cmp instruction retired before port write")
				}
				if !a.Processor.Z || !a.Processor.C {
					t.Fatalf("initial cmp flags Z=%v C=%v, want true true", a.Processor.Z, a.Processor.C)
				}
			}

			a.WritePort(0, 0x11)

			for a.microOp.active {
				a.Run()
			}
			if a.Processor.Z || !a.Processor.N || a.Processor.C {
				t.Fatalf("patched cmp flags Z=%v N=%v C=%v, want false true false", a.Processor.Z, a.Processor.N, a.Processor.C)
			}
		})
	}
}

func TestWritePortPatchesPendingCMPADirectIndexed(t *testing.T) {
	a := NewAPU()
	a.Control = 0
	a.RAM[0x0200] = 0x74 // CMP A, dp+X
	a.RAM[0x0201] = 0xF0
	a.Processor.PC = 0x0200
	a.Processor.A = 0x10
	a.Processor.X = 0x04
	a.InPorts[0] = 0x10
	a.SetPortComparePatch(true)

	a.Run()
	if a.pending == 0 {
		t.Fatal("cmp instruction retired before port write")
	}
	if !a.Processor.Z || !a.Processor.C {
		t.Fatalf("initial cmp flags Z=%v C=%v, want true true", a.Processor.Z, a.Processor.C)
	}

	a.WritePort(0, 0x11)
	if a.Processor.Z || !a.Processor.N || a.Processor.C {
		t.Fatalf("patched cmp flags Z=%v N=%v C=%v, want false true false", a.Processor.Z, a.Processor.N, a.Processor.C)
	}
}

func TestWritePortPatchesPendingMOVDirectPorts(t *testing.T) {
	tests := []struct {
		name   string
		opcode uint8
		got    func(*APU) uint8
	}{
		{name: "MOV A, dp", opcode: 0xE4, got: func(a *APU) uint8 { return a.Processor.A }},
		{name: "MOV X, dp", opcode: 0xF8, got: func(a *APU) uint8 { return a.Processor.X }},
		{name: "MOV Y, dp", opcode: 0xEB, got: func(a *APU) uint8 { return a.Processor.Y }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewAPU()
			a.Control = 0
			a.RAM[0x0200] = tt.opcode
			a.RAM[0x0201] = 0xF4
			a.Processor.PC = 0x0200
			a.InPorts[0] = 0x10
			a.SetPortComparePatch(true)

			a.Run()
			if a.pending == 0 {
				t.Fatal("mov instruction retired before port write")
			}
			if got := tt.got(a); got != 0x10 {
				t.Fatalf("initial load = %02X, want 10", got)
			}

			a.WritePort(0, 0x80)
			if got := tt.got(a); got != 0x80 {
				t.Fatalf("patched load = %02X, want 80", got)
			}
			if !a.Processor.N || a.Processor.Z {
				t.Fatalf("patched load flags N=%v Z=%v, want true false", a.Processor.N, a.Processor.Z)
			}
		})
	}
}

func TestWritePortPatchesPendingMOVIndexedAndAbsolutePorts(t *testing.T) {
	tests := []struct {
		name  string
		code  []uint8
		setup func(*APU)
		got   func(*APU) uint8
	}{
		{
			name: "MOV A, dp+X",
			code: []uint8{0xF4, 0xF0},
			setup: func(a *APU) {
				a.Processor.X = 0x04
			},
			got: func(a *APU) uint8 { return a.Processor.A },
		},
		{name: "MOV A, abs", code: []uint8{0xE5, 0xF4, 0x00}, got: func(a *APU) uint8 { return a.Processor.A }},
		{name: "MOV X, abs", code: []uint8{0xE9, 0xF4, 0x00}, got: func(a *APU) uint8 { return a.Processor.X }},
		{name: "MOV Y, abs", code: []uint8{0xEC, 0xF4, 0x00}, got: func(a *APU) uint8 { return a.Processor.Y }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewAPU()
			a.Control = 0
			copy(a.RAM[0x0200:], tt.code)
			a.Processor.PC = 0x0200
			if tt.setup != nil {
				tt.setup(a)
			}
			a.InPorts[0] = 0x10
			a.SetPortComparePatch(true)

			a.Run()
			// Path B slice 2 (opcode 0xE5 = MOV A, !abs targeting
			// $00F4): the absolute-port read goes through the
			// cycle-split microOp, which has not yet performed the
			// port read after one Run(). The other test cases (MOV X,
			// abs / MOV Y, abs at 0xE9 / 0xEC) still use the atomic-
			// retire path and the original shim-based timing applies.
			inMicroOp := a.microOp.active
			if !inMicroOp {
				if a.pending == 0 {
					t.Fatal("mov instruction retired before port write")
				}
				if got := tt.got(a); got != 0x10 {
					t.Fatalf("initial load = %02X, want 10", got)
				}
			}

			a.WritePort(0, 0x80)

			// Drain any in-flight microOp.
			for a.microOp.active {
				a.Run()
			}
			if got := tt.got(a); got != 0x80 {
				t.Fatalf("patched load = %02X, want 80", got)
			}
		})
	}
}

func TestWritePortPatchesPendingLogicDirectPorts(t *testing.T) {
	tests := []struct {
		name  string
		code  []uint8
		a     uint8
		want  uint8
		wantN bool
	}{
		{name: "OR A, dp", code: []uint8{0x04, 0xF4}, a: 0x01, want: 0x81, wantN: true},
		{name: "AND A, dp", code: []uint8{0x24, 0xF4}, a: 0xF0, want: 0x80, wantN: true},
		{name: "EOR A, dp", code: []uint8{0x44, 0xF4}, a: 0x81, want: 0x01, wantN: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewAPU()
			a.Control = 0
			copy(a.RAM[0x0200:], tt.code)
			a.Processor.PC = 0x0200
			a.Processor.A = tt.a
			a.InPorts[0] = 0x10
			a.SetPortComparePatch(true)

			a.Run()
			if a.pending == 0 {
				t.Fatal("logic instruction retired before port write")
			}

			a.WritePort(0, 0x80)
			if got := a.Processor.A; got != tt.want {
				t.Fatalf("patched A = %02X, want %02X", got, tt.want)
			}
			if a.Processor.N != tt.wantN || a.Processor.Z {
				t.Fatalf("patched flags N=%v Z=%v, want %v false", a.Processor.N, a.Processor.Z, tt.wantN)
			}
		})
	}
}

func TestWritePortPatchesPendingLogicIndexedPorts(t *testing.T) {
	tests := []struct {
		name string
		code []uint8
		a    uint8
		want uint8
	}{
		{name: "OR A, dp+X", code: []uint8{0x14, 0xF0}, a: 0x01, want: 0x81},
		{name: "AND A, dp+X", code: []uint8{0x34, 0xF0}, a: 0xF0, want: 0x80},
		{name: "EOR A, dp+X", code: []uint8{0x54, 0xF0}, a: 0x81, want: 0x01},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewAPU()
			a.Control = 0
			copy(a.RAM[0x0200:], tt.code)
			a.Processor.PC = 0x0200
			a.Processor.A = tt.a
			a.Processor.X = 0x04
			a.InPorts[0] = 0x10
			a.SetPortComparePatch(true)

			a.Run()
			if a.pending == 0 {
				t.Fatal("logic instruction retired before port write")
			}

			a.WritePort(0, 0x80)
			if got := a.Processor.A; got != tt.want {
				t.Fatalf("patched A = %02X, want %02X", got, tt.want)
			}
		})
	}
}

func TestWritePortPatchesPendingADCAndSBCPortReads(t *testing.T) {
	tests := []struct {
		name  string
		code  []uint8
		setup func(*APU)
		carry bool
	}{
		{name: "ADC A, dp", code: []uint8{0x84, 0xF4}},
		{name: "ADC A, abs", code: []uint8{0x85, 0xF4, 0x00}},
		{name: "ADC A, (X)", code: []uint8{0x86}, setup: func(a *APU) { a.Processor.X = 0xF4 }},
		{name: "ADC A, (dp+X)", code: []uint8{0x87, 0x20}, setup: setupPortIndexedIndirect},
		{name: "ADC A, dp+X", code: []uint8{0x94, 0xF0}, setup: func(a *APU) { a.Processor.X = 0x04 }},
		{name: "ADC A, abs+X", code: []uint8{0x95, 0xF0, 0x00}, setup: func(a *APU) { a.Processor.X = 0x04 }},
		{name: "ADC A, abs+Y", code: []uint8{0x96, 0xF0, 0x00}, setup: func(a *APU) { a.Processor.Y = 0x04 }},
		{name: "ADC A, (dp)+Y", code: []uint8{0x97, 0x20}, setup: setupPortIndirectIndexed},
		{name: "SBC A, dp", code: []uint8{0xA4, 0xF4}, carry: true},
		{name: "SBC A, abs", code: []uint8{0xA5, 0xF4, 0x00}, carry: true},
		{name: "SBC A, (X)", code: []uint8{0xA6}, setup: func(a *APU) { a.Processor.X = 0xF4 }, carry: true},
		{name: "SBC A, (dp+X)", code: []uint8{0xA7, 0x20}, setup: setupPortIndexedIndirect, carry: true},
		{name: "SBC A, dp+X", code: []uint8{0xB4, 0xF0}, setup: func(a *APU) { a.Processor.X = 0x04 }, carry: true},
		{name: "SBC A, abs+X", code: []uint8{0xB5, 0xF0, 0x00}, setup: func(a *APU) { a.Processor.X = 0x04 }, carry: true},
		{name: "SBC A, abs+Y", code: []uint8{0xB6, 0xF0, 0x00}, setup: func(a *APU) { a.Processor.Y = 0x04 }, carry: true},
		{name: "SBC A, (dp)+Y", code: []uint8{0xB7, 0x20}, setup: setupPortIndirectIndexed, carry: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewAPU()
			a.Control = 0
			copy(a.RAM[0x0200:], tt.code)
			a.Processor.PC = 0x0200
			a.Processor.A = 0x10
			a.Processor.C = tt.carry
			a.InPorts[0] = 0x01
			if tt.setup != nil {
				tt.setup(a)
			}
			a.SetPortComparePatch(true)

			a.Run()
			if a.pending == 0 {
				t.Fatal("arithmetic instruction retired before port write")
			}

			a.WritePort(0, 0x80)
			if got := a.Processor.A; got != 0x90 {
				t.Fatalf("patched A = %02X, want 90", got)
			}
			if !a.Processor.N || a.Processor.Z || a.Processor.C {
				t.Fatalf("patched flags N=%v Z=%v C=%v, want true false false", a.Processor.N, a.Processor.Z, a.Processor.C)
			}
		})
	}
}

func TestWritePortPatchesPendingRemainingLogicPortReads(t *testing.T) {
	tests := []struct {
		name  string
		code  []uint8
		setup func(*APU)
		a     uint8
		want  uint8
	}{
		{name: "OR A, (X)", code: []uint8{0x06}, setup: func(a *APU) { a.Processor.X = 0xF4 }, a: 0x01, want: 0x81},
		{name: "OR A, (dp+X)", code: []uint8{0x07, 0x20}, setup: setupPortIndexedIndirect, a: 0x01, want: 0x81},
		{name: "OR A, abs", code: []uint8{0x05, 0xF4, 0x00}, a: 0x01, want: 0x81},
		{name: "OR A, abs+X", code: []uint8{0x15, 0xF0, 0x00}, setup: func(a *APU) { a.Processor.X = 0x04 }, a: 0x01, want: 0x81},
		{name: "OR A, abs+Y", code: []uint8{0x16, 0xF0, 0x00}, setup: func(a *APU) { a.Processor.Y = 0x04 }, a: 0x01, want: 0x81},
		{name: "OR A, (dp)+Y", code: []uint8{0x17, 0x20}, setup: setupPortIndirectIndexed, a: 0x01, want: 0x81},
		{name: "AND A, (X)", code: []uint8{0x26}, setup: func(a *APU) { a.Processor.X = 0xF4 }, a: 0xF0, want: 0x80},
		{name: "AND A, (dp+X)", code: []uint8{0x27, 0x20}, setup: setupPortIndexedIndirect, a: 0xF0, want: 0x80},
		{name: "AND A, abs", code: []uint8{0x25, 0xF4, 0x00}, a: 0xF0, want: 0x80},
		{name: "AND A, abs+X", code: []uint8{0x35, 0xF0, 0x00}, setup: func(a *APU) { a.Processor.X = 0x04 }, a: 0xF0, want: 0x80},
		{name: "AND A, abs+Y", code: []uint8{0x36, 0xF0, 0x00}, setup: func(a *APU) { a.Processor.Y = 0x04 }, a: 0xF0, want: 0x80},
		{name: "AND A, (dp)+Y", code: []uint8{0x37, 0x20}, setup: setupPortIndirectIndexed, a: 0xF0, want: 0x80},
		{name: "EOR A, (X)", code: []uint8{0x46}, setup: func(a *APU) { a.Processor.X = 0xF4 }, a: 0x81, want: 0x01},
		{name: "EOR A, (dp+X)", code: []uint8{0x47, 0x20}, setup: setupPortIndexedIndirect, a: 0x81, want: 0x01},
		{name: "EOR A, abs", code: []uint8{0x45, 0xF4, 0x00}, a: 0x81, want: 0x01},
		{name: "EOR A, abs+X", code: []uint8{0x55, 0xF0, 0x00}, setup: func(a *APU) { a.Processor.X = 0x04 }, a: 0x81, want: 0x01},
		{name: "EOR A, abs+Y", code: []uint8{0x56, 0xF0, 0x00}, setup: func(a *APU) { a.Processor.Y = 0x04 }, a: 0x81, want: 0x01},
		{name: "EOR A, (dp)+Y", code: []uint8{0x57, 0x20}, setup: setupPortIndirectIndexed, a: 0x81, want: 0x01},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewAPU()
			a.Control = 0
			copy(a.RAM[0x0200:], tt.code)
			a.Processor.PC = 0x0200
			a.Processor.A = tt.a
			a.InPorts[0] = 0x10
			if tt.setup != nil {
				tt.setup(a)
			}
			a.SetPortComparePatch(true)

			a.Run()
			if a.pending == 0 {
				t.Fatal("logic instruction retired before port write")
			}

			a.WritePort(0, 0x80)
			if got := a.Processor.A; got != tt.want {
				t.Fatalf("patched A = %02X, want %02X", got, tt.want)
			}
		})
	}
}

func setupPortIndexedIndirect(a *APU) {
	a.Processor.X = 0x04
	a.RAM[0x24] = 0xF4
	a.RAM[0x25] = 0x00
}

func setupPortIndirectIndexed(a *APU) {
	a.Processor.Y = 0x04
	a.RAM[0x20] = 0xF0
	a.RAM[0x21] = 0x00
}

func TestWritePortDoesNotPatchAfterNextInstructionStarts(t *testing.T) {
	a := NewAPU()
	a.Control = 0
	a.RAM[0x0200] = 0x7E // CMP Y, dp
	a.RAM[0x0201] = 0xF4
	a.RAM[0x0202] = 0x00 // NOP
	a.Processor.PC = 0x0200
	a.Processor.Y = 0x10
	a.InPorts[0] = 0x10
	a.SetPortComparePatch(true)

	a.Run()
	for a.pending != 0 {
		a.Run()
	}
	a.Run()

	a.WritePort(0, 0x11)
	if !a.Processor.Z || !a.Processor.C {
		t.Fatalf("cleared cmp flags Z=%v C=%v, want true true", a.Processor.Z, a.Processor.C)
	}
}

func TestWritePortPatchesPendingCMPDirectImmediate(t *testing.T) {
	a := NewAPU()
	a.Control = 0
	a.RAM[0x0200] = 0x78 // CMP dp, #imm
	a.RAM[0x0201] = 0xCC
	a.RAM[0x0202] = 0xF4
	a.Processor.PC = 0x0200
	a.SetPortComparePatch(true)

	a.Run()
	if a.pending == 0 {
		t.Fatal("cmp instruction retired before port write")
	}
	if a.Processor.Z || a.Processor.C {
		t.Fatalf("initial cmp flags Z=%v C=%v, want false false", a.Processor.Z, a.Processor.C)
	}

	a.WritePort(0, 0xCC)
	if !a.Processor.Z || !a.Processor.C || a.Processor.N {
		t.Fatalf("patched cmp flags Z=%v C=%v N=%v, want true true false", a.Processor.Z, a.Processor.C, a.Processor.N)
	}
}
