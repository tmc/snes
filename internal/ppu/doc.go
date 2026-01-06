/*
Package ppu implements the SNES Picture Processing Unit (S-PPU1 and S-PPU2).

The PPU is responsible for generating the video signal. It is a highly complex scanline-based renderer that supports multiple background layers, sprites, and special effects.

Subsystems:
-   **VRAM**: 64KB of video RAM storing tile data and tilemaps.
-   **OAM** (Object Attribute Memory): Stores data for up to 128 sprites.
-   **CGRAM** (Color Generator RAM): Stores 256 color palettes (15-bit color).
-   **Backgrounds**: 4 independent layers supporting 8 distinct modes (Mode 0-7). Mode 7 allows for affine transformations (rotation/scaling).
-   **Sprites**: Logic for fetching and rendering sprites, including priority arbitration and "8 sprites per tile" / "32 sprites per line" limits.
-   **Windowing & Math**: Color arithmetic (add/sub) and window masking logic.

The PPU runs in sync with the CPU but generates pixels based on specific latch timings (H-Counter/V-Counter).
*/
package ppu
