# Campaign status

Reviewed on October 1, 2026 against integration revision `93703ef7`.
The five-milestone campaign remains active. Shared checkouts are preserved;
changes and receipts are local and have not been pushed.

| Milestone | Status | Evidence and limits |
| --- | --- | --- |
| Rotation dispatch admission | Accepted bounded recovery | All 15 natural `0C:C45B` cases have observed caller/selector/JSL/helper/table/JML ancestry, compile through generic C recovery, and match observed execution and the software reference with ordered effects. Eleven admission controls and eight extraction controls reject; clean body-only evidence remains ineligible. |
| Rotation edit executed as C | Verified bounded backend | Original and compiled-C baseline agree across 255 complete machine frame records; 833 instructions execute as C. The edited branch changes 147 frames and repeats identically. This backend uses authored opcode templates, independently of generic recovery qualification. |
| Automated recovery workflow | Verified bounded workflow | Corrected source `a322a28c` selects, extracts, admits under explicit reviewed policy, generates C, and qualifies one sampled genuine `0E:D60B` case. All 310 committed artifact pins were checked. Policyless resume returns recorded qualification; explicit policy reruns fresh validation. |
| Connected animation recovery | Implementation and review | The generic multi-bank decoder is under independent review. The capture contains 116 complete closure intervals: 52 early returns, 63 ordinary handler paths, and one timer-expiry path. These are an execution census, not admitted C replay cases. |
| Selected sprite editor | Verified bounded edit/reset | Actual Brave interactions clear OAM0's size bit, change PPU frames 83 and 84, then reset to zero frame differences. The UI links the supported writer and DMA chain. This is a reversible data intervention; individual pixel ownership remains unknown. |

## Retained evidence

- Machine C edit: `/Users/tmc/tmp/machine-branch-rotation/REPORT.md`.
- Workflow: `/Users/tmc/tmp/recovery-workflow-receipts/REPORT.md` and
  `/Users/tmc/tmp/recovery-workflow-smoke/cache-fixed-resume-results.json`.
- Sprite backend: `/Users/tmc/tmp/sprite-data-branch/REPORT.md`.
- Browser edit/reset:
  `/Users/tmc/tmp/editor-root-sprite-browser-acceptance.json`.
- Dispatch acceptance: `/Users/tmc/tmp/helper-final-independent-review/REPORT.md`
  and `receipt.json`; final combined runtime gate:
  `/Users/tmc/tmp/m1-combined-gate/final-6780e620/`.
- Clean ancestry negative review:
  `/Users/tmc/tmp/helper-final-independent-review/clean-body-test-review.md`;
  raw result: `/Users/tmc/tmp/m1-combined-gate/clean-body-only-followup.jsonl`.
- Final leaf/editor regressions:
  `/Users/tmc/tmp/m1-combined-gate/remaining-93703ef7/report.json`.
- Requirement audit: `/Users/tmc/tmp/snes-campaign-requirement-audit.json`.

Matching clean-engine write histories have been produced for both captures:
240 of 240 frame summaries match the dispatch capture, and 480 of 480 match
its extended connected-animation capture. Pins and producer outcomes are in
`/Users/tmc/tmp/rotation-history-d21/root-receipt.json` and
`/Users/tmc/tmp/rotation-history-d21-480/root-receipt.json`.

## Dispatch acceptance and source reuse

The accepted ancestry runs bind matching clean `d21ee387` capture and history
identities. The helper checks low/high stack and scratch access order, pointer
lineage, actual ROM table reads, and the handler continuation. Malformed operand
fetch metadata now rejects before fetch classification; the opcode-fetch reset
exception applies only to the opcode slot. Adjacent CPU states must also have
identical exit/entry cycles. Dirty producers and different engine identities
reject even when selected frame hashes agree.

The combined runtime gate at `6780e620` retains 15 positive cases, 11 rejected
admission controls and 8 rejected extraction controls. Its aggregate report is
marked failed because two negative tests used obsolete test fixtures: the
historical body-only capture is dirty, and the scratch shape test removed only
the old WRAM event view. These failures remain in the raw log. The corrected
shape control removes both event views. Test-only `93703ef7` constructs a clean
body-only projection, removes 3,494 retired ancestor instructions, and updates
all capture/fixture hashes. All 15 opportunities refuse with
`missing_fixture_predecessor_insn`; admission refuses `capture lacks seq 2944775`.
Neither negative relies on an unrelated identity mismatch.

The root identity receipt
`/Users/tmc/tmp/m1-combined-gate/root-runtime-identity-93703ef7.json`
compares 247 production Go files between these revisions: none changed. The
positive/control runtime receipts are reused on that basis; the corrected
negative tests were executed separately on the final test source.

The final leaf/connected focused run has 84 passing test events and two passing
packages, with no failures or skips. This includes a configured reviewed leaf
sample, not the full 782-case corpus or admission of the connected census.
The editor/workflow/CLI short run has 238 passing test events and seven skipped
integration tests: captured edit, machine experiment, frames, sprite experiment,
sprite capture, target manifest and captured queue. Their retained inputs were
not selected in that command. `cmd/snesedit` separately reports no test files.
Earlier configured machine and browser receipts remain separate evidence; editor,
CPU replacement and `cmd/snesedit` source is unchanged since `2ee5b646`.

## Remaining acceptance

1. Freeze and review the connected decoder. Extract the 116 complete closure
   cases with matching history; preserve the 139 incomplete later-handler
   intervals as exclusions. Admit and compare observed execution, software
   reference execution and compiled C with ordered effects. The census alone
   does not complete connected animation recovery.
2. Keep the selected-sprite edit/reset scope separate from generic recovered-C
   qualification. Extend object provenance and supported edits through measured
   evidence; individual pixel ownership is still unknown.
3. Repeat the relevant configured gates after future runtime changes. Preserve
   missing input and unsupported path exclusions in each receipt.

Software-reference agreement does not establish hardware timing, unseen paths,
whole-game C recovery or an independent CPU implementation. Edited branches
retain `captured_proof_eligible=false`. The sprite edit does not execute a C
replacement. These qualifications remain part of acceptance.
