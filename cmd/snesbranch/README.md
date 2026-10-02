# snesbranch

Repeat an explicitly pinned complete-machine checkpoint and publish frame
artifacts into a new directory:

```
go run ./cmd/snesbranch -config /path/to/config.json \
  -config-sha256 <config-file-sha256> -out /path/to/new-run
```

Compiled replacement modes require an explicit `region` object in the config.
It supplies a LoROM CPU start, byte length, code SHA-256, low-WRAM data bank,
allowed raw bus cells, ADC immediate address, original immediate and permitted
replacement values. There is no default game routine. The current timing
vocabulary supports bounded native binary 8-bit low-WRAM instructions only.

For `recovered_c`, prepare fresh source and IR identities from the pinned ROM:

```
go run ./cmd/snesbranch -prepare-recovered \
  -config /path/to/selected.json -config-sha256 <selected-file-sha256> \
  -out /path/to/new-prepared.json
```

Preparation creates a new config file; it does not run the machine, emit frame
captures or grant recovery qualification. Measure that file before executing
it. Source and timing identities are separate from captured proof.
