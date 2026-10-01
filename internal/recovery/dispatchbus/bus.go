package dispatchbus

import "fmt"

// State contains CPU fields needed to derive data accesses.
type State struct {
	A, Y, S, D uint16
	PB, DB, P  byte
	PC         uint16
	E          bool
	Cycles     uint64
}

// Instruction contains a fixture-verified retired instruction.
type Instruction struct {
	Entry, Exit State
	Bytes       []byte
	Fetches     []uint32
}

// Access is a normalized measured byte access. ValueKnown records schema-aware
// decoding, including schema 2's omitted zero read value.
type Access struct {
	ID, Cycle        uint64
	Space, Op, Actor string
	Address          uint32
	Value            byte
	PC               uint32
	Opcode           byte
	Bytes            []byte
	ROMOffset        uint32
	ROM              bool
	Schema, Width    int
	ValueKnown       bool
}

// Verify checks exact data access order and values against measured instructions
// and owned pinned LoROM bytes. Instruction fetches are checked by its caller.
// Native binary mode, D=0, low bank-zero WRAM scratch and stack are supported.
// The caller must verify instruction fetches and full CPU continuity against
// its pinned fixture before calling Verify.
func Verify(insns []Instruction, accesses []Access, rom []byte) error {
	if len(insns) == 0 || len(insns) > 64 || len(accesses) > 1<<20 || len(rom) == 0 {
		return fmt.Errorf("helper bus: invalid bounds")
	}
	for i := 1; i < len(accesses); i++ {
		if accesses[i].Cycle < accesses[i-1].Cycle || accesses[i].ID <= accesses[i-1].ID {
			return fmt.Errorf("helper bus: non-monotonic access stream")
		}
	}
	memory := map[uint32]byte{}
	for _, in := range insns {
		if len(in.Bytes) == 0 || len(in.Bytes) > 4 || len(in.Fetches) != len(in.Bytes) || in.Entry.Cycles >= in.Exit.Cycles || in.Entry.E || in.Entry.D != 0 || in.Entry.P&8 != 0 {
			return fmt.Errorf("helper bus: unsupported CPU context")
		}
		pc := uint32(in.Entry.PB)<<16 | uint32(in.Entry.PC)
		op := in.Bytes[0]
		var data []Access
		for _, a := range accesses {
			if a.Cycle <= in.Entry.Cycles || a.Cycle > in.Exit.Cycles {
				continue
			}
			fetch := false
			if a.Op == "read" && a.Space == "cpu" && a.ROM {
				for k, f := range in.Fetches {
					if a.Address == f && k < len(in.Bytes) && a.Value == in.Bytes[k] {
						fetch = true
						break
					}
				}
			}
			if fetch {
				continue
			}
			if a.Schema != 2 || a.Width != 1 || !a.ValueKnown {
				return fmt.Errorf("helper bus: unsupported byte payload at $%06x", pc)
			}
			if a.Actor != "cpu" || a.PC != pc || a.Opcode != op {
				return fmt.Errorf("helper bus: CPU context mismatch at $%06x", pc)
			}
			if len(a.Bytes) == 0 || len(a.Bytes) > len(in.Bytes) {
				return fmt.Errorf("helper bus: missing CPU byte context at $%06x", pc)
			}
			for k, b := range a.Bytes {
				if b != in.Bytes[k] {
					return fmt.Errorf("helper bus: CPU byte context mismatch at $%06x", pc)
				}
			}
			if len(data) > 0 && (a.Cycle < data[len(data)-1].Cycle || a.ID <= data[len(data)-1].ID) {
				return fmt.Errorf("helper bus: non-monotonic accesses at $%06x", pc)
			}
			data = append(data, a)
		}
		cursor := 0
		use := func(space, kind string, addr uint32, value *byte) (byte, error) {
			if cursor >= len(data) {
				return 0, fmt.Errorf("helper bus: missing %s at $%06x for $%06x", kind, addr, pc)
			}
			a := data[cursor]
			cursor++
			if a.Space != space || a.Op != kind || a.Address != addr {
				return 0, fmt.Errorf("helper bus: address/order mismatch at $%06x", pc)
			}
			if space == "cpu" {
				off, ok := romOffset(addr, len(rom))
				if !a.ROM || !ok || a.ROMOffset != uint32(off) || a.Value != rom[off] {
					return 0, fmt.Errorf("helper bus: ROM data mismatch at $%06x", pc)
				}
			}
			if value != nil && a.Value != *value {
				return 0, fmt.Errorf("helper bus: value mismatch at $%06x", pc)
			}
			if space == "wram" {
				if addr >= 0x2000 {
					return 0, fmt.Errorf("helper bus: unsupported WRAM address")
				}
				if kind == "read" {
					v, ok := memory[addr]
					if !ok || v != a.Value {
						return 0, fmt.Errorf("helper bus: scratch lineage mismatch at $%06x", pc)
					}
				} else {
					memory[addr] = a.Value
				}
			}
			return a.Value, nil
		}
		write := func(addr uint32, val uint16, n int) error {
			for k := 0; k < n; k++ {
				b := byte(val >> uint(8*k))
				if _, e := use("wram", "write", uint32(uint16(addr+uint32(k))), &b); e != nil {
					return e
				}
			}
			return nil
		}
		read := func(addr uint32, n int) (uint32, error) {
			var v uint32
			for k := 0; k < n; k++ {
				b, e := use("wram", "read", uint32(uint16(addr+uint32(k))), nil)
				if e != nil {
					return 0, e
				}
				v |= uint32(b) << uint(8*k)
			}
			return v, nil
		}
		na, nx := 1, 1
		if in.Entry.P&0x20 == 0 {
			na = 2
		}
		if in.Entry.P&0x10 == 0 {
			nx = 2
		}
		operand := func() (uint32, error) {
			if len(in.Bytes) < 2 {
				return 0, fmt.Errorf("helper bus: missing operand")
			}
			return uint32(in.Bytes[1]), nil
		}
		var err error
		switch op {
		case 0x22:
			if len(in.Bytes) != 4 {
				return fmt.Errorf("helper bus: invalid JSL")
			}
			saved := in.Entry.PC + 3
			vals := []byte{in.Entry.PB, byte(saved >> 8), byte(saved)}
			for k, b := range vals {
				_, err = use("wram", "write", uint32(in.Entry.S-uint16(k)), &b)
				if err != nil {
					break
				}
			}
		case 0x84, 0x85:
			var addr uint32
			addr, err = operand()
			if err == nil {
				v, n := in.Entry.Y, nx
				if op == 0x85 {
					v, n = in.Entry.A, na
				}
				err = write(addr, v, n)
			}
		case 0x7a, 0x68:
			n := nx
			if op == 0x68 {
				n = na
			}
			var v uint32
			v, err = read(uint32(in.Entry.S+1), n)
			if err == nil {
				want := uint16(v)
				got := in.Exit.Y
				if op == 0x68 {
					got = in.Exit.A
					if n == 1 {
						want |= in.Entry.A & 0xff00
					}
				}
				if got != want || in.Exit.S != in.Entry.S+uint16(n) {
					err = fmt.Errorf("helper bus: pull result mismatch at $%06x", pc)
				}
			}
		case 0xb7:
			var addr, pointer uint32
			addr, err = operand()
			if err == nil {
				pointer, err = read(addr, 3)
			}
			var value uint16
			if err == nil {
				for k := 0; k < na; k++ {
					b, e := use("cpu", "read", (pointer+uint32(in.Entry.Y)+uint32(k))&0xffffff, nil)
					if e != nil {
						err = e
						break
					}
					value |= uint16(b) << uint(8*k)
				}
			}
			if err == nil {
				if na == 1 {
					value |= in.Entry.A & 0xff00
				}
				if in.Exit.A != value {
					err = fmt.Errorf("helper bus: table result mismatch at $%06x", pc)
				}
			}
		case 0xa4:
			var addr, v uint32
			addr, err = operand()
			if err == nil {
				v, err = read(addr, nx)
			}
			if err == nil && in.Exit.Y != uint16(v) {
				err = fmt.Errorf("helper bus: LDY result mismatch at $%06x", pc)
			}
		case 0xdc:
			if len(in.Bytes) != 3 {
				return fmt.Errorf("helper bus: invalid JML")
			}
			var v uint32
			v, err = read(uint32(in.Bytes[1])|uint32(in.Bytes[2])<<8, 3)
			if err == nil && v != uint32(in.Exit.PB)<<16|uint32(in.Exit.PC) {
				err = fmt.Errorf("helper bus: JML target mismatch at $%06x", pc)
			}
		case 0xc2, 0xe2, 0x29, 0x0a, 0xa8, 0xc8:
		default:
			return fmt.Errorf("helper bus: unsupported opcode $%02x", op)
		}
		if err != nil {
			return err
		}
		if cursor != len(data) {
			return fmt.Errorf("helper bus: unexpected data access at $%06x", pc)
		}
	}
	return nil
}
func romOffset(a uint32, n int) (int, bool) {
	if a > 0xffffff || a&0xffff < 0x8000 || a>>16 == 0x7e || a>>16 == 0x7f {
		return 0, false
	}
	off := int((a>>16&127)*32768 + (a & 32767))
	return off, off < n
}
