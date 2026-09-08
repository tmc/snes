# HDMA ordering and timing boundary

Reference: bsnes `9144b5ac557f6bd62aacefb87ee31cf5e12a476b`,
`sfc/cpu/dma.cpp` and `sfc/cpu/timing.cpp`. The production changes here qualify
bus-operation order and register continuation, not elapsed HDMA time.

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

Those totals are insufficient to schedule production safely:

1. The reference CPU samples setup once in line0 at a version/alignment-dependent
   position (`12 + dmaCounter()` for CPU version2), and scanline HDMA at H=1104
   on lines below `vdisp`. The current PPU calls reset at field wrap and executes
   HDMA at dot274 (H=1096), inside PPU synchronization.
2. `dmaEdge` first arms pending ownership, then enters at a CPU bus-cycle edge.
   It performs DMA-counter alignment before work and aligns to the interrupted
   CPU cycle afterward. An active HDMA channel can cancel general DMA on the
   same channel; these decisions occur between DMA bytes.
3. The current CPU/scheduler interface often discovers a PPU event after the CPU
   instruction has already executed. Calling `Scheduler.AddCycles` from inside
   `PPU.Run` re-enters PPU synchronization. Charging at a later drain point would
   leave reads and writes published before their required time.

Exact production timing remains blocked on a CPU/DMA-edge suspension contract.
The next implementation needs a pending setup/run request, an explicit bus
owner and per-read half-cycle continuation (including a latched byte before
its write). The scheduler must advance each wait outside PPU.Run, synchronize
the PPU without reentry, then perform the corresponding bus operation. Save
state must include ownership, alignment, channel/pass/byte index, pending read
value and remaining wait. Tests must cover CPU and general-DMA interruption,
setup/run overlap, same-channel cancellation and save/resume inside both
halves of a read before enabling that path.

No compensating cycle charge, unused timing model or unqualified scheduling
interface was added. Full timing and arbitration remain outstanding.
