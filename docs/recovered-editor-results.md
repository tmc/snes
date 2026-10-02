# Recovered C editor experiment

The existing editor experiment endpoints now accept operator-pinned `recovered_c`
configuration. The browser submits only increment 5 or 6. The controller snapshots
the owned ROM and complete checkpoint, regenerates both original and edited
source/typed IR pins, and checks the runtime's mode, semantics origin, ROM, source,
IR, timing plan, edit, configuration, input schedule and proof status before
publishing results. Historical `generated_c` configuration remains supported.

The page displays original and edited generated C separately, backend provenance,
typed IR identities, actual original/edited PNGs and restore status. Refused jobs
publish no result or frame through HTTP. Restore executes original increment 5
from the same immutable checkpoint; it discards no authority checks.

## Qualification

The retained owned-ROM experiment uses 255 frames and the empty input schedule.
Original and restored runs agree with the interpreter for all 255 frames. Edited
and repeated edited runs each change 147 frames, first at PPU frame 332. Each
original and edited runner executes 833 recovered C instructions.

The browser operates Execute and Restore, polls `/api/job`, and loads the actual
`/api/job/frame` PNG. Independent comparison matches every original and edited
frame metadata field to the retained headless experiment, including machine,
component, bus, pixel and clock identities. Repeat and restore frame metadata
match exactly. Generated source, original/edited IR, timing plan, edit and runner
identities match the headless receipts. Published PNG hashes are independently
checked.

PPU start and VBlank boundaries agree across original and edited branches.
Edited host return cycles can differ by a few cycles because the host stops at
instruction boundaries; the controller checks ordering and bounded completion,
while the retained fixture comparison checks their exact expected values.

Focused tests cover operator source/IR/plan/edit/ROM pins, unsupported mode and
browser input, derivative pin regeneration, repeated edit/restore, failed-job
publication, and stale runtime source/IR/origin/config/input/clock/proof identities.
The existing authored generated C and sprite tests also pass.

```
SNES_RECOVERED_RECEIPTS=/Users/tmc/tmp/generated-runtime-bridge-receipts \
  go test ./internal/editor/web ./cmd/snesedit
```

Local raw browser results, commands, unit controls, independent fixture checks,
server executable, jobs and screenshots are retained under
`/Users/tmc/tmp/recovered-editor-receipts/`. Owned ROM/checkpoint identities and
headless source receipts remain under
`/Users/tmc/tmp/generated-runtime-bridge-receipts/`.

This qualifies one bounded rotation contract in the software runtime. Every
result retains `captured_proof_eligible=false`. It establishes neither broader
routine coverage nor independent hardware equivalence.
