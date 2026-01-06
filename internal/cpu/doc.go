/*
Package cpu implements the Ricoh 5A22 microprocessor, the central processing unit of the SNES.

The 5A22 is based on the WDC 65c816, a 16-bit successor to the MOS 6502. It includes custom features such as a DMA controller, multiplication/division registers, and automatic wait-state generation.

Key Features:
-   **Dual Mode**: Operates in "Emulation Mode" (6502 compatible) or "Native Mode" (65816).
-   **Variable Register Width**: Accumulator (A) and Index Registers (X, Y) can be switched between 8-bit and 16-bit widths dynamically using `REP` and `SEP` instructions.
-   **24-bit Addressing**: Accesses up to 16MB of address space via Program Bank (PB) and Data Bank (DB) registers.
-   **DMA/HDMA**: Internal controller for high-speed memory transfers (DMA) and H-Blank transfers (HDMA).

Implementation Details:
The CPU executes instructions cycle-by-cycle (or block-by-block) and synchronizes with the PPU and APU via the scheduler. It delegates memory access to the `bus` package.
*/
package cpu
