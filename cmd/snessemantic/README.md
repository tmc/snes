# snessemantic

Emit an opt-in executable C transformation from pinned ROM and a connected
region configuration:

```
go run ./cmd/snessemantic -config pinned.json -out new-output-directory
```

The configuration has `rom`, `rom_sha256` and `region`. `region` uses the existing
`decomp.ConnectedConfig` fields (without embedded ROM bytes): spans, entry,
context, instruction/step bounds and explicit indirect targets. The ROM must
match the supplied SHA256. The output directory must not exist.

Outputs: original.c, semantic.c and manifest.json. The manifest binds original
and transformed source, the region, ROM and config, and recovered local
expressions with defining instruction IDs. Generation is not execution
qualification. Existing machine-C receipts cannot bless transformed source.

The first pass supports block-local eight-bit loads and immediate ADC with
architectural writeback. It preserves reads, stores, flags and guards. Other
statements retain machine lowering. It does not infer structs, game-purpose
names, cross-block values, structured loops or whole-game equivalence.
