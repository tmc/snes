/*
Package input manages SNES input devices and the $4016/$4017 serial-read
protocol.

Four device types share the Device interface (Latch / ReadSerial):

  - StandardController: 16-bit button word, the default SNES gamepad.
  - Mouse: 32-bit report with signature, two buttons, three-level
    sensitivity toggle, and signed 7-bit per-latch dX/dY.
  - SuperScope: 32-bit report with four buttons, off-screen / noise
    flags, and PPU-beam-position capture (H, V) taken on trigger pull
    via a BeamLatcher hook.
  - Multitap: four-controller multiplexer; a Select line picks between
    the {slot 0, slot 1} pair and the {slot 2, slot 3} pair when the
    console reads serial data.

Registers $4016/$4017 and the auto-joypad read mechanism ($4218...$421F)
are implemented by callers of this package; the device types only model
the serial-protocol state machine on a single port.
*/
package input
