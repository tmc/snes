# Headless source-edit experiments

`snesexperiment` is a bounded comparison backend for a future editor. It does
not drive a running console, fork full frame state, or replace ROM routines.

```sh
snesexperiment -config experiment.json -timeout 2m
```

The configuration explicitly pins the project content revision, ROM bytes,
one original routine case, original generated C, literal patch, and external
input schedule with SHA-256 values. It also specifies the original CPU-address
region, instruction budget, and refusal frontiers. Bounds are half-open. Only
LoROM, currently admitted pure CPU/RAM cases, and an explicit empty external
schedule (`[]`) are supported. A nonempty schedule is refused rather than
ignored.

The headless flow is:

1. Read and hash all declared inputs; retain the original unchanged.
2. Admit the original captured case using existing retained-evidence contracts.
3. Regenerate the bounded original region from the explicit ROM and admitted
   entry widths; require byte-identical generated source.
4. Compile and bind the original source, run existing three-way replay, and
   require fresh captured CPU and ordered-write qualification.
5. Apply an explicit literal patch to a separate source copy, compile it using
   the existing runner, and execute it from the **same** initial CPU/memory case
   and empty external schedule.
6. Compare exit registers, next PC, and ordered canonical writes. Publish the
   result and both source copies without granting proof to edited C.

A patch has `find` and `replace` strings. It must change exactly one occurrence.
The backend does not guess a parameter location or edit the user's source file.

## Configuration

The `internal/editor/replay.Config` JSON fields are:

| Fields | Purpose |
| --- | --- |
| `project_dir`, `revision` | Current project and content identity. |
| `rom_path`, `rom_sha256` | Explicit ROM and its bytes' identity. |
| `case_path`, `case_sha256` | One serialized `ReplayCase`, not an inventory claim. |
| `source_path`, `source_sha256` | Original generated C; must regenerate identically. |
| `patch_path`, `patch_sha256` | Explicit literal source edit. |
| `inputs_path`, `inputs_sha256` | Explicit external schedule; only `[]` supported. |
| `corpus_root` | Existing evidence verifier's retained corpus root. |
| `start`, `end`, `max_steps` | CPU-address region and finite instruction budget. |
| `refusal_targets` | Original region's explicit refusal frontiers. |
| `out_dir` | New durable output directory; existing outputs are refused. |

Entry E/M/X widths come from the admitted case. Carry is left dynamic, matching
bounded queue generation. A declared range/frontier set does not grant admission;
the original source and generated region must agree before replay.

## Original qualification and counterfactual labels

`result.json` contains the complete original baseline receipt separately from
edited execution data. `edited_captured_proof_eligible` is **always false**.
This remains false even when an edit happens to have no effect on this sample.

| Edited status | Meaning |
| --- | --- |
| `diverged` | Compared exit CPU state, next PC, or ordered writes changed. |
| `refused` | Edited execution reported unsupported access or a bound. |
| `same_sample` | No compared change in this single sample; not equivalence. |

The artifact tree includes `original.c`, `edited.c`, `case.json`, `patch.json`,
`inputs.json`, and `baseline.json` under `artifacts/`. The root `result.json` is
published last as the readiness marker. Input bytes and source artifacts are
rechecked before publication. Original captured qualification cannot be reused
as edited-source qualification.

The edited runner binary SHA-256, compiler identity/flags, source digest, input
identities, and original receipt metadata are retained. Temporary binaries and
private ROM copies are not published. Reverting an experiment means selecting
the unchanged original, not applying generated writes to a shared machine.

## Current limits

This is one routine's CPU/RAM comparison, not a full-system checkpoint. It does
not compare cycles, PPU/APU state, interrupts, device scheduling, frame sequences,
raw bus timing, or visible-pixel ownership. The reference emulator shares the
Go CPU ancestry; it is not an independent bsnes result. Initial RAM values are
retained case inputs, not a claim that arbitrary edited states are naturally
reachable.

The known sprite-preparation routine is only plumbing validation. A meaningful
movement or animation target additionally needs its own admitted captured case,
semantic parameter descriptor, and a defensible frame/effect link. No such link
is inferred from a source patch or an exit register change.
