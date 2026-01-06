/*
Package input manages the input devices and polling mechanisms.

It simulates the serial data protocols used by the SNES to communicate with controllers.

Features:
-   **Controller Support**: Standard Joypad, Mouse, Super Scope, and Justifier.
-   **Registers**: Implements the manual reading registers ($4016/$4017) and the automatic reading mechanism ($4218 etc).
-   **State**: Maintains the current button/axis state fed from the frontend.
*/
package input
