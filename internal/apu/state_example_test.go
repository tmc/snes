package apu

import "fmt"

func ExampleValidateState() {
	state := NewAPU().SaveState()
	fmt.Println(ValidateState(state))
	// Output: <nil>
}
