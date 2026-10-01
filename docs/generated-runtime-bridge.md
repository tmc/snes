# Generated C in the running machine

`snesbranch` has three execution modes relevant to source recovery:

- `original_interpreter` repeats the checkpoint with the Go CPU.
- `generated_c` executes the existing authored rotation template.
- `recovered_c` decodes pinned ROM bytes, lifts machine IR, and compiles the C
  emitted by `decomp.GenerateTimedRegionC`.

The recovered mode currently supports the native, binary, eight-bit rotation
region `$0C:C45B..$0C:C47B`, with DB `$0C` and direct page zero. This is a bounded
execution experiment. It does not establish whole-game C recovery or grant a
captured-proof receipt.

## Source and input identities

Use `machinebranch.PrepareRecovered(rom, addend)` to obtain the required source,
original IR, edited IR, timing plan, and edit hashes. Supply those hashes in the
configuration's `recovered` object. The ROM and complete-machine checkpoint
have their own required hashes. The addend is either 5 (unchanged) or 6 (an edit
to the lifted ADC immediate at `$0C:C46C`).

`Run` regenerates the source from an owned copy of the verified ROM, compares
all supplied identities, then compiles that source. It does not accept an
external C file or infer authority from an artifact directory. The runtime
still fetches the original ROM operand 5; the edited lifted expression uses 6.
The baseline always runs the interpreter from a separate copy of the same
immutable checkpoint and receives the same ordered input schedule.

## Runtime boundary

The compiled program requests operand fetches, physical reads and writes, and
idle clocks through the runtime's instruction lease. The Go controller checks
these requests against the reviewed timing plan and verifies original fetch
bytes. It does not execute the original instruction's arithmetic to obtain the
replacement result.

The done response must preserve DB, D, PB, X and Y, retain native eight-bit
binary widths, and have the expected stack and successor PC. Branch targets
use the actual fetched displacement and entry zero flag. RTS uses the two
actual bank-zero stack reads. These checks constrain the protocol and control
flow; they are not a second implementation of the generated ALU semantics.

An operation error, malformed response, invalid successor, or changed identity
aborts the run. The CLI publishes no result directory on that path.

## Retained results

Run the CLI with a pinned JSON configuration:

```
snesbranch -config config.json -config-sha256 SHA256 -out new-directory
```

The output retains the immutable checkpoint, baseline and replica PNGs,
`recovered.c`, `original-ir.json`, `edited-ir.json`,
`recovered-identities.json`, and `result.json`. The IR hashes cover these exact
canonical typed semantic documents, rather than cached presentation strings. The result records
the observed compiler and executable identity, executed replacement count,
physical bus counts and hashes, framebuffer and component hashes, serialized
state hashes, frame geometry and clock boundaries.

An unchanged run should match every frame. An edited run is a derivative
experiment with `captured_proof_eligible=false`. Repeat and reset runs must start
from the original checkpoint. Determinism here is evidence from this runtime;
it is not an independent hardware oracle.
