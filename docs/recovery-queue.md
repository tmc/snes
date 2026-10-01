# Bounded C recovery queue

The queue connects candidate ranking to C generation, compilation, and captured
routine replay. It processes candidates sequentially and publishes a durable
report. A successful command can contain blocked or mismatching candidates;
command success means the report was published, not that C was qualified.

```sh
snesdasm queue \
  -project game_dasm \
  -rom game.sfc \
  -cases cases.jsonl \
  -corpus captures \
  -out recovered_c \
  -limit 5 -maxcases 100 -maxsteps 50000 \
  -format json
```

All five input/output path flags are required. `-cases` identifies a routine-case
JSONL inventory. An empty file is valid and yields blocked candidates; inventory
claims do not grant admission. `-corpus` is the root expected by the evidence
verifier for the referenced retained artifacts. The ROM must match the project
identity and supported mapping.

The default budgets are five candidates, 100 cases per candidate, and 50,000
machine instructions per case. Zero does not mean unlimited. Candidate limits
must be 1–100, case limits 1–10,000, and instruction limits 1–1,000,000. There are
no user-supplied shell commands, compiler arguments, live execution controls, or
web UI in this command.

## Qualification scope

A generated source file is mechanical C, not a qualification result. Compilation
only establishes that the emitted source builds. A candidate becomes qualified
only when a nonempty selected case set is admitted against retained evidence,
replayed, and checked for receipt freshness, exit CPU state, and ordered memory
writes. Qualification applies to those persisted cases and their execution
contexts. It does not imply all branches or other entry states were covered.

The production reference replay uses the Go CPU core. It is not an independent
bsnes CPU comparison. Timing, interrupts, device behavior, raw bus-access
parity, visible-pixel attribution, and whole-game equivalence are outside this
qualification scope.

The current admission contracts support existing routine identities. A newly
mined routine can therefore remain blocked even if its instruction vocabulary
is supported. The queue records this boundary rather than treating unverified
inventory contexts or a zero-case compile as proof.

## Artifact layout

The output directory must not already exist. The queue stages its artifacts, exclusively reserves the output
root, and publishes `report.json` last as its readiness marker. It does not
overwrite an existing run or unrelated files. Use a new output directory for each run.

`report.json` identifies the project content revision, ROM SHA-256, input artifact
hashes, bounded candidate results, and limitations. The complete staged payload
lives under `artifacts/`. Each result's `directory`
field is the path to that candidate's artifacts relative to the output root.
Directories use `artifacts/<candidate-id>/<input-content-hash>/`. Consumers
should use the reported field rather than derive a directory from its rank.

Within a candidate directory:

| File | Meaning |
| --- | --- |
| `proposal.json` | Unqualified mined region and frontiers. |
| `admissions.json` | Admission attempts against retained evidence. |
| `result.json` | Status, stable reason code, detail, counts, and generated artifact hashes. |
| `region.json` | Bound machine IR, when decoding succeeds. |
| `generated.c` | Mechanical generated source, when generation succeeds. |
| `cases.json` | The admitted cases selected for replay. |
| `receipts.json` | Freshness-checked replay receipts. |

Later-stage files are absent when a candidate is blocked earlier. The generated
C source is retained; the temporary compiled runner and its private ROM copy
are not published. Compiler, runner, source, ROM, and context provenance lives
in replay receipts. Keep retained evidence available if you want to rerun or
revalidate a result.

Read the candidate status and counts separately: cases examined, cases admitted,
matching replays, refused cases, mismatches, and admitted cases left unexecuted. A blocked result is actionable
information about missing evidence or unsupported contracts. It is not a claim
that the bytes are data or that the original ROM cannot execute them.
