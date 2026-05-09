package bus

// MemoryDevice represents any component that can be read from or written to via the bus.
type MemoryDevice interface {
	Read(address uint32) uint8
	Write(address uint32, value uint8)
	BlockRead(address uint32, length int) []byte
}

// Bus handles address routing between the CPU and various components.
type Bus struct {
	// pages is a [256][256]Page table.
	// The outer index is the Bank (00-FF), the inner is the Page (00-FF).
	pages [256][256]MemoryDevice

	// MDR (Memory Data Register) holds the residual value on the bus.
	// It is updated on every Read and Write.
	MDR uint8

	// waitStates is a [256][256]uint8 table storing cycle costs per page.
	// We use a lookup table for speed in the hot path.
	// Updates to MEMSEL will trigger a re-population of high banks.
	waitStates [256][256]uint8

	// MEMSEL register value ($420D)
	MEMSEL uint8

	// WriteHook, if non-nil, is invoked on every Write before the device sees
	// the value. Intended for parity tracing; keep nil on the hot path.
	WriteHook func(address uint32, value uint8)

	// ReadHook, if non-nil, is invoked on every Read after the device returns
	// its value. Intended for parity tracing; keep nil on the hot path.
	ReadHook func(address uint32, value uint8)
}

func NewBus() *Bus {
	b := &Bus{}
	b.Clear()
	b.InitializeWaitStates()
	return b
}

// Clear resets all pages to unmapped OpenBusDevice.
func (b *Bus) Clear() {
	openBus := NewOpenBusDevice(b)
	for bank := 0; bank < 256; bank++ {
		for page := 0; page < 256; page++ {
			b.pages[bank][page] = openBus
		}
	}
}

// Map maps a device to a specific range.
// Simplification: We map by Page (256 bytes).
// The range usually aligns. If not, we might need finer granularity or just handle pages.
// SNES mapping is typically Bank/Page aligned.
func (b *Bus) Map(start, end uint32, device MemoryDevice) {
	startBank := (start >> 16) & 0xFF
	endBank := (end >> 16) & 0xFF

	// Iterate banks
	for bank := startBank; bank <= endBank; bank++ {
		startPage := uint32(0)
		endPage := uint32(0xFF)

		if bank == startBank {
			startPage = (start >> 8) & 0xFF
		}
		if bank == endBank {
			endPage = (end >> 8) & 0xFF
		}

		for page := startPage; page <= endPage; page++ {
			b.pages[bank][page] = device
		}
	}
}

func (b *Bus) Read(address uint32) uint8 {
	bank := (address >> 16) & 0xFF
	page := (address >> 8) & 0xFF

	device := b.pages[bank][page]
	// Wait states? Handled by CPU using GetWaitStates.
	// But we need to define them.

	if device != nil {
		b.MDR = device.Read(address)
	} else {
		// Fallback if something went wrong and nil is in the table
		// In a correct impl, this branch is unreachable.
	}
	if b.ReadHook != nil {
		b.ReadHook(address, b.MDR)
	}
	return b.MDR
}

func (b *Bus) Write(address uint32, value uint8) {
	b.MDR = value

	if b.WriteHook != nil {
		b.WriteHook(address, value)
	}

	bank := (address >> 16) & 0xFF
	page := (address >> 8) & 0xFF

	device := b.pages[bank][page]
	if device != nil {
		device.Write(address, value)
	}
}

// OpenBusDevice and WaitStates are defined in separate files.

func (b *Bus) GetPage(bank, page uint32) MemoryDevice {
	return b.pages[bank][page]
}
