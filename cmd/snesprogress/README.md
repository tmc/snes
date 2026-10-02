# snesprogress

Display recorded recovery evidence, with separate units and scopes:

```
snesprogress -config /absolute/progress.json -config-sha256 SHA
snesprogress -config /absolute/progress.json -config-sha256 SHA -listen 127.0.0.1:8476
```

The first command writes JSON; the second serves `/progress`, `/api/progress` and
indexed immutable evidence links. The editor accepts the same inputs using
`-progress-config` and `-progress-sha256` and links to `/progress`.

Config schema `snes-progress-config-v1` requires `recovery` and optionally
`coverage`, each with an absolute `path` and externally selected `sha256`.
Optional `batch` has `directory`, `config` (path/hash), and the original
completion `manifest_sha256`. Loading revalidates the retained batch with the
existing workflow verifier; it does not run C. Changed or incomplete batch
inputs refuse rather than retaining a stale qualification count.

Reached counts unique physical ROM instruction starts across recorded runs.
Decoded counts the union of bytes in the recovery document; context variants
and mirrored instruction starts do not inflate it. Coverage hits preserve
integer precision and require consistent per-frame/per-run accounting.

Captured counts known complete executions in selected extraction receipts,
with measured-entry scope. Missing receipts remain unavailable. Emitted counts
selected candidates with generated source bound to journal and batch inventories,
even when replay refused. Qualified counts sampled cases from a revalidated
recorded batch; compilation does not grant qualification.

The ROM map uses physical 32 KiB segments, not CPU banks. Its bars measure decoded
bytes, which include neither a code-only denominator nor a game-completion
estimate. The candidate table can be filtered by address, checkpoint, outcome
and refusal text. Checkpoints do not establish full call/data closure.

This is a read-only snapshot: no project mutation, hidden refresh, policy grant,
controller search or new execution. Start a new load with explicit new pins for
new evidence. No single percentage combines these stages.
