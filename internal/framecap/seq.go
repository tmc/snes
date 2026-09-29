package framecap

// seqClockSize is the number of recent observations a SeqClock
// remembers: about four frames of the densest instruction stream.
const seqClockSize = 1 << 17

// A SeqClock maps cycles to trace sequence numbers. It remembers the
// entry cycles of recent observations.
type SeqClock struct {
	ring []uint64
	last uint64 // last observed seq, or 0
}

// NewSeqClock returns an empty SeqClock.
func NewSeqClock() *SeqClock {
	return &SeqClock{ring: make([]uint64, seqClockSize)}
}

// Observe records that observation seq entered at cycles. Sequence
// numbers must be consecutive and cycles nondecreasing.
func (c *SeqClock) Observe(seq, cycles uint64) {
	c.ring[seq%seqClockSize] = cycles
	c.last = seq
}

// First returns the first seq whose entry cycle is at or after cycle.
// It reports false if that seq is not yet known or has been forgotten.
// Once no further observations will be made, a seq not yet known is
// Next.
func (c *SeqClock) First(cycle uint64) (uint64, bool) {
	if c == nil || c.last == 0 || c.ring[c.last%seqClockSize] < cycle {
		return 0, false
	}
	lo := uint64(1)
	if c.last > seqClockSize {
		lo = c.last - seqClockSize + 1
	}
	if c.ring[lo%seqClockSize] >= cycle {
		if lo == 1 {
			return 1, true
		}
		return 0, false // an earlier, forgotten seq may qualify
	}
	// ring[lo] < cycle <= ring[last]
	hi := c.last
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if c.ring[mid%seqClockSize] >= cycle {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi, true
}

// Next returns the seq the next observation will have.
func (c *SeqClock) Next() uint64 { return c.last + 1 }

// passed reports whether an observation at or after cycle is known.
func (c *SeqClock) passed(cycle uint64) bool {
	return c.last != 0 && c.ring[c.last%seqClockSize] >= cycle
}
