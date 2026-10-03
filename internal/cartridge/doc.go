/*
Package cartridge handles the physical game media (Game Pak) and its internal components.

It is responsible for parsing ROM headers, configuring the memory bus mapping (LoROM/HiROM), and simulating on-cartridge hardware.

Responsibilities:
-   **Loading**: Reading the ROM file and validating checksums.
-   **Mapping**: configuring the `bus` to route addresses correctly to ROM limits and RAM.
-   **Expansion Chips**: Hosting implementations of coprocessors such as:
  - **Super FX (GSU)**: RISC processor for 3D polygon rendering.
  - **SA-1**: 65c816 accelerator.
  - **DSP-n**: Fixed-point math coprocessors.
  - **CX4, SDD1, SPC7110**: Other specialized chips.
*/
package cartridge
