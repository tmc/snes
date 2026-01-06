package apu

// APUMainBusAdapter adapts the APU for the Main SNES Bus (uint32 address)
type APUMainBusAdapter struct {
	APU *APU
}

func (a *APUMainBusAdapter) Read(address uint32) uint8 {
	return a.APU.ReadPort(address)
}

func (a *APUMainBusAdapter) Write(address uint32, value uint8) {
	a.APU.WritePort(address, value)
}

func (a *APUMainBusAdapter) BlockRead(address uint32, length int) []byte {
	data := make([]byte, length)
	for i := 0; i < length; i++ {
		data[i] = a.Read(address + uint32(i))
	}
	return data
}
