package spc700

import "fmt"

func ExampleSPC700_ClearPendingPortOperation() {
	c := New(nil)
	c.ArmPendingPortLoadA(0xf4)
	c.ClearPendingPortOperation()
	c.PatchPortWrite(0xf4, 0x5a)
	fmt.Println(c.A)
	// Output: 0
}
