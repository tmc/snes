package updsp

import "testing"

func TestALU_ArithmeticFlags(t *testing.T) {
	tests := []struct {
		name                    string
		op                      uint8
		acc, lhs, cIn           uint16
		wantRes                 uint16
		wantCarry, wantOverflow bool
	}{
		{name: "ADD no carry", op: aluADD, acc: 0x0001, lhs: 0x0001, wantRes: 0x0002},
		{name: "ADD carry out", op: aluADD, acc: 0xFFFF, lhs: 0x0001, wantRes: 0x0000, wantCarry: true},
		{name: "ADD signed overflow", op: aluADD, acc: 0x7FFF, lhs: 0x0001, wantRes: 0x8000, wantOverflow: true},
		{name: "SUB", op: aluSUB, acc: 0x0003, lhs: 0x0001, wantRes: 0x0002},
		{name: "SUB borrow", op: aluSUB, acc: 0x0000, lhs: 0x0001, wantRes: 0xFFFF, wantCarry: true},
		{name: "AND", op: aluAND, acc: 0xF0F0, lhs: 0x0FF0, wantRes: 0x00F0},
		{name: "OR", op: aluOR, acc: 0xF000, lhs: 0x0001, wantRes: 0xF001},
		{name: "XOR", op: aluXOR, acc: 0xFF00, lhs: 0x0FF0, wantRes: 0xF0F0},
		{name: "INC", op: aluINC, acc: 0x0010, wantRes: 0x0011},
		{name: "DEC", op: aluDEC, acc: 0x0010, wantRes: 0x000F},
		{name: "SHR1 preserves sign", op: aluSHR1, acc: 0x8002, wantRes: 0xC001},
		{name: "SHR1 carry out", op: aluSHR1, acc: 0x0001, wantRes: 0x0000, wantCarry: true},
		{name: "SHL1 with cIn", op: aluSHL1, acc: 0x0001, cIn: 1, wantRes: 0x0003},
		{name: "SHL1 carry out", op: aluSHL1, acc: 0x8000, wantRes: 0x0000, wantCarry: true},
		{name: "XCHG", op: aluXCHG, acc: 0x12AB, wantRes: 0xAB12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, carry, ov := alu(tt.op, tt.acc, 0, tt.lhs, tt.cIn)
			if res != tt.wantRes {
				t.Errorf("result = %#04x, want %#04x", res, tt.wantRes)
			}
			if carry != tt.wantCarry {
				t.Errorf("carry = %v, want %v", carry, tt.wantCarry)
			}
			if ov != tt.wantOverflow {
				t.Errorf("overflow = %v, want %v", ov, tt.wantOverflow)
			}
		})
	}
}

func TestAddSubFlags_SignedOverflow(t *testing.T) {
	// Adding two negatives that produce a positive should overflow.
	res, carry, ov := addWithFlags(0x8000, 0x8000, 0)
	if res != 0 || !carry || !ov {
		t.Errorf("-32768 + -32768: got (%#04x,%v,%v), want (0,true,true)", res, carry, ov)
	}
	// Subtracting a positive from a negative that crosses zero overflows.
	res, borrow, ov := subWithFlags(0x8000, 0x0001, 0)
	if res != 0x7FFF || borrow || !ov {
		t.Errorf("-32768 - 1: got (%#04x,borrow=%v,ov=%v), want (0x7FFF,false,true)", res, borrow, ov)
	}
}
