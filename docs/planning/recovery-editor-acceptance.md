# Recovery and editing campaign acceptance

This campaign ends with a locally integrated recovery pipeline and a headless
editing experiment. It does not require a web demo, a push, or a native game port.

## Required deliverables

| Deliverable | Acceptance evidence |
| --- | --- |
| Recovery queue and CLI | Bounded candidate selection, verified case admission, generated C compilation, replay receipts, atomic output publication, and refusal controls |
| Generic case extractor | Stream hashes computed over parsed bytes; complete instruction intervals; ordered bus effects; initial-memory provenance; explicit interrupted/incomplete counts |
| New routine support | Reviewed corpus and closure policy owned by the verifier; no routine-address allowlist or automatic trust of producer manifests |
| Editing experiment | Immutable baseline, explicit patch, identical initial CPU and memory, pinned inputs, repeatable state/write comparison, and edited proof eligibility always false |
| Behavioral target | ROM-backed routine and parameter mapping, with an actual capture or an explicit missing-capture report |
| Local integration | Task-owned source commits, focused integration checks, retained command logs and source identities, no unrelated changes |

## Evidence rules

- Entry hits are not complete executions. Keep both counts.
- A receipt hash binds bytes; it does not establish history completeness.
- A missing write is not evidence of an initial value. Require pinned initial
  bytes and history coverage, or refuse the occurrence.
- Qualification applies only to the selected admitted cases. It does not imply
  coverage of unseen branches, cycle accuracy, or hardware equivalence.
- Baseline qualification and experimental divergence are separate results.
- WRAM snapshots are not full-machine checkpoints. Full frame branching requires
  an isolated emulator instance and a complete restorable state.

## Current evidence to preserve

The unchanged runtime at `9e71ceef` has retained evidence for 782 leaf cases,
22 rejected controls, 20-case lazy/prefetch parity, and the 60-case composed
routine gate. The tests-only follow-up `08fce508` changes the expected rejection
stage for an interrupted case. It does not constitute a new runtime gate.

The `cpu_transition_match` receipt alias is unset for these routine receipts.
Architectural equality was checked from payloads; do not describe every receipt
boolean as passing.

## Completion audit

For each deliverable, record the integrated revision, exact command, exit status,
artifact paths, observed counts, skipped cases, and unresolved limits. Re-run
checks affected by integration changes. Reuse older evidence only when the
relevant source identity is unchanged and state that reuse explicitly.

## Delivered checkpoint

The bounded campaign is delivered at source revision `4a6302be`. See
[delivery and evidence](recovery-editor-delivery.md) for current counts, commands
receipt locations, source-identity reuse and remaining boundaries.
