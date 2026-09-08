package cartridge

import "fmt"

func ExampleCartridge_Power() {
	c := &Cartridge{RAM: make([]byte, 0x2000)}
	c.RAM[0] = 42
	c.Power()
	fmt.Println(c.RAM[0])
	// Output: 42
}
