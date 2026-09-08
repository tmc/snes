# Qualification receipts

Run from the repository root, with receipts outside the checkout:

```
mkdir -p "$HOME/tmp/snes-qualification"
TMPDIR="$HOME/tmp" make qualify \
  MANIFEST=cmd/snesqualify/testdata/synthetic.json \
  OUT="$HOME/tmp/snes-qualification"
```

The artifact-free fixture qualifies runner mechanics, not emulator compatibility.
A manifest lists `artifacts` and `cases`. Each case selects one package, one exact
Go test name (including optional slash-separated subtests), and a positive
`min_comparisons`. The selected test must run, pass, and print
`QUALIFY comparisons=N` after its successful comparisons. Emit this with
`fmt.Printf`, not `t.Logf`, so the record occupies its own output line.
The package must pass, no selected tests may skip, and observed comparisons
must reach the requested minimum. Exit status alone never qualifies a case.
Each invocation writes `receipt.json`, including failed preflights and tests.
Use a distinct output directory per invocation to preserve historical receipts.

Example artifact entry:

```json
{
  "path": "/owned/reference/core.dylib",
  "sha256": "<64 lowercase hex digits>",
  "kind": "core",
  "env": "SNES_QUALIFY_BSNES_CORE",
  "build": {
    "path": "/owned/reference/build.json",
    "sha256": "<64 lowercase hex digits>",
    "source_revision": "<pinned revision>"
  }
}
```

Kinds are `rom`, `firmware`, `core`, and `fixture`. Paths are relative to the
working directory unless absolute. Optional `env` bindings pass the verified
absolute artifact path to tests; use the variable that the test actually reads.
A core requires a separately hashed JSON build record containing `binary_sha256`,
`source_revision`, `compiler`, and `flags` (a string array). Its binary digest and
revision must match the manifest. This checks consistency of supplied provenance;
it does not reproduce the reference build. Supply every runtime dependency in
the artifact inventory, including firmware. Manifests and tests remain the
reviewed definition of the evidence contract.

Ordinary developer tests retain optional-reference skips. Qualification rejects
those skips. The self-hosted workflow is enabled only by the repository variable
`SNES_QUALIFICATION=enabled`, uses a runner labeled `snes-qualification`, and reads
the locally provisioned manifest path from `SNES_QUALIFICATION_MANIFEST`.
ROMs, firmware, reference binaries, and local receipts are never uploaded to git.

Current table-driven parity has an artifact-free ROM whose 65816 program writes
`5a a5` to `$7e2000` and loops. `TestPublicLoaderSmoke` checks the literal result
through `LoadROM`/`Power`/`RunFrame`; `TestTableDrivenParity/initialized-wram`
compares exactly those two initialized bytes with each installed reference.

Historical ROM hashes remain in `TestLegacyROMSmoke`, using their original
manual mapping and display-frame boundary. Their existing manifest comments
explicitly identify them as Go smoke hashes. They cannot be migrated to the
public full-frame API by changing the test harness alone. No hashes were
regenerated. The historical ROMs' reference subtests require declared `windows`
entries with `region` (currently `WRAM`), `start`, and positive `length`; without
ROM-specific initialization and frame-alignment evidence these explicitly skip.
Whole WRAM is not a substitute: reference cores initialize untouched RAM
differently. Qualification should pin a concrete initialized-window test.
