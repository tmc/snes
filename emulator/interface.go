package emulator

// Interface is the contract that an emulator core must implement.
// It is composed of smaller, focused interfaces.
type Interface interface {
	Core
	Video
	Audio
	InputDriver
	State
	Cheat
	Configuration
}

// Core defines the fundamental lifecycle and information methods.
type Core interface {
	Loader
	Runner
	Information() Information
}

// Loader handles loading and unloading of game media.
type Loader interface {
	Loaded() bool
	Hashes() []string
	Manifests() []string
	Titles() []string
	Title() string
	Load() bool
	Unload()
}

// Runner handles the execution control of the emulator.
type Runner interface {
	Power()
	Reset()
	Run()
}

// Video handles video output and color conversion.
type Video interface {
	Display() Display
	// Color converts a 32-bit internal color to a 64-bit platform color (if needed).
	Color(color uint32) uint64
}

// Audio handles audio synchronization.
type Audio interface {
	// Synchronize synchronizes the audio system to the given timestamp.
	Synchronize(timestamp uint64)
}

// InputDriver handles controller ports and devices.
type InputDriver interface {
	Ports() []Port
	Devices(port uint) []Device
	Inputs(device uint) []Input // Returns []Input struct from types.go
	Connected(port uint) uint
	Connect(port, device uint)
}

// State handles save states.
type State interface {
	// Serialize serializes the emulator state.
	// We use []byte instead of a custom serializer class for Go idiomaticy.
	Serialize(synchronize bool) []byte
	// Unserialize restores the emulator state.
	Unserialize(data []byte) bool
}

// Cheat handles cheat codes.
type Cheat interface {
	Read(address uint32) uint8
	Cheats(cheats []string)
}

// Configuration handles emulator settings and options.
type Configuration interface {
	ConfigurationName() string
	Configuration(name string) string
	Configure(configuration string) bool
	ConfigureOption(name, value string) bool
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
