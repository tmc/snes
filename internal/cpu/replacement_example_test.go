package cpu

import (
	"fmt"

	"github.com/tmc/snes/internal/bus"
)

func ExampleInstructionExecutor() {
	c := NewCPU(bus.NewBus())
	c.PC = 0x8000
	c.P = 0x30
	c.ReplaceInstruction = func(address uint32, opcode uint8) InstructionExecutor {
		if address != 0x8000 {
			return nil
		}
		return func(io *InstructionIO) (Snapshot, error) {
			if err := io.Idle(6); err != nil {
				return Snapshot{}, err
			}
			state := io.State()
			state.A = 42
			return state, nil
		}
	}
	c.Step()
	fmt.Println(c.A, c.Fault == nil)
	// Output: 42 true
}
