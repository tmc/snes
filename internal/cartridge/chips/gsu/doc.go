// Package gsu implements the Super FX (GSU) coprocessor.
//
// The GSU is a RISC-like 16-bit processor with sixteen general-purpose
// registers (R0..R15 where R15 aliases the program counter), a small
// status register (SFR) carrying the flags and prefix state, and
// bank-selection registers for ROM (ROMBR), RAM (RAMBR), and program
// (PBR) memory. Pixel rendering is performed through a separate 8-pixel
// horizontal cache that is flushed into a character-base-row (CBR) tile
// only when the next plot changes the current row.
//
// This package targets phase 10 of the pure-Go SNES roadmap: a
// correctly-shaped core, the arithmetic/branch/memory opcode surface, and
// the three pixel-pipeline quirks called out in design_doc.md §5:
//
//   - Exclusive ALT1/ALT2/ALT3 prefix bits that modify the next opcode and
//     self-clear.
//   - FMULT vs LMULT sharing an opcode slot via the ALT prefix.
//   - Pixel-cache deferred commit: the cache writes to RAM only on a CBR
//     row change, not on every PLOT.
//
// The package does not (yet) perform bus writes against PPU VRAM directly;
// instead it records the commit through a pluggable VRAMWriter hook that
// the outer system will bind once the cartridge bus owns a VRAM-write path.
package gsu
