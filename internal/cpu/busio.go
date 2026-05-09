package cpu

// BusIO is the minimal bus surface the WDC65816 core needs to fetch
// opcodes, read operands, write results, account for memory wait
// states, and observe/restore the open-bus MDR latch around I/O reads.
//
// *bus.Bus from internal/bus satisfies this via the adapter returned
// by NewSCPUBus. Decoupling the CPU from the concrete S-CPU bus lets
// a future 65816 derivative (e.g. SA-1) reuse the core with its own
// bus fabric.
type BusIO interface {
	Read(addr uint32) uint8
	Write(addr uint32, val uint8)
	GetWaitStates(addr uint32) uint64
	MDR() uint8
	SetMDR(val uint8)
}
