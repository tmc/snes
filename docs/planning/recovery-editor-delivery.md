# Recovery and headless editing delivery

This is the historical headless delivery. For the current five-milestone
campaign and its remaining gates, see [Campaign status](campaign-status.md).

Integration branch: `codex/campaign-integration`, source revision `4a6302be`,
worktree `/Users/tmc/tmp/wt-campaign-integration`. Shared checkouts were preserved.
No push or demo/UI was added.

## Delivered

- Bounded recovery queue and CLI: candidate inventory, explicit reviewed policy,
  verified admission, machine IR, generated and compiled C, durable replay receipts.
- Go case extractor: checked stream identities, producer coverage, instruction
  continuity, initial-memory history, nested CPU context and ordered bus effects.
  Producer proposals grant no consumer authority.
- Headless editing: immutable original C and inputs, explicit patch, deterministic
  CPU/memory replay, separate baseline qualification and experimental divergence.
- Source-backed animation target with a real natural capture and a precise
  admission boundary for the next implementation slice.

## Acceptance evidence

| Requirement | Retained evidence and result |
| --- | --- |
| Extractor | `/Users/tmc/tmp/extractor-combined-cpu-context-final/REPORT.md`: exact integrated executable source review; 66 passing test entries including subtests, no failures or skips; 111 leaf hits produce 110 complete cases and one illegal-call refusal; composition bootstrap produces 60 cases |
| New natural routine | `/Users/tmc/tmp/new-0ed-consumer-preflight/REPORT.md`: all 110 cases for `0E:D60B` admitted, compiled and matched; ten negative controls rejected and ten substitution controls ineligible |
| Final CLI | `/Users/tmc/tmp/campaign-integration-receipts/new-routine-queue.stdout.json`: actual run at `4a6302be`, exit 0; selected leaf `0E:D60B`, three admitted and matched, zero refused/mismatched/unexecuted |
| Existing queue and edit | `/Users/tmc/tmp/campaign-final-queue-edit/REPORT.md`: default and explicit policy queue runs pass; two repeated edit runs have two eligible baselines each, two intended divergences each, and zero edited proof grants; inputs unchanged |
| Behavioral target | `/Users/tmc/tmp/editor-target-capture/REPORT.md`: 15 natural rotation-handler intervals, frames 225–239, 13 instructions and three writes each; 240/240 frame-hash parity in the capture comparison |
| Focused checks | `/Users/tmc/tmp/campaign-integration-receipts/`: final extractor, queue, editor, CLI and generic-policy command logs and receipts retained |

The all-110 gate ran in an isolated worktree at `d4415a24`. Revision `4a6302be`
adds extractor files only; its decompiler, queue and replay runtime are unchanged.
The final CLI additionally exercised three cases at the integrated revision.
Existing queue/edit evidence is reused on the same unchanged source identity.
These are focused checks, not a claim that the entire repository suite passed.

Generated leaf C SHA-256:
`5063ca55f14367080b9c00ece813cda5c194cb75732a44f82828fbb0e728a71d`.

Raw reviewed policy SHA-256:
`8a9a21e728465f7bc45ce015d653ba5a3f1df2ae067fea942f691b99d88291bc`.

Durable CLI output: `/Users/tmc/tmp/campaign-new-routine-queue/`.
Full qualification artifacts:
`/Users/tmc/tmp/new-0ed-consumer-preflight/qualified-gate/`.

## Limits and next boundary

Qualification covers bounded native CPU architecture and ordered WRAM writes.
It does not establish timing, device behavior, unseen paths or whole-game C
recovery. The reference Go CPU shares ancestry with the replay CPU. The
`cpu_transition_match` alias is unset in routine receipts; payload equality was
checked directly, so not every receipt boolean is asserted true.

The title fixture and recapture have component/framebuffer parity on 920/920
frames, but aggregate state hashes differ on 912 frames. This remains
component-only evidence.

The new seven-instruction leaf is a mechanical recovery target. The rotation
handler at `0C:C45B..C47B` is the behavioral target: it changes polygon angles and
a timer, but enters through a dispatch table rather than an immediate call.
Current direct-call admission refuses that boundary. Next work is explicit
verified dispatch-entry admission, followed by a headless angle-change experiment.
No edited frame rendering or live game editing has been established.

Extractor publication was checked on macOS; other operating systems remain
unqualified. Missing coverage, unsupported actors and incomplete occurrences
refuse rather than acquire proof eligibility.
