# Partial-step qualification

Production `Device.Step` still uses whole-instruction execution and debt. Do not
replace it with a loop around `StepSlice`: the latter deliberately returns zero
progress for unsupported boundaries. `Run` can resume an active partial frame
and must not dispatch that opcode again.

Production callers are `Cartridge.Step(masterCycles)`, ROM arbitration
(`arbitrateGSUROM`, six cycles), and CPU register/cache synchronization
(`stepGSURegisterAccess`, six cycles). Consequently production must handle all
reachable instructions, prefix combinations, cache states and small budgets.

The current partial frame contains opcode/PC/PBR, phase, ALT mode, source and
destination registers, immediate bytes, bank/address, remaining wait and pending
prefix/post-increment flags. The surrounding board state also retains pipeline,
register-modification flags, cache validity, ROM/RAM pending buffers and delays,
and production budget/debt. No new serialized fields are needed by the SBK
pending-write phase.

| Instructions | Qualified handler boundaries | Restrictions |
| --- | --- | --- |
| FMULT | Multiply wait | Plain opcode |
| IWT | Low/high immediate fetch | Plain opcode; complete fetch budget required |
| LM | Immediate fetches, pending RAM sync, word read | ALT1/ALT3; no register prefix |
| LMS | Immediate fetch, pending RAM sync, word read | ALT1/ALT3; pending RAM delay exceeds dispatch |
| SM/SMS | Immediate fetches, pending old write, low/high write staging | ALT2; no register prefix |
| STW | Pending old write and low/high staging | Plain opcode |
| STB | Pending RAM sync then byte staging | ALT1/ALT3; pending RAM delay exceeds dispatch |
| LDW/LDB | Pending RAM sync then word/byte read | Plain/ALT1/ALT3; pending RAM delay exceeds dispatch |
| GETB/GETC | Pending ROM sync then data read | Pending ROM delay exceeds dispatch; no register prefix |
| ROMB/RAMB | Pending ROM/RAM sync then bank update | ALT3/ALT2; pending delay exceeds dispatch |
| SBK | Pending old RAM write, low/high staging | Plain opcode |

SBK now covers an existing pending write in a different RAM bank. Tests compare
coarse, single-cycle and edge partitions, restore every partial boundary, count
one dispatch and one retirement, check exact byte visibility, and use sentinels
to detect replayed writes. The source reference is bsnes at `9144b5ac557f6bd62aacefb87ee31cf5e12a476b`,
`processor/gsu/instructions.cpp`, `instructionSBK`: stage the low byte, then stage
the high byte through the synchronized RAM buffer.

Operand fetch admission now prices `R15+1`, matching `fetch8`'s pre-increment.
The regression test crosses a valid-to-invalid cache line: a 95-cycle request
must make no progress when the refill needs 96 cycles. Before this fix that
request consumed 98 cycles and retired the opcode. Save/resume and the complete
96-cycle fill now match whole-handler execution.

Production integration remains open for these boundaries:

- Cold NOP, STOP, prefixes, branches/LOOP/JMP/LJMP, ordinary ALU operations,
  MULT/UMULT, IBT, PLOT/RPIX, CACHE and other unsupported opcode families.
- Register-prefix combinations and alternate-mode variants excluded by the
  admission predicates; register-14/15 side effects require explicit witnesses.
- RAM/ROM operations when no pending buffer exists or its delay expires during
  dispatch, where the current predicates deliberately decline admission.
- Budgets smaller than a complete opcode or operand refill, including cache
  line fills and uncached bus accesses. Cache fills remain atomic, not sliced.
- CPU-visible changes to bus ownership and MMIO during an active instruction.
- External pinned reference windows for first retirement and CPU-visible RAM
  timing. Synthetic whole-handler agreement is not hardware timing evidence.

`TestStepSliceUnsupportedBoundariesDoNotAdvance` preserves this no-progress
contract for representative unsupported classes. Production switching remains
blocked on the complete boundary inventory and reference windows; no fallback
that executes an unqualified instruction early has been added.
