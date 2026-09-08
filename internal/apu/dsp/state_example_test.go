package dsp

import "fmt"

func ExampleDSP_LoadState() {
	d := New()
	d.Write(0x0c, 64)
	state := d.SaveState()
	d.Write(0x0c, 0)
	if err := d.LoadState(state); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(d.MVOLL)
	// Output: 64
}

func ExampleValidateState() {
	state := New().SaveState()
	fmt.Println(ValidateState(state))
	// Output: <nil>
}
