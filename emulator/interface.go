package emulator

// Interface is the contract that an emulator core must implement.
type Interface interface {
	Core
	Video
	Audio
	InputDriver
	State
}

// Core defines the fundamental lifecycle and metadata methods.
type Core interface {
	Information() Information
	Loaded() bool
	LoadROM(data []byte) error
	Unload()
	Power()
	Reset()
	RunFrame() error
}

// Video handles video output.
type Video interface {
	Display() Display
	FrameBuffer() []uint16
}

// Audio handles audio synchronization.
type Audio interface {
	DrainAudio(dst []int16) int
}

// InputDriver handles controller ports and devices.
type InputDriver interface {
	Ports() []Port
	Devices(port uint) []Device
	Inputs(port, device uint) []Input
	Connected(port uint) uint
	Connect(port, device uint) error
	SetInputState(port uint, state uint16) error
}

// State handles save states.
type State interface {
	Serialize() ([]byte, error)
	Unserialize(data []byte) error
	SaveRAM() []byte
	LoadSaveRAM(data []byte) error
}

// Memory handles passive memory inspection.
type Memory interface {
	ReadWRAMAt(p []byte, off int64) (int, error)
}

// Capabilities handles capability flags and generic getters/setters.
type Capabilities interface {
	Cap(name string) bool
	Get(name string) interface{}
	Set(name string, value interface{}) bool
}

// Settings handles specific core settings.
type Settings interface {
	FrameSkip() uint
	SetFrameSkip(frameSkip uint)

	RunAhead() bool
	SetRunAhead(runAhead bool)
}
