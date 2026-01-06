/*
Package emulator defines the public API for the SNES emulator.

It provides a high-level facade for frontend applications (such as GUIs or CLI tools) to interact with the core emulator logic without needing to understand the internal component architecture.

Responsibilities:
-   System Initialization: Loading ROMs and setting up the emulator state.
-   Execution Control: Stepping frames, running cycles, and managing the main loop.
-   Input Handling: accepting controller input from the host system.
-   Audio/Video Output: Providing access to the framebuffer and audio sample buffer.
-   State Management: Save states (serialization) and configuration.

Typical Usage:

	sys := snes.NewSystem(myInterface)
	sys.Load(romData)
	for {
	    sys.Run() // Executes one frame
	}
*/
package emulator
