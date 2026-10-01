package dispatchbus

import (
	"fmt"
	"testing"
)

func ExampleVerify() {
	fmt.Println(Verify(nil, nil, nil)) // Output: helper bus: invalid bounds
}

func fixture() ([]Instruction, []Access, []byte) {
	rom := make([]byte, 4*32768)
	rom[3*32768+4] = 0x34
	rom[3*32768+5] = 0x12
	var instructions []Instruction
	var accesses []Access
	add := func(code []byte, s State, exit State, events []Access) {
		s.Cycles = uint64(len(instructions)*100 + 1)
		exit.Cycles = s.Cycles + 90
		i := Instruction{Entry: s, Exit: exit, Bytes: code}
		for k := range code {
			i.Fetches = append(i.Fetches, uint32(s.PB)<<16|uint32(s.PC+uint16(k)))
		}
		instructions = append(instructions, i)
		for k, a := range events {
			a.Schema, a.Width, a.ValueKnown = 2, 1, true
			a.ID = uint64(len(accesses) + 1)
			a.Cycle = s.Cycles + uint64(k+1)
			a.Actor = "cpu"
			a.PC = uint32(s.PB)<<16 | uint32(s.PC)
			a.Opcode = code[0]
			a.Bytes = code
			accesses = append(accesses, a)
		}
	}
	w := func(a uint32, v byte) Access { return Access{Space: "wram", Op: "write", Address: a, Value: v} }
	r := func(a uint32, v byte) Access { return Access{Space: "wram", Op: "read", Address: a, Value: v} }
	add([]byte{0x22, 0, 0x90, 0}, State{PB: 3, PC: 0x8000, S: 0x1fd, P: 0x30}, State{}, []Access{w(0x1fd, 3), w(0x1fc, 0x80), w(0x1fb, 3)})
	add([]byte{0x7a}, State{PC: 0x9000, S: 0x1fa, P: 0x30}, State{Y: 3, S: 0x1fb}, []Access{r(0x1fb, 3)})
	add([]byte{0x68}, State{PC: 0x9001, S: 0x1fb, P: 0x10}, State{A: 0x0380, S: 0x1fd}, []Access{r(0x1fc, 0x80), r(0x1fd, 3)})
	add([]byte{0x85, 1}, State{PC: 0x9002, A: 0x0380, P: 0x10}, State{}, []Access{w(1, 0x80), w(2, 3)})
	add([]byte{0x84, 0}, State{PC: 0x9004, Y: 3, P: 0x30}, State{}, []Access{w(0, 3)})
	add([]byte{0xb7, 0}, State{PC: 0x9006, Y: 1, P: 0x10}, State{A: 0x1234}, []Access{r(0, 3), r(1, 0x80), r(2, 3), {Space: "cpu", Op: "read", Address: 0x038004, Value: 0x34, ROM: true, ROMOffset: 3*32768 + 4}, {Space: "cpu", Op: "read", Address: 0x038005, Value: 0x12, ROM: true, ROMOffset: 3*32768 + 5}})
	add([]byte{0x85, 0}, State{PC: 0x9008, A: 0x1234, P: 0x10}, State{}, []Access{w(0, 0x34), w(1, 0x12)})
	add([]byte{0xdc, 0, 0}, State{PC: 0x900a, P: 0x30}, State{PB: 3, PC: 0x1234}, []Access{r(0, 0x34), r(1, 0x12), r(2, 3)})
	return instructions, accesses, rom
}
func TestVerifyHelperBus(t *testing.T) {
	in, events, rom := fixture()
	if err := Verify(in, events, rom); err != nil {
		t.Fatal(err)
	}
	for k := range events {
		t.Run(fmt.Sprintf("delete-%d", k), func(t *testing.T) {
			a := append([]Access(nil), events[:k]...)
			a = append(a, events[k+1:]...)
			if Verify(in, a, rom) == nil {
				t.Fatal("missing access accepted")
			}
		})
	}
	for _, kind := range []string{"value", "address", "order", "actor", "opcode", "PC", "prefix", "cycle", "ROM-offset", "ROM-value", "ROM-fetch", "unsupported-D", "unsupported-E", "unsupported-decimal", "missing-value", "missing-width", "wide-payload", "unknown-schema"} {
		t.Run(kind, func(t *testing.T) {
			a := append([]Access(nil), events...)
			i := append([]Instruction(nil), in...)
			switch kind {
			case "missing-value":
				a[6].ValueKnown = false
			case "missing-width":
				a[6].Width = 0
			case "wide-payload":
				a[6].Width = 2
			case "unknown-schema":
				a[6].Schema = 0
			case "value":
				a[6].Value++
			case "address":
				a[9].Address++
			case "order":
				a[4], a[5] = a[5], a[4]
			case "actor":
				a[6].Actor = "dma"
			case "opcode":
				a[6].Opcode++
			case "PC":
				a[6].PC++
			case "prefix":
				a[6].Bytes = []byte{0}
			case "cycle":
				a[6].Cycle = i[3].Entry.Cycles
			case "ROM-offset":
				a[12].ROMOffset++
			case "ROM-value":
				a[12].Value++
			case "ROM-fetch":
				a[12].Address = i[5].Fetches[1]
				a[12].Value = i[5].Bytes[1]
			case "unsupported-D":
				i[0].Entry.D = 1
			case "unsupported-E":
				i[0].Entry.E = true
			case "unsupported-decimal":
				i[0].Entry.P |= 8
			}
			if Verify(i, a, rom) == nil {
				t.Fatal("modified access accepted")
			}
		})
	}
}

func TestFetchMetadata(t *testing.T) {
	in := Instruction{Entry: State{PC: 0x8000, P: 0x30, Cycles: 1}, Exit: State{Cycles: 10}, Bytes: []byte{0xc2, 0}, Fetches: []uint32{0x8000, 0x8001}}
	rom := make([]byte, 32768)
	rom[0] = 0xc2
	base := []Access{{ID: 1, Cycle: 2, Space: "cpu", Op: "read", Actor: "cpu", Address: 0x8000, Value: 0xc2, PC: 0x8000, ROM: true, Schema: 2, Width: 1, ValueKnown: true}, {ID: 2, Cycle: 3, Space: "cpu", Op: "read", Actor: "cpu", Address: 0x8001, PC: 0x8000, Opcode: 0xc2, Bytes: []byte{0xc2}, ROM: true, ROMOffset: 1, Schema: 2, Width: 1, ValueKnown: true}}
	for _, kind := range []string{"valid", "schema", "width", "value-known", "actor", "pc", "opcode", "bytes", "offset", "duplicate", "order"} {
		t.Run(kind, func(t *testing.T) {
			a := append([]Access(nil), base...)
			switch kind {
			case "schema":
				a[1].Schema = 99
			case "width":
				a[1].Width = 2
			case "value-known":
				a[1].ValueKnown = false
			case "actor":
				a[1].Actor = "dma"
			case "pc":
				a[1].PC++
			case "opcode":
				a[1].Opcode = 0
			case "bytes":
				a[1].Bytes = nil
			case "offset":
				a[1].ROMOffset = 0
			case "duplicate":
				a = append(a, a[1])
				a[2].ID = 3
				a[2].Cycle = 4
			case "order":
				a[0], a[1] = a[1], a[0]
				a[0].ID = 1
				a[0].Cycle = 2
				a[1].ID = 2
				a[1].Cycle = 3
			}
			err := Verify([]Instruction{in}, a, rom)
			if (err == nil) != (kind == "valid") {
				t.Fatalf("%s: %v", kind, err)
			}
		})
	}
}
