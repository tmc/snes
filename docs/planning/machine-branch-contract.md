# Complete-machine branching with compiled C

Owned package: internal/editor/machinebranch. CLI: cmd/snesbranch. Runtime dependency: reviewed CPU instruction replacement API5e402963. Dispatch admission and the generic decompiler are separate owners.

## Checkpoint and frame contract

Config pins explicit ROM and complete version3 serialized state, an ordered controller schedule and1..600host frames. Owned bytes are measured before loading two independent machines; exact serialization roundtrip is required. Frame-skip/run-ahead, bad identities, unsupported schedules and CPU faults refuse. No partial result is published. Input changes apply at host-frame start.

PPU.FrameHook/CopyFrame supplies actual pixels and PPU frame/start/vblank identities. Serialized/component hashes and CPU cycles refer to host-frame end. These two instants are distinct. Physical bus journals hash ordered operation,address,value and CPU master-clock values for every bus hook event; they are runtime evidence, not an independent hardware oracle. PNG and checkpoint artifacts publish atomically to a new output directory.

## Compiled rotation slice

Mode generated_c requires nonzero Addend. The constructor verifies the exact32ROM bytes in0C:C45B..C47B and generates a narrow C instruction program from native8-bit opcode templates. It supports14static instructions, including the timer64 extra INC. The mechanical template is distinct from the generic decompiler and carries no captured recovery eligibility.

A persistent compiled process requests Fetch/Read/Write/Idle synchronously. The Go adapter services these through InstructionIO, after normal interrupt arbitration/opcode fetch, instead of executing the original opcode. Runtime primitives retain bus timing, DMA, math and device scheduling. C returns architectural registers; the runtime owns the cycle counter. Original ROM bytes remain unchanged, including the fetched ADC immediate5. Addend6 changes C arithmetic only.

Native M/X8, binary arithmetic, DB0C and D0 are required. Requests, instruction boundaries, operand values, physical WRAM cells, stack accesses and successor widths are bounded. A callback error faults the isolated machine and discards the result. No arbitrary C source or full-game C scheduler is supported.

Result distinguishes deterministic baseline equality from replacement execution. Addend5 compares interpreter and original C. Addend6 compares interpreter and edited C; divergence is expected. CapturedProofEligible remains false for both. Compiler version, C source/runner SHA, executed instruction count and full original/edited frame data are retained. A visible effect requires actual framebuffer differences, not merely changed WRAM.
