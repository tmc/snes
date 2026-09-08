# SPC700 opcode names

From the repository root, regenerate the names table using a local bsnes checkout:

```sh
go run ./cmd/spc-codegen ../bsnes/bsnes/processor/spc700/instruction.cpp > internal/apu/spc700/opcodes.gen.go
```

The current input was verified at bsnes revision
`9144b5ac557f6bd62aacefb87ee31cf5e12a476b`; its SHA-256 is recorded in the
output. The generator requires all 256 unique opcode declarations, sorts
by opcode, formats Go source, and emits no timestamp. It generates names
only; instruction implementations are separate source files.
