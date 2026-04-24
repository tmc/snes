// Package updsp implements the NEC uPD77C25 microcontroller used by the SNES
// DSP-1, DSP-1A, DSP-1B, DSP-2, DSP-3, and DSP-4 cartridge coprocessors.
//
// The uPD77C25 core is 24 bits wide with 16-bit data paths and a 16x16 -> 31-bit
// multiplier. Program ROM is 2048 words of 24 bits; data ROM is 1024 words of
// 16 bits; data RAM is 256 words of 16 bits. Instructions fall into four
// classes distinguished by the top two bits of the 24-bit opcode:
//
//   - 00: OP  - ALU operation
//   - 01: RT  - ALU operation followed by RET (return from subroutine)
//   - 10: JP  - jump / call
//   - 11: LD  - load immediate into destination register
//
// The SNES CPU exchanges data with the uPD77C25 through two memory-mapped
// registers, SR and DR. SR is read-only from the CPU side and exposes status
// bits that coordinate data transfers; DR is the 16-bit data register accessed
// as two 8-bit halves. See package design_doc.md section 5.5 for the cartridge
// quirks catalog, in particular the DR/SR half-read synchronization protocol
// and the DSP-1 vs DSP-1B ROM split.
//
// Program ROMs for all variants are Nintendo IP and are NOT embedded. The
// [LoadProgramROM] function reads a variant ROM from a caller-provided path,
// typically the SNES_DSP1_ROM environment variable used by the parity
// harness; when no ROM is available the core will still expose the SR/DR
// protocol in passthrough mode so that unit tests can exercise the
// coprocessor bus without the copyrighted program.
package updsp
