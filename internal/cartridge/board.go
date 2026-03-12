package cartridge

import "github.com/tmc/snes/internal/bus"

// Board is a cartridge board mapped onto the SNES bus.
type Board interface {
	bus.MemoryDevice
	MapToBus(*bus.Bus)
	Step(masterCycles uint64)
	SaveRAM() []byte
	LoadSaveRAM(data []byte) error
	Serialize() ([]byte, error)
	Unserialize(data []byte) error
}

// Coprocessor is a time-stepped cartridge coprocessor.
type Coprocessor interface {
	Step(masterCycles uint64)
	Serialize() ([]byte, error)
	Unserialize(data []byte) error
}
