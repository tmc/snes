# Source edits and observed rendered effects

A pinned `observation` configuration adds a bounded event window to the machine
experiment. Existing bus, DMA, HDMA and physical PPU hooks record actual accesses
without replacing the state or physical bus journal. `provenance.Compare` and
`machinebranch.CheckObservations` bind the raw windows and explanation to the
owned ROM, checkpoint, input schedule, generated source/IR, run and completed
frame identities. The browser displays a summary and inspectable JSON through
the existing experiment/job/frame routes.

The report distinguishes source instruction writes, last-writer DMA/high-OAM
byte-version links, changed physical PPU writes, changed PPU register writes,
and actual frame differences. Corresponding write versions use PPU frame,
address, PC and occurrence order; physical storage comparisons omit PC. This
correspondence is an observation comparison, not a dependency inference.

## Retained rotation experiment

The observation window covers relative host frames [106,111). Completed PPU
frames are 330..334; host instruction completion also observes the beginning
of PPU frame 335. Each event retains its actual PPU frame and cycle.
The selected source interval is `$0C:C45B..$0C:C47B`; frame 108 is inspected.
All four full-machine runs still capture 255 frames and execute 833 recovered
C instructions. Every frame's state, component hashes, ordered physical bus
journal, geometry, clock boundaries and decoded pixel hash matches the earlier
observation-off experiment. Repeat and restore results are identical in their
entire result JSON, including all raw observation events and reports.

| Observation | Original | Edited |
|---|---:|---:|
| Events in the bounded window | 203,022 | 203,063 |
| Selected source instruction writes | 3 | 3 |
| Direct selected-source DMA/high-OAM chains | 0 | 0 |
| Changed source write versions | 0 | 1 |
| Changed PPU register write versions | 0 | 217 |
| Changed physical PPU write versions | 0 | 75 |
| Changed pixel frames across the full run | 0 | 147 |

The first changed pixel frame remains PPU 332. The selected source write and
PPU consumption differences are observed; CPU arithmetic dependencies between
them are not tracked. The report explicitly says transitive source-to-render
dataflow is unavailable. It assigns no visible pixel ownership to an OAM entry.

## Complementary observed sprite chain

A separate no-edit sprite experiment restores its own pinned checkpoint and
records three host frames. It observes 32 selected routine writer→DMA→high-OAM
chains. For the selected sprite's high byte, CPU PC `$00:8614` writes value
`$AA` to WRAM `$0A00`; channel-zero DMA reads that version, writes `$2104`, and
physically writes OAM byte 512. Intervening writers replace earlier byte versions.
This is evidence for that sprite upload, not a rotation-to-sprite dependency.

## Qualification and inspection

Focused tests cover observation-on/off parity, deterministic windows, event-budget
refusal, partial/filtered coverage, intervening writes, missing actor metadata,
stale window pins, source/frame/run identities, report tampering and failed HTTP
publication. Existing authored generated C and sprite regressions pass. The
operator-selected backend appears in read-only target metadata: recovered mode
uses browser increments 5/6; historical authored mode keeps 1..12. The local
server refuses nonliteral or nonloopback Host authorities. A real historical
authored-mode browser run executes increment 4 with the original 1..12 bounds.

The retained Brave browser operates the actual source edit, polls the job and
loads the original/edited PNGs. Its explanation reports the observed differences,
zero direct rotation DMA/OAM chains and unavailable transitive dependency.
An independent pinned-window `sneseffects` CLI reproduces the runtime report.
Independent PNG decoding reconstructs all 2,046 published BGR555 framebuffer
hashes across the four rotation runs and complementary sprite run.

```
go test ./internal/provenance ./internal/editor/machinebranch \
  ./cmd/sneseffects ./cmd/snesbranch ./cmd/snesedit ./internal/editor/web

sneseffects -original original-window.json -original-sha256 ORIGINAL_SHA \
  -edited edited-window.json -edited-sha256 EDITED_SHA -frame 108 \
  -routine-start 0xcc45b -routine-end 0xcc47c -sprite 0
```

Local configurations, raw windows, runtime source/IR material, frame artifacts,
commands, controls, browser receipts and independent checks are retained under
`/Users/tmc/tmp/effect-observation-receipts/`.

## Limits

The producer supports ordinary LoROM and version-3 complete checkpoints without
cheats, run-ahead or direct sprite data intervention. At most 16 frames and two
million events per branch are allowed. Exhaustion refuses the run and publishes
no partial window. Coverage applies only to this window; initial byte versions
are unknown. Low-OAM latch origins, unsupported DMA shapes and HDMA consumption
remain unknown. CPU PC metadata is runtime observation, not independently
recovered source ancestry. Generated source pins on the interpreter branch
identify reference material, not executed C. Every report retains
`captured_proof_eligible=false`.
