package dispatchbus_test

import (
	"fmt"
	"github.com/tmc/snes/internal/recovery/dispatchbus"
)

func ExampleState() {
	s := dispatchbus.State{PC: 0x9000, P: 0x30}
	fmt.Printf("%04x\n", s.PC)
	// Output: 9000
}
func ExampleInstruction() {
	in := dispatchbus.Instruction{Bytes: []byte{0x7a}}
	fmt.Printf("%02x\n", in.Bytes[0])
	// Output: 7a
}
func ExampleAccess() {
	a := dispatchbus.Access{Schema: 2, Width: 1, ValueKnown: true}
	fmt.Println(a.Width, a.ValueKnown)
	// Output: 1 true
}
