/*
Package snes implements a cycle-accurate Super Nintendo Entertainment System (SNES) emulator in pure Go.

It orchestrates the various hardware subsystems (CPU, PPU, APU, Bus) to simulate the console's behavior. This package implements the [emulator.Interface], allowing it to be driven by generic frontends.

# Architecture

The system is composed of several internal components:
-   **CPU**: Ricoh 5A22 (65c816), the main processor.
-   **PPU**: S-PPU1/2, the video rendering unit.
-   **APU**: S-SMP (SPC700 + DSP), the audio subsystem.
-   **Bus**: The central memory map router.

The [System] struct wires these components together and manages the main execution loop via the [Scheduler].

Usage

	sys := snes.NewSystem(myFrontend)
	if err := sys.LoadROM(romData); err != nil {
	    log.Fatal(err)
	}
	sys.Power()
	for {
	    if err := sys.RunFrame(); err != nil {
	        log.Fatal(err)
	    }
	}
*/
package snes
