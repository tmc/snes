package cx4

import "fmt"

func ExampleDevice_Power() {
	d := New(nil)
	d.Write(0x6000, 42)
	d.Power()
	v, _ := d.Read(0x6000)
	fmt.Println(v)
	// Output: 0
}
