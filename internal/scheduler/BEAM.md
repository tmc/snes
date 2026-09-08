# Display-frame and NMI events

A video thread may implement `BeamEvents() (frameStart, vblankStart uint64)`.
Both values are timestamps in CPU master clocks. Zero means that edge has not
yet occurred since reset. The PPU owns these timestamps; scheduler observation
and display-return consumption have separate serialized timestamps.

`RunDisplayFrame` returns the next vblank event after call entry once the CPU
reaches it. The first normal frame therefore returns at line 225, and the first overscan
frame at line 240, in either region. Later returns follow the PPU field counter,
including the NTSC odd-field short line and PAL interlace long line. If a full-frame call or explicit synchronization has already crossed an edge
before entry, that old edge is consumed and the next one is awaited. CPU instruction granularity can carry
the CPU beyond the exact event timestamp. Rounding the PPU ahead of the CPU
during synchronization must not deliver an event early.

`RunFrame` remains the existing fixed-duration API, including its historical
average NTSC budget. It now receives NMI from beam events when a PPU is present;
its duration is not redefined by this change. Thread-only scheduler fixtures
retain the previous synthetic schedule because they provide no beam events.

The NMI beam level is separate from the read-to-clear `$4210` flag. Reading the
flag cannot replay a consumed event. NMITIMEN writes synchronize the PPU before
changing the enable bit, so a newly discovered edge is observed under the old
enable state; the existing rising-enable path then consults the latched flag.
SETINI updates the vblank threshold immediately. Interlace field length remains
captured at line 128. Rendering still produces complete scanlines.

Reference: bsnes `9144b5ac557f6bd62aacefb87ee31cf5e12a476b`,
`sfc/ppu/io.cpp:updateVideoMode`, `sfc/ppu/counter/counter-inline.hpp:tickScanline`,
and `sfc/cpu/irq.cpp:nmiPoll`. The first two establish the 225/240 boundaries,
line-128 capture and field periods used by independent root tests. The CPU
reference additionally models a two-clock counter communication delay and a
four-clock NMI polling/hold pipeline. This change preserves the current
instruction-boundary interrupt delivery abstraction; it does not claim that
that finer CPU polling latency is implemented or qualified.

The root matrix exercises all region/overscan/interlace combinations over three
fields and observes real NMI handler WRAM writes. Restore cases cover before,
at and after the edge. Separate tests reject normal-line NMI in overscan and
PPU-ahead-of-CPU delivery, and verify SETINI changes and flag read-clear behavior.
No audio or video golden was changed.
