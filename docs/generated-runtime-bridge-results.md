# Generated C running inside the SNES machine

The rotation handler at `$0C:C45B..$0C:C47B` now runs as automatically emitted
C inside the emulator. ROM decoding and machine IR provide the semantics;
the existing instruction lease services its operand fetches, reads, writes,
and idle clocks. The controller checks the reviewed timing and control-flow
contract without executing the original arithmetic as a replacement fallback.

The implementation is in `decomp.GenerateTimedRegionC` and
`machinebranch.PrepareRecovered`. `snesbranch` selects it with `recovered_c`.
The existing authored `generated_c` backend remains separate and unchanged.

## Retained experiment

All four runs restore the same complete checkpoint and ROM, apply the same
empty input schedule, and capture 255 frames. Each executes 833 instructions
through the recovered C backend.

| Run | Result |
|---|---|
| Original immediate 5 | All 255 frames agree with the interpreter |
| Edited immediate 6 | 147 frames have different decoded pixels; first at PPU frame 332 |
| Repeat edited | Entire result JSON is byte-identical to the edited run |
| Reset immediate 5 | Entire result JSON is byte-identical to the original run |

Agreement covers serialized machine state, component hashes, ordered physical
bus stream hashes and counts, clock boundaries, frame geometry, and rendered
pixels. The independent artifact check verifies all 2,040 PNGs and reconstructs
their BGR555 framebuffer hashes. The edited value changes the lifted ADC
expression at `$0C:C46C`; actual ROM operand fetches still read 5.

The source, canonical original and edited IR, ROM, timing plan, edit, compiler,
runner, CLI executable, configuration, and checkpoint have separate identities.
The CLI retains `recovered.c`, `original-ir.json`, `edited-ir.json`,
`recovered-identities.json`, `checkpoint.state`, PNGs, and `result.json`.
An earlier edited-IR hash bug was reproduced and corrected: the identity now
includes typed semantic expressions instead of cached presentation strings.

## Reproduce

Build `./cmd/snesbranch`. Obtain the explicit recovered identities with
`machinebranch.PrepareRecovered(rom, 5)` or `PrepareRecovered(rom, 6)` and place
them in the pinned configuration. Run:

```
snesbranch -config config.json -config-sha256 SHA256 -out new-directory
```

Retained local commands, configurations, source manifest, raw tests, and runs:

- `/Users/tmc/tmp/generated-runtime-bridge-receipts/REPORT.md`
- `/Users/tmc/tmp/generated-runtime-bridge-receipts/run-machine-gates.sh`
- `/Users/tmc/tmp/generated-bridge-independent-review/`
- `/Users/tmc/tmp/generated-c-bridge/`

Emitter source revisions are `d8f56cbd` and its identity correction `4494ecd8`;
runtime revision is `4138b464`. The integrated implementation tree at
`d0aa952b` is byte-identical to the qualified runtime tree. These changes and
artifacts are local; nothing was pushed or published.

## Limits and next work

This qualifies one native, binary, eight-bit, low-WRAM rotation region with
DB `$0C` and direct page zero. It is evidence from this software runtime, not
whole-game C recovery or independent hardware equivalence. The physical bus
journal does not compare CPU observer metadata during micro-operations.
Every run retains `captured_proof_eligible=false`; edits inherit no captured
recovery proof. Failed operations discard the branch, without claiming rollback
of partial device activity.

The next product step is to connect this backend to the editor's branch action:
show the generated C, apply the bounded constant edit, compare frames, and reset.
Broader routine coverage requires additional reviewed timing and context
contracts and their own qualification.
