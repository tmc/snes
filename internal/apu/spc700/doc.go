/*
Package spc700 implements the Sony SPC700 8-bit microprocessor, the core of the Super Nintendo Entertainment System's (SNES) audio subsystem.

The SPC700 is the central processing unit of the S-SMP (Sound-CPU Module), operating in parallel with the main SNES CPU (Ricoh 5A22). It controls the S-DSP (Sony Digital Signal Processor) for sample playback, echo, and envelope generation.

# System Architecture

The S-SMP runs asynchronously from the main SNES system, driven by a 24.576 MHz ceramic resonator. The SPC700 CPU operates at a clock divider of 24, resulting in an effective clock speed of 1.024 MHz. It communicates with the main S-CPU strictly through four memory-mapped I/O ports, requiring a handshaking protocol for data transfer.

# Registers

The SPC700 architecture is similar to the MOS 6502 but utilizes a unique instruction set and register layout.

	A (8-bit):   Accumulator. Lower 8 bits of the 16-bit "YA" virtual register.
	Y (8-bit):   Index Register. Upper 8 bits of the 16-bit "YA" virtual register.
	X (8-bit):   Index Register.
	SP (8-bit):  Stack Pointer. Hardwired to memory page $01 ($0100-$01FF).
	PC (16-bit): Program Counter.
	PSW (8-bit): Program Status Word (Flags).

The YA register is a virtual 16-bit register formed by concatenating Y (high byte) and A (low byte), allowing for 16-bit arithmetic operations like ADDW (Add Word) and SUBW (Subtract Word).

Program Status Word (PSW)

The flag layout (NVPBHIZC) differs from the 6502:

	Bit 7 (N): Negative. Set if the most significant bit of the result is set.
	Bit 6 (V): Overflow. Set if a signed arithmetic overflow occurs.
	Bit 5 (P): Direct Page. If 0, Direct Page is $0000-$00FF. If 1, Direct Page is $0100-$01FF.
	Bit 4 (B): Break. Set by the BRK instruction.
	Bit 3 (H): Half-Carry. Used for DAA/DAS decimal adjustments.
	Bit 2 (I): Interrupt Enable. If 1, interrupts are enabled. Note: The S-SMP has no IRQ lines connected.
	Bit 1 (Z): Zero. Set if the result is zero.
	Bit 0 (C): Carry.

# Memory Map

The SPC700 addresses a 64KB address space:

	$0000-$00EF: Zero Page / Direct Page RAM.
	$00F0-$00FF: Memory Mapped I/O (MMIO) Control Registers.
	$0100-$01FF: Stack RAM (and P=1 Direct Page).
	$0200-$FFBF: General Purpose RAM.
	$FFC0-$FFFF: IPL (Initial Program Load) ROM. Contains the bootloader.

The IPL ROM at $FFC0 is enabled on reset. It can be mapped out via the CONTROL register ($00F1), allowing read/write access to the underlying RAM in this region.

MMIO Registers ($00F0-$00FF)

The package must implement the following hardware registers located in the zero page:

	$00F0 (TEST):    Test register. CAUTION: Writes can halt the CPU or disable RAM.
	$00F1 (CONTROL): Timer control, I/O clear, and IPL ROM enable.
	$00F2 (DSPADDR): Index register for the S-DSP (0-127).
	$00F3 (DSPDATA): Data register for the S-DSP.
	$00F4-$00F7:     Communication Ports 0-3 (CPUIO). Bidirectional data exchange with S-CPU.
	$00FA (T0TARGET): Timer 0 Target (8kHz base clock).
	$00FB (T1TARGET): Timer 1 Target (8kHz base clock).
	$00FC (T2TARGET): Timer 2 Target (64kHz base clock).
	$00FD-$00FF:     Timer 0-2 Outputs (Counter values).

# Timing and Synchronization

Accuracy is critical for audio emulation. The SPC700 executes instructions in units of machine cycles, where 1 machine cycle = 2 master clocks (approx 2.048 MHz basis). However, listed instruction timings are typically derived from the 1.024 MHz effective clock.

Timers 0, 1, and 2 must be incremented relative to the master clock. T0 and T1 operate at 8kHz (derived from 24.576MHz / 24 / 128), and T2 operates at 64kHz (derived from 24.576MHz / 24 / 16).

# DSP Interaction

The SPC700 controls the audio generation solely through the DSPADDR ($00F2) and DSPDATA ($00F3) ports. The standard method for writing to DSP registers involves writing the register index to $00F2, followed by the data to $00F3.

Boot Process (IPL)

Upon reset, the PC is set to $FFC0. The internal IPL ROM waits for a handshake signal ($AA, $BB) from the main S-CPU via ports $2140/$2141. Once established, it enters a transfer loop to download the user's sound driver into Audio RAM.

# Errata and Quirks

1. Wrap-Around: Direct Page addressing wraps within the current page (e.g., $00FF + 1 = $0000). Stack operations wrap within page $01.
2. DIV Opcode: The division operation (YA / X) calculates H, V, Z, and N flags based on specific, non-standard rules. It relies on a 9-bit result capability; if the quotient > 511, the results are specific to the hardware implementation.
3. MUL Opcode: The multiply instruction (YA = Y * A) updates the Z and N flags based on the Y register (high byte) only.
4. MOV (X)+: This instruction is an indirect load with auto-increment. It consumes an extra cycle compared to standard loads.
5. CPU/APU Synchronization: Because the APU runs on its own oscillator (24.576 MHz) versus the SNES master clock (~21.47 MHz), the two systems drift. Synchronization must be handled carefully, typically by catching up the APU to the CPU's current timestamp.
*/
package spc700
