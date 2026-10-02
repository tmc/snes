# Recovery and inspection campaign

This campaign extends the recovered-C editor with measured frame inspection,
local C expressions, bounded exploration and observed WRAM readers. It builds on
the isolated recovery/editor integration at `948f8e26`; shared checkouts and
remote branches are not part of this delivery.

## Measured execution beside frames

The editor can capture completed replacement-C instruction counts for each
relative host frame. The frame slider, image pair, pixel-change strip and
generated C case highlights share one selection. A measured zero is different
from unavailable execution capture. Counts identify instruction addresses, not
pixel ownership or individual C statements.

Four real 255-frame jobs (original 5, edit 6, repeat 6, reset 5) each recorded 833
completed compiled instructions. Each had 191 measured-zero frames and 64
active frames. Edited jobs changed 147 framebuffer hashes; original and reset
changed none. Relative frame 108 / PPU frame 332 highlighted exactly 13 measured
case labels. Rapid selection and missing-frame controls passed. Published frame
metadata matched timeline-disabled controls in 4,080 comparisons. Those records
include copied original results; they are not 4,080 independent executions.

Evidence: `~/tmp/timeline-inspection-receipts/REPORT.md` and its pinned browser,
parity, source and test receipts. Locally warmed image-ready latency excludes
paint, machine execution and compilation.

## Executable local C expressions

`cmd/snessemantic` emits original C, transformed C and a manifest from a pinned
ROM and connected typed IR. The opt-in pass preserves architectural writeback,
flags, memory callback order and refusal guards. Existing C receipts do not
qualify newly transformed source.

The actual connected rotation region produces seven load locals and two ADC
expressions using runtime entry A/carry, with nine source-map declarations.
There are **zero fused cross-block chains**. This is not game-level variable,
function or structured-loop recovery.

Fresh transformed C matched trace, the Go reference core and original C on all
116 real closures: 52 early, 63 ordinary and one timer64 path. Finite arithmetic
controls covered 2,048 byte/addend/carry combinations, high accumulator bits,
flags and deliberately changed arithmetic or write order. Independent review
also compared the 15 read callbacks of one ordinary case exactly. Cycles are
not part of this semantic comparison.

Evidence: `~/tmp/semantic-c-receipts/REPORT.md` and
`~/tmp/snes-next-campaign/semantic-independent-review/`.

## Bounded capture and exploration

`cmd/snesexplore` classifies pinned prior refusals, measures a baseline and eight
bounded controller schedules, then invokes existing capture and extraction
tools. Novelty uses instruction address and CPU width/status context, not
changing raw state hashes. Discovery, extraction, admission and C execution
remain separate outcomes.

Runtime consumers share one privately owned, hash-verified ROM snapshot and a
private verified producer executable. The operator ROM pin is retained
separately. A transient substitution/restoration control reproduced mixed-ROM
capture publication before the fix and no longer changes the producer inputs.
The private ROM is removed before publication; it is not a published artifact.

All nine prior batch refusals were reached but outside the old capture ranges.
The selected `$00:9347` target already had 652 discovery hits. The pilot measured
240 baseline frames and eight 30-frame schedules. Every schedule had the same
site/context set: controller-relative novelty was zero. The winning schedule
repeated deterministically twice.

Recapture evaluated 15 entries and extracted **zero complete cases**: one lacked
a fixture body instruction, and 14 failed covered-access requirements. The body
includes `STA $2115`, a PPU register access outside the current generic WRAM
qualification contract. There are zero admitted or generated-C-executed cases
for this target. Expanding that contract requires timed device semantics, not
relaxing the access checks.

Evidence: `~/tmp/snes-next-campaign/EXPLORATION-REPORT.md`. Runtime receipts name
the revision actually executed; later ownership fixes have separate controls.

## Observed WRAM reader frontier

`cmd/snesreaders` selects an attributed WRAM write in a pinned complete window
and reports CPU/DMA reads of that byte version until the next write or window
end. Low-RAM mirrors and WRAM ports map to physical WRAM. A same-value overwrite
starts a new version; unsupported reader actors remain unknown. Conflicting
read values or declared incomplete/restricted windows refuse. Writer completeness
is the producer's declared contract, not independently established by absent
events.

For the real angular write at `$0C:C46E`, relative frame 108, the original byte
was 115 and the edited byte was 170. Both were subsequently read at `$09:F882`
in frame 109 and `$0C:C468` in frame 110 before replacement. Independent review
reproduced both outputs byte for byte. This is observed byte consumption, not
arithmetic propagation, pixel causality or a transitive dependency graph.

Evidence: `~/tmp/snes-next-campaign/reader-report.md` and
`~/tmp/snes-next-campaign/root-independent-review/`.

## Compressed producer receipts

The extractor now accepts the trace producer's raw gzip-container receipt when
the declared compressed digest matches independently measured raw bytes and the
logical stream digest separately matches decoded bytes. Legacy decoded-only
receipts remain supported. Raw-container receipts require explicit gzip encoding
and a matching container pin; omitted encoding remains decoded-receipt-only.
Producer files are not rewritten.

The retained gzip triple now reaches extraction successfully, with the same
zero-complete / 15-refused result described above. Compatibility success is not
routine qualification.

## Next contracts

1. Extend local value recovery across explicitly safe CFG edges, retaining
   state, flags, reads and instruction provenance before claiming fusion.
2. Select a naturally reached pure-WRAM capture gap for a new qualified routine;
   treat timed MMIO as a separate execution contract.
3. Connect observed byte readers to the editor selection without inventing
   arithmetic or pixel dependencies.
4. Expand instruction/frame inspection beyond the current bounded replacement
   region only when its address and instruction budgets are explicit.

All comparisons here are bounded software evidence. The reference core shares
implementation with the runtime. Edited experiments and reader frontiers never
become captured-proof eligible; none of these results establishes a whole-game
native C port or hardware equivalence.
