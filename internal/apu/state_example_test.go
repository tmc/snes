package apu

import "fmt"

func ExampleValidateState() {
	state := NewAPU().SaveState()
	fmt.Println(ValidateState(state))
	// Output: <nil>
}

func ExampleAPUInputReadState() {
	a := NewAPU()
	a.Control = 0
	a.Processor.PC = 0x200
	copy(a.RAM[0x200:], []byte{0xe4, 0xf4})
	a.RunUntilTarget(5, SyncPortWrite)
	state := a.SaveState()
	fmt.Println(state.ClockVersion, state.InputRead.Phase, state.InputRead.Waiting)
	// Output: 1 5 true
}
