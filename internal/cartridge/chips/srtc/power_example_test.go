package srtc

import "fmt"

func ExampleDevice_Power() {
	d := New(nil)
	d.Power()
	v, _ := d.Read(0x2800)
	fmt.Println(v)
	// Output: 15
}
