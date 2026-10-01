# Campaign status

Reviewed on October 1, 2026 against integration revision `2ee5b646`.
The five-milestone campaign remains active. Shared checkouts are preserved;
changes and receipts are local and have not been pushed.

| Milestone | Status | Evidence and limits |
| --- | --- | --- |
| Rotation dispatch admission | Pending acceptance | Body-only captures previously received admission. Independent review requires actual caller/helper/handler ancestry, ROM fetches, table and scratch accesses, and correct JSR push order. The correction must pass a frozen positive and negative gate. |
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
- Dispatch independent review:
  `/Users/tmc/tmp/dispatch-ancestry-independent-review/WIP-review.md`.
- Requirement audit: `/Users/tmc/tmp/snes-campaign-requirement-audit.json`.

Matching clean-engine write histories have been produced for both captures:
240 of 240 frame summaries match the dispatch capture, and 480 of 480 match
its extended connected-animation capture. Pins and producer outcomes are in
`/Users/tmc/tmp/rotation-history-d21/root-receipt.json` and
`/Users/tmc/tmp/rotation-history-d21-480/root-receipt.json`.

## Remaining acceptance

1. Freeze the corrected dispatch implementation. Independently execute genuine
   positives and missing/substituted ancestry, helper fetch, table/scratch,
   selector and stack controls. Integrate only after these pass.
2. Freeze and review the connected decoder. Extract the 116 complete closure
   cases with matching history; preserve the 139 incomplete later-handler
   intervals as exclusions. Admit and compare observed execution, software
   reference execution and compiled C with ordered effects.
3. Repeat focused integration gates on the final combined source identity.

Software-reference agreement does not establish hardware timing, unseen paths,
whole-game C recovery or an independent CPU implementation. Edited branches
retain `captured_proof_eligible=false`. The sprite edit does not execute a C
replacement. These qualifications remain part of acceptance.
