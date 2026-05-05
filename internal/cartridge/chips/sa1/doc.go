// Package sa1 implements the cartridge-facing shell of the SA-1 board.
//
// The current implementation claims the S-CPU control register window and
// preserves it through save states. Commercial-ROM support still requires the
// SA-1 65c816 core, Super MMC ROM mapping, BW-RAM arbitration, DMA/character
// conversion, and bidirectional interrupt handling.
package sa1
