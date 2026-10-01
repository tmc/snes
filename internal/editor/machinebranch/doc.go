// Package machinebranch repeats a bounded input schedule from an immutable
// complete emulator checkpoint. It records frame and hardware-state identities.
// A narrow compiled C rotation slice can replace instruction execution through
// the runtime's timed bus interface; it does not grant recovery qualification.
package machinebranch
