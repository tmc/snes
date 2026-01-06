/*
Package apu implements the audio subsystem of the SNES.

It acts as a container for the S-SMP (Sound Module) and S-DSP (Digital Signal Processor). Unlike the main CPU and PPU, the APU runs on an independent clock (24.576 MHz) and executes asynchronously.

Components:
-   **spc700**: The 8-bit CPU core that controls the audio hardware.
-   **dsp**: The DSP responsible for sample mixing, ADSR envelope generation, and echo effects.
-   **RAM**: 64KB of dedicated audio RAM.

Communication with the main system is achieved strictly through 4 memory-mapped I/O ports ($2140-$2143).
*/
package apu
