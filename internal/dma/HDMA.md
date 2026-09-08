# HDMA ordering and timing boundary

Reference: bsnes `9144b5ac557f6bd62aacefb87ee31cf5e12a476b`,
`sfc/cpu/dma.cpp` and `sfc/cpu/timing.cpp`. The production controller now suspends CPU bus cycles and advances timed DMA
phases. Qualification below distinguishes synthetic phase witnesses from
remaining console-wide timing work.

`hdmaRun` transfers all active channels in channel order, then advances/reloads
all active channels in channel order. A reload performs an A-bus read even when
the line count does not need a new descriptor. Only an expired count consumes
that descriptor and increments the table pointer. An indirect zero terminator
still consumes one indirect byte; it consumes the second byte only if a later
channel remains enabled and incomplete. The one-byte case leaves that byte in
the high half of the indirect-address register. The reference itself marks the
zeroed low half of that intermediate value as requiring hardware verification;
this implementation follows the pinned reference behavior.

Production now follows that order, updates the visible table and indirect
pointers, honors writes to those pointers and the line counter, handles reverse
HDMA and eight-bit B-bus address wrapping, and suppresses invalid WRAM-to-WRAM
B-bus pairs. The raw `0x80` line counter decrements to `0x7f`, so its remaining
127 lines do not repeat the initial transfer. Per-channel continuation remains
in the existing serialized state.

Tests observe the ordered reads and writes, descriptor-dependent channel
continuation, pointer readback, reverse transfers, invalid endpoint suppression,
128-line behavior and future operation order after restore. These are synthetic
bus witnesses; they do not establish game-level HDMA timing.

## Reference costs and event order

| Work | Reference master clocks, excluding arbitration alignment |
| --- | --- |
| Global setup or scanline run | 8 |
| A-bus descriptor/dummy read | 4 before read, 4 after read |
| Each transfer byte | 4 before source read, 4 after read, then destination write |
| Active channel without a transfer | 8 for its mandatory reload read |
| Direct active channel with N transferred bytes | 8 × N + 8 reload read |
| Reloading a nonterminating indirect descriptor | 16 additional clocks for two pointer reads |
| Indirect zero terminator on last active channel | 8 additional clocks for one pointer read |
| Indirect zero terminator with a later active channel | 16 additional clocks for two pointer reads |

There is no separate additional eight-clock HDMA per-channel fee beyond the
reload read. For example, single-channel direct mode0 costs 24 clocks per
transfer line (global8 + byte8 + reload8), while a skipped line costs16.
Single-channel direct setup costs16; ordinary indirect setup costs32; an
indirect terminating-last setup costs24.

## Production suspension contract

CPU reads, writes and internal instruction cycles call `BusEdge` before their
first clocks. The first edge with pending DMA arms ownership and lets one full
CPU cycle run. The following edge drains `DMA.RunSlice` before the interrupted
cycle resumes. `BeforeExecute` remains an independent diagnostic hook after
opcode fetch. Waiting CPU cycles also offer bus edges; stopped/faulted CPU
handling and the existing approximate interrupt sequence are not requalified.

The PPU only calls `RequestHDMA`: setup at line0 H=`12 + frameStart%8` for the
version2 divider, scanline work at H=1104 while V<`vdisp`. Requests carry their
beam timestamps, so a PPU event up to one dot ahead cannot steal the CPU bus
early. Completed or disabled channels do not acquire the bus on run events.
The current implementation supports the version2 setup formula, not version1.

DMA owns entry alignment, global waits, channel traversal, descriptor/indirect
reads, transfers and exit alignment. Entry costs `8 - CPUclock%8`; exit costs
`interruptedCycle - DMAclocks%interruptedCycle`, including a full period at
exact alignment. Each read waits four clocks, latches its value, then waits
four more before using it or writing the destination. All eight channels and
modes, both directions and indirect addressing use the same production path.
HDMA interrupts general DMA between bytes and can cancel its own channel.
General DMA's size decrement happens after that edge, matching the reference's
short-circuit on cancellation. If HDMA cancels the last enabled channel during
an already-running general transfer, both the inner HDMA return and outer DMA
return resynchronize. These CPU waits do not increment the DMA divider count.

`Scheduler.AddDMACycles` advances the suspended CPU without another CPU edge,
then synchronizes devices. PPU synchronization cannot execute DMA bus work.
Cartridge clocks are delivered during DMA waits; instruction retirement
subtracts those delivered clocks so they are counted once. Reset clears DMA
ownership and peripheral timelines before CPU reset-vector reads advance time.
The old immediate helpers remain available for isolated register/bus tests;
they are not the system's production dispatch path.

## State and evidence boundaries

`ExecutionState` stores pending/armed requests, timestamps, current/return
phases, channel/byte positions, interrupted-cycle length, DMA divider count,
remaining wait, bus endpoints and the latched byte. `RunSlice` can stop after
either read half and resume from `DMA.SaveState`. `LoadState` validates phase-specific selectors and continuation targets before
mutating the controller; `ValidateExecution` is also available for preflight. The host remains responsible for restoring the matching
bus and clock snapshot alongside the controller.

Production drains synchronously inside the current CPU instruction's Go stack.
That stack is **not** serialized. `System.Serialize` and `Unserialize` reject
active CPU/DMA execution, and system-state admission rejects an in-flight DMA
continuation without a CPU continuation. Legitimate instruction-boundary saves
retain pending and armed ownership across the root gob round trip. Concurrent
calls into a running system remain unsupported.

Synthetic tests qualify entry/exit alignment, global and half-read charges,
setup/run ordering, all channel/mode/direction/indirect combinations, completed
channel suppression, GDMA cancellation and per-clock save/resume for setup,
transfer and general DMA. Root witnesses exercise actual CPU instructions,
request-only PPU callbacks, diagnostic hooks, save rejection and repeated reset
with stale armed requests. Scheduler tests verify delivery before retirement
without duplicate cartridge clocks. Removing the global wait or the second
read half fails the exact timestamp witness.

This is not a complete 5A22 timing qualification: four-clock interrupt polling,
IRQ locking after DMA, CPU version1 setup, the existing DRAM-refresh model and
full mid-instruction system save/resume remain outstanding. No game/audio
phase golden was rebaselined to claim those boundaries. Selected existing
input, Mode7, OAM and write-trace parity checks pass separately from the
synthetic DMA clock witnesses.

## Test engine ownership

Semantic and source-literal bus-order tests enter the production timed engine
through `Request`/`RequestHDMA`, two `BeginEdge` calls and `RunSlice`. The old
immediate general-DMA implementation is removed. The old immediate HDMA model
exists only in `untimed_hdma_test.go` as the independent `untimedHDMA` oracle
for the transfer-mode/direction matrix. It has its own descriptor, channel-order
and transfer loops and never calls the timed engine. It shares address/mode
helpers, whose results are independently pinned by literal expected bus traces.
