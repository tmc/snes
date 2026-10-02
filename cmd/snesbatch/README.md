# Ten candidate recovery batch

Build `go build ./cmd/snesbatch`. Supply a strict JSON config with schema
`snes-recovery-batch-config-v1`, nonempty `sources` (absolute `path` and
`sha256` pairs), and exactly ten `tasks`. Each task contains pinned `config`,
`evidence`, and `policy` input pairs using the existing snesrecover formats.
Each candidate must have a distinct ROM SHA-256 and entry address pair.

```sh
TMPDIR="$HOME/tmp" snesbatch \
  -config "$HOME/tmp/batch.json" -config-sha256 "$BATCH_SHA256" \
  -out "$HOME/tmp/recovery-batch" -timeout 30m
```

The CLI runs the existing extractor, evidence verifier, candidate miner, generic
C generator, compiler and replay checker. The queue selects the task's entry
before applying its finite limit. Candidate ranking and source manifests never
grant admission. A reviewed admission policy is supplied explicitly; proposed
trust roots remain proposals. Missing delivery returns an unexecuted row.
Capture coverage, unsupported instructions, admission refusal, compilation
failure and replay disagreement retain their stage and reason.

The batch publishes an immutable `manifest.json` after all ten tasks finish.
Each `task-NN-EEEEEE` directory retains the revisioned workflow journal,
extraction receipt and cases, and any queue IR, generated C, admissions,
compiler identities and raw replay receipts. `row-NN.json` records the individual
outcome. A manifest with refusals is valid accounting and does not establish
success for those candidates. Cancellation or changed input refuses publication.
The output is reserved exclusively and the readiness manifest is published last;
consumers must require that marker before reading a batch.

An identical rerun verifies all pinned source, ROM, project, evidence and policy
inputs, executable identity, retained artifact hashes and journal chains. It
returns the retained qualification without executing again. Use a new output
directory for fresh measurements. Changing pins or the executable requires a
new batch directory. Retained results remain sampled captured CPU/RAM and
ordered WRAM effects; there is no timing, independent hardware-oracle,
whole-game or mathematical-proof claim.

A ten-task batch is finite but can reread shared captures for each task. The
current extractor checks cancellation between phases. The batch deadline is
checked between candidate workflows and before publication.
