# Pure Go SNES Emulator

A cycle-accurate Super Nintendo Entertainment System (SNES) emulator written in pure Go (Golang).

## Architecture
Port of the `bsnes` architecture, implementing a modular design:
-   `emulator`: Generic emulator interface.
-   `snes`: Core system coordinator.
-   `internal/cpu`: 65c816 CPU core.
-   `internal/ppu`: S-PPU video renderer.
-   `internal/apu`: S-SMP (SPC700) audio subsystem.
-   `internal/bus`: Memory mapping and address routing.

## Usage
Run the emulator with a ROM file:

```bash
go run ./cmd/snes <path-to-rom.sfc>
```

