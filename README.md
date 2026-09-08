# SNES emulator in Go

A Super Nintendo emulator with a Go CPU, video, audio, and cartridge core.
The desktop player uses Ebiten; reference tests use native libretro cores.
Timing and game compatibility remain under development.

## Run

Use the Go version declared in `go.mod`. Supply your own ROM:

```sh
go run ./cmd/snes -fast-forward=false game.sfc
```

Arrow keys move; X/Z map to A/B, S/A to X/Y, Q/W to L/R, Enter to Start,
and right Shift to Select. F6 saves state, F9 restores it, and Backspace rewinds.

For a bounded headless run:

```sh
go run ./cmd/snes -frames 60 -frame-log frames.csv game.sfc
```

Use `-input-script` for recorded input, `-load-state` to resume, and
`-save-state` to retain the resulting state. State format 2 includes hardware
latches omitted by format 1; format-1 files are rejected. Save states require
the matching ROM by default and are not a stable format across versions. Current
states use version 3; older states are rejected. Save/restore requires an
instruction boundary and returns an error during active CPU or DMA execution.
Battery-backed cartridge RAM is a separate save format.

## Cartridge admission

Normal loading rejects SA-1, S-DD1, SPC7110, ST-010/ST-011, ST-018 and Super
Game Boy execution. GSU, Cx4, OBC-1 and S-RTC have implemented paths and focused
tests; that does not establish complete game compatibility.

DSP cartridges require firmware. Set `SNES_DSP1_ROM` to an 8 KiB combined
program/data image for DSP-1 (substitute DSP1A, DSP1B, DSP2, DSP3 or DSP4 for
other variants). Separate `SNES_DSP1_PROGRAM_ROM` and `SNES_DSP1_DATA_ROM`
files are also accepted. Size checks do not identify firmware contents.
When the game hash is unknown, select the variant explicitly:

```sh
go run ./cmd/snes -dsp-variant DSP-1 -fast-forward=false game.sfc
```

`-diagnostic-passthrough` permits inspection of incomplete hardware or absent
DSP firmware; it does not implement that hardware. The library exposes the
same choices through `LoadROMWithOptions`. Default loading never enables this
mode implicitly.

## Control and inspection

`cmd/snesprobe` serves JSON requests over stdio or a Unix socket, and can serve
gRPC. These interfaces expose local file access and emulator control; run them
in a trusted local environment. Unix sockets use private permissions and refuse
to replace ordinary files or active listeners. A persistent `.lock` companion
serializes cooperating startup and cleanup; keep it while using the socket. See the
[request limits and cancellation contract](internal/snesprobe/README.md).

```sh
go run ./cmd/snesprobe serve
```

`cmd/snestrace` provides trace/replay tools; `cmd/snesdis` disassembles ROMs.
Run `snestrace run -help` or `snesdis -help` for their options. Library consumers construct
`NewSystem(nil)`, load a ROM, power the machine, and call `RunFrame`. Runnable
examples cover program execution and save/restore. Direct `System` access is
single-goroutine; the probe service serializes its clients.

## Test and qualify

```sh
mkdir -p "$HOME/tmp/snes-qualification"
TMPDIR="$HOME/tmp" make test
TMPDIR="$HOME/tmp" make qualify \
  MANIFEST=cmd/snesqualify/testdata/synthetic.json \
  OUT="$HOME/tmp/snes-qualification"
```

Ordinary tests may skip unavailable ROMs or reference cores. Qualification
requires an exact named test, positive comparison counts, and no skips; it
retains a receipt even on failure. The bundled fixture tests the runner itself.
It is not a game-compatibility certificate. See
[qualification manifests and artifact identity](cmd/snesqualify/README.md).

The current integrated suite still exposes an audio-onset mismatch (168
versus the historical 166 samples). Sample count and SPCSMP comparisons pass
in the final full-suite run. Initialized-WRAM comparisons pass against locally
installed bsnes and snes9x cores. Whole-library compatibility, dot-accurate
rendering, and all enhancement chips have not been qualified.

## Source layout

- `snes`: system lifecycle, execution, input, state and inspection APIs.
- `emulator`: frontend interface and shared display/input types.
- `internal/cpu`, `ppu`, `apu`: console processors and rendering/audio.
- `internal/bus`, `dma`, `scheduler`: mapping, transfers and execution timing.
- `internal/cartridge`: board mapping, firmware and enhancement chips.
- `internal/parity`: synthetic, ROM and reference comparisons.

SPC opcode names are generated from pinned bsnes source. Follow the
[regeneration instructions](cmd/spc-codegen/README.md); edit the generator first.
