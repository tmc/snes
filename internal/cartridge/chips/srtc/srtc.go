// Package srtc implements the Sharp RTC (S-RTC) coprocessor.
// The chip exposes a 4-bit serial protocol
// over MMIO at $2800 (read) and $2801 (write) in banks $00-$3F (and
// $80-$BF mirrors). 13 BCD register cells hold the date/time
// (second/minute/hour/day/month/year/weekday); writes auto-calculate
// the weekday on the 12th cell write.
//
// References:
//
//   - bsnes/sfc/coprocessor/sharprtc/sharprtc.cpp (protocol state machine)
//   - bsnes/sfc/coprocessor/sharprtc/memory.cpp (rtcRead/rtcWrite + load/save)
//   - bsnes/sfc/coprocessor/sharprtc/time.cpp (tick chain + leap-year + weekday)
//   - bsnes/heuristics/super-famicom.cpp:298 (detection: cartridgeTypeHi == 0x5)
package srtc

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"time"
)

// State enumerates the 4-state serial protocol. Bsnes
// sharprtc.cpp:16 defines the same.
type State uint8

const (
	StateReady State = iota
	StateCommand
	StateRead
	StateWrite
)

// daysInMonth mirrors bsnes time.cpp:1.
var daysInMonth = [12]uint{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

// Clock returns the current wall time. Tests inject a deterministic
// clock; production wires time.Now.
type Clock func() time.Time

// Device is the Sharp RTC chip surface. It implements the cartridge
// Coprocessor interface, returning ok=true only for $2800/$2801 hits.
type Device struct {
	state State
	index int

	second  uint
	minute  uint
	hour    uint
	day     uint
	month   uint
	year    uint
	weekday uint

	now Clock
}

// New returns a fresh S-RTC device with a deterministic clock injected.
// Pass time.Now in production; tests pass a fake.
func New(now Clock) *Device {
	if now == nil {
		now = time.Now
	}
	d := &Device{now: now, state: StateRead, index: -1}
	return d
}

// SetClock replaces the wall-clock source. Useful for state-restored
// runs that want to advance from the snapshot moment.
func (d *Device) SetClock(now Clock) {
	if now != nil {
		d.now = now
	}
}

// SyncTime initializes the date/time fields from the current clock.
// bsnes calls this from cartridge load when no battery-backed RTC
// data is supplied.
func (d *Device) SyncTime() {
	t := d.now()
	d.second = uint(t.Second())
	if d.second > 59 {
		d.second = 59
	}
	d.minute = uint(t.Minute())
	d.hour = uint(t.Hour())
	d.day = uint(t.Day())
	d.month = uint(t.Month())
	d.year = uint(t.Year() - 1000) // Sharp RTC year is offset from 1000
	d.weekday = uint(t.Weekday())
}

// Read implements the Coprocessor interface. Returns ok=true only
// for $2800 reads. Mirrors bsnes sharprtc.cpp:62-80.
func (d *Device) Read(addr uint32) (uint8, bool) {
	bank := (addr >> 16) & 0xff
	offset := addr & 0xffff
	if !((bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) && offset == 0x2800) {
		return 0, false
	}
	if d.state != StateRead {
		return 0, true
	}
	if d.index < 0 {
		d.index++
		return 15, true
	}
	if d.index > 12 {
		d.index = -1
		return 15, true
	}
	v := d.rtcRead(uint8(d.index))
	d.index++
	return v, true
}

// Write implements the Coprocessor interface. Returns true only for
// $2801 writes. Mirrors bsnes sharprtc.cpp:82-132.
func (d *Device) Write(addr uint32, val uint8) bool {
	bank := (addr >> 16) & 0xff
	offset := addr & 0xffff
	if !((bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) && offset == 0x2801) {
		return false
	}
	data := val & 0x0F

	if data == 0x0D {
		d.state = StateRead
		d.index = -1
		return true
	}
	if data == 0x0E {
		d.state = StateCommand
		return true
	}
	if data == 0x0F {
		// snes9x/bsnes both treat 0x0F as unknown / no-op.
		return true
	}

	switch d.state {
	case StateCommand:
		switch data {
		case 0:
			d.state = StateWrite
			d.index = 0
		case 4:
			// Reset command per bsnes sharprtc.cpp:103-114.
			d.state = StateReady
			d.index = -1
			d.second = 0
			d.minute = 0
			d.hour = 0
			d.day = 0
			d.month = 0
			d.year = 0
			d.weekday = 0
		default:
			d.state = StateReady
		}
	case StateWrite:
		if d.index >= 0 && d.index < 12 {
			d.rtcWrite(uint8(d.index), data)
			d.index++
			if d.index == 12 {
				// Auto-calculate weekday after the 12 BCD cells are
				// fully written. Year value lives in cells 9/10/11
				// (year ones / tens / hundreds). Day cell is 1-based;
				// month cell is 1-based per bsnes time.cpp.
				d.weekday = calculateWeekday(1000+d.year, d.month, d.day)
			}
		}
	}
	return true
}

// Step is required by the Coprocessor interface. The S-RTC's clock
// advances independently of CPU cycles; we sample the host clock on
// each protocol read instead. Step is therefore a no-op.
func (d *Device) Step(masterCycles uint64) {}

// rtcRead returns the BCD nibble at the given cell index per bsnes
// memory.cpp:1-18.
func (d *Device) rtcRead(addr uint8) uint8 {
	switch addr {
	case 0:
		return uint8(d.second % 10)
	case 1:
		return uint8(d.second / 10)
	case 2:
		return uint8(d.minute % 10)
	case 3:
		return uint8(d.minute / 10)
	case 4:
		return uint8(d.hour % 10)
	case 5:
		return uint8(d.hour / 10)
	case 6:
		return uint8(d.day % 10)
	case 7:
		return uint8(d.day / 10)
	case 8:
		return uint8(d.month)
	case 9:
		return uint8(d.year % 10)
	case 10:
		return uint8(d.year / 10 % 10)
	case 11:
		return uint8(d.year / 100)
	case 12:
		return uint8(d.weekday)
	}
	return 0
}

// rtcWrite stores a BCD nibble at the given cell index per bsnes
// memory.cpp:20-36.
func (d *Device) rtcWrite(addr uint8, data uint8) {
	v := uint(data) & 0x0F
	switch addr {
	case 0:
		d.second = d.second/10*10 + v
	case 1:
		d.second = v*10 + d.second%10
	case 2:
		d.minute = d.minute/10*10 + v
	case 3:
		d.minute = v*10 + d.minute%10
	case 4:
		d.hour = d.hour/10*10 + v
	case 5:
		d.hour = v*10 + d.hour%10
	case 6:
		d.day = d.day/10*10 + v
	case 7:
		d.day = v*10 + d.day%10
	case 8:
		d.month = v
	case 9:
		d.year = d.year/10*10 + v
	case 10:
		d.year = d.year/100*100 + v*10 + d.year%10
	case 11:
		d.year = v*100 + d.year%100
	case 12:
		d.weekday = v
	}
}

// TickSecond advances the clock one second, cascading minute → hour
// → day → month → year as needed. Mirrors bsnes time.cpp:3-44.
// Exposed for tests that drive the chip without going through real
// host time.
func (d *Device) TickSecond() {
	d.second++
	if d.second < 60 {
		return
	}
	d.second = 0
	d.tickMinute()
}

func (d *Device) tickMinute() {
	d.minute++
	if d.minute < 60 {
		return
	}
	d.minute = 0
	d.tickHour()
}

func (d *Device) tickHour() {
	d.hour++
	if d.hour < 24 {
		return
	}
	d.hour = 0
	d.tickDay()
}

func (d *Device) tickDay() {
	days := daysInMonth[(d.month-1)%12]
	// Add one day to February in leap years.
	if d.month == 2 {
		switch {
		case d.year%400 == 0:
			days++
		case d.year%100 == 0:
			// Not a leap year in centuries divisible by 100 (but not 400).
		case d.year%4 == 0:
			days++
		}
	}
	d.day++
	if d.day <= days {
		return
	}
	d.day = 1
	d.tickMonth()
}

func (d *Device) tickMonth() {
	d.month++
	if d.month <= 12 {
		return
	}
	d.month = 1
	d.year = (d.year + 1) & 0xFFF // 12-bit year per bsnes time.cpp:42-44
}

// calculateWeekday returns the day of week for (year, month, day).
// 0 = Sunday, 1 = Monday, ..., 6 = Saturday. Sharp RTC epoch is
// 1000-01-01 which was a Wednesday. Mirrors bsnes time.cpp:50-83.
func calculateWeekday(year, month, day uint) uint {
	if year < 1000 {
		year = 1000
	}
	if month < 1 {
		month = 1
	}
	if month > 12 {
		month = 12
	}
	if day < 1 {
		day = 1
	}
	if day > 31 {
		day = 31
	}
	y, m := uint(1000), uint(1)
	sum := uint(0)
	for y < year {
		leap := false
		if y%4 == 0 {
			leap = true
			if y%100 == 0 && y%400 != 0 {
				leap = false
			}
		}
		sum += 365
		if leap {
			sum++
		}
		y++
	}
	for m < month {
		days := daysInMonth[(m-1)%12]
		leapMonth := false
		if days == 28 {
			if y%4 == 0 {
				leapMonth = true
				if y%100 == 0 && y%400 != 0 {
					leapMonth = false
				}
			}
		}
		sum += days
		if leapMonth {
			sum++
		}
		m++
	}
	sum += day - 1
	return (sum + 3) % 7 // 1000-01-01 was a Wednesday
}

// state is the gob-serializable snapshot of the device.
type state struct {
	State   uint8
	Index   int
	Second  uint
	Minute  uint
	Hour    uint
	Day     uint
	Month   uint
	Year    uint
	Weekday uint
}

// Serialize implements the cartridge Coprocessor interface.
func (d *Device) Serialize() ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(state{
		State:   uint8(d.state),
		Index:   d.index,
		Second:  d.second,
		Minute:  d.minute,
		Hour:    d.hour,
		Day:     d.day,
		Month:   d.month,
		Year:    d.year,
		Weekday: d.weekday,
	}); err != nil {
		return nil, fmt.Errorf("serialize srtc: %w", err)
	}
	return buf.Bytes(), nil
}

// Unserialize implements the cartridge Coprocessor interface.
func (d *Device) Unserialize(data []byte) error {
	var s state
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&s); err != nil {
		return fmt.Errorf("unserialize srtc: %w", err)
	}
	if s.State > uint8(StateWrite) || s.Index < -1 || s.Index > 13 {
		return fmt.Errorf("unserialize srtc: invalid protocol state")
	}
	d.state = State(s.State)
	d.index = s.Index
	d.second = s.Second
	d.minute = s.Minute
	d.hour = s.Hour
	d.day = s.Day
	d.month = s.Month
	d.year = s.Year
	d.weekday = s.Weekday
	return nil
}

// Power restarts the serial protocol while preserving date/time and the clock.
// See bsnes sfc/coprocessor/sharprtc/sharprtc.cpp, SharpRTC::power.
func (d *Device) Power() {
	d.state = StateRead
	d.index = -1
}
