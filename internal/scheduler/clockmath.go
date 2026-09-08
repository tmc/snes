package scheduler

import "math/bits"

// clockBefore compares a*af with b*bf without overflowing either product.
func clockBefore(a, af, b, bf uint64) bool {
	ah, al := bits.Mul64(a, af)
	bh, bl := bits.Mul64(b, bf)
	return ah < bh || ah == bh && al < bl
}

// scaledClock returns floor(cycles*to/from), its remainder and whether the
// quotient exceeds uint64. from must be nonzero. Only an unrepresentable
// quotient saturates; an overflowing intermediate product still has an exact
// 128-bit representation.
func scaledClock(cycles, from, to uint64) (q, r uint64, overflow bool) {
	hi, lo := bits.Mul64(cycles, to)
	if hi >= from {
		return ^uint64(0), 0, true
	}
	q, r = bits.Div64(hi, lo, from)
	return q, r, false
}

func targetCyclesAtOrAfter(masterCycles, masterFrequency, targetFrequency uint64) uint64 {
	if masterFrequency == 0 || targetFrequency == 0 {
		return 0
	}
	q, r, overflow := scaledClock(masterCycles, masterFrequency, targetFrequency)
	// The API cannot express a target beyond MaxUint64. Use the largest
	// representable target; do not wrap to a past clock.
	if overflow || q == ^uint64(0) {
		return ^uint64(0)
	}
	if r != 0 {
		q++
	}
	return q
}

func targetCyclesBefore(masterCycles, masterFrequency, targetFrequency uint64) uint64 {
	if masterFrequency == 0 || targetFrequency == 0 {
		return 0
	}
	q, r, overflow := scaledClock(masterCycles, masterFrequency, targetFrequency)
	if overflow {
		return ^uint64(0)
	}
	if r == 0 && q != 0 {
		q--
	}
	return q
}
