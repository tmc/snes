package emulator

// Information contains metadata about the emulated system.
type Information struct {
	Manufacturer string
	Name         string
	Extension    string
	Resettable   bool
}

// DisplayType represents the technology of the display.
type DisplayType int

const (
	DisplayTypeCRT DisplayType = iota
	DisplayTypeLCD
)

// Display contains information about the video output.
type Display struct {
	ID               uint
	Name             string
	Type             DisplayType
	Colors           uint
	Width            uint
	Height           uint
	InternalWidth    uint
	InternalHeight   uint
	AspectCorrection float64
}

// Port represents a physical connector on the console.
type Port struct {
	ID   uint
	Name string
}

// Device represents a peripheral that can be connected to a port.
type Device struct {
	ID   uint
	Name string
}

// InputType represents the physical nature of an input.
type InputType int

const (
	InputTypeHat InputType = iota
	InputTypeButton
	InputTypeTrigger
	InputTypeControl
	InputTypeAxis
	InputTypeRumble
)

// Input represents a specific input on a device.
type Input struct {
	Type InputType
	Name string
}

// Standard controller button bit layout.
const (
	StandardButtonB      uint16 = 1 << 15
	StandardButtonY      uint16 = 1 << 14
	StandardButtonSelect uint16 = 1 << 13
	StandardButtonStart  uint16 = 1 << 12
	StandardButtonUp     uint16 = 1 << 11
	StandardButtonDown   uint16 = 1 << 10
	StandardButtonLeft   uint16 = 1 << 9
	StandardButtonRight  uint16 = 1 << 8
	StandardButtonA      uint16 = 1 << 7
	StandardButtonX      uint16 = 1 << 6
	StandardButtonL      uint16 = 1 << 5
	StandardButtonR      uint16 = 1 << 4
)
