/*
Package bus implements the SNES memory system and address decoding.

It serves as the central "nervous system" of the emulator, routing read and write requests from the CPU to the appropriate hardware components based on the 24-bit address.

Architectural Roles:
-   **Memory Mapping**: distinguishing between LoROM, HiROM, and ExHiROM mapping models based on the cartridge configuration.
-   **Address Decoding**: Routing addresses $00-$3F (System), $7E-$7F (WRAM), and $80-$FF (Cartridge) to their respective devices (PPU, CPU I/O, RAM, ROM).
-   **Open Bus**: Simulating the persistence of the last value on the data bus for reads to unmapped regions.

Interfaces:
The `MemoryDevice` interface allows any component (RAM, PPU, Cartridge) to be attached to the bus.
*/
package bus
