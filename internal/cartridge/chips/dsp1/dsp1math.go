package dsp1

// Fixed-point math helpers ported from snes9x dsp1.cpp.
// Source: github.com/bsnes-emu/snes9x dsp1.cpp (GPLv2).
// Functions intentionally mirror the C reference one-for-one so behaviour
// can be diffed against snes9x and bsnes traces.

// inverse computes a fixed-point Newton-Raphson reciprocal of (coef, exp)
// using the DSP1ROM seed at offset 0x0065. Mirrors snes9x DSP1_Inverse.
func inverse(coef, exp int16) (iCoef, iExp int16) {
	if coef == 0 {
		return 0x7fff, 0x002f
	}
	sign := int16(1)
	if coef < 0 {
		if coef < -32767 {
			coef = -32767
		}
		coef = -coef
		sign = -1
	}
	for coef < 0x4000 {
		coef <<= 1
		exp--
	}
	if coef == 0x4000 {
		if sign == 1 {
			iCoef = 0x7fff
		} else {
			iCoef = -0x4000
			exp--
		}
	} else {
		i := int16(dsp1ROM[((int32(coef)-0x4000)>>7)+0x0065])
		i = (i + int16(-int32(i)*(int32(coef)*int32(i)>>15)>>15)) << 1
		i = (i + int16(-int32(i)*(int32(coef)*int32(i)>>15)>>15)) << 1
		iCoef = i * sign
	}
	iExp = 1 - exp
	return
}

// sinFP returns DSP1_Sin(angle), the DSP-1 quarter-wave sine using
// SinTable + MulTable interpolation. Mirrors snes9x DSP1_Sin.
func sinFP(angle int16) int16 {
	if angle < 0 {
		if angle == -32768 {
			return 0
		}
		return -sinFP(-angle)
	}
	hi := int32(angle) >> 8
	lo := int32(angle) & 0xff
	s := int32(dsp1SinTable[hi]) + (int32(dsp1MulTable[lo])*int32(dsp1SinTable[0x40+hi]))>>15
	if s > 32767 {
		s = 32767
	}
	return int16(s)
}

// cosFP returns DSP1_Cos(angle). Mirrors snes9x DSP1_Cos, including the
// cos(-32768) = -32768 edge.
func cosFP(angle int16) int16 {
	if angle < 0 {
		if angle == -32768 {
			return -32768
		}
		angle = -angle
	}
	hi := int32(angle) >> 8
	lo := int32(angle) & 0xff
	s := int32(dsp1SinTable[0x40+hi]) - (int32(dsp1MulTable[lo])*int32(dsp1SinTable[hi]))>>15
	if s < -32768 {
		s = -32767
	}
	return int16(s)
}

// normalize mirrors snes9x DSP1_Normalize. It shifts m left until the top
// bit of the magnitude is 1 (within 15 bits) and adjusts *exponent
// accordingly, scaling via DSP1ROM[0x21+e].
func normalize(m int16, exp int16) (coef, expOut int16) {
	var e int16
	i := int16(0x4000)
	if m < 0 {
		for (m&i) != 0 && i != 0 {
			i >>= 1
			e++
		}
	} else {
		for (m&i) == 0 && i != 0 {
			i >>= 1
			e++
		}
	}
	if e > 0 {
		coef = int16(int32(m) * int32(dsp1ROM[0x21+e]) << 1)
	} else {
		coef = m
	}
	expOut = exp - e
	return
}

// normalizeDouble mirrors snes9x DSP1_NormalizeDouble for a 32-bit product
// split into m=hi15 and n=lo15. Uses the same DSP1ROM lookups.
func normalizeDouble(product int32) (coef, expOut int16) {
	n := int16(product & 0x7fff)
	m := int16(product >> 15)
	var e int16
	i := int16(0x4000)
	if m < 0 {
		for (m&i) != 0 && i != 0 {
			i >>= 1
			e++
		}
	} else {
		for (m&i) == 0 && i != 0 {
			i >>= 1
			e++
		}
	}
	if e > 0 {
		coef = int16(int32(m) * int32(dsp1ROM[0x0021+e]) << 1)
		if e < 15 {
			coef += int16(int32(n) * int32(dsp1ROM[0x0040-e]) >> 15)
		} else {
			i = 0x4000
			if m < 0 {
				for (n&i) != 0 && i != 0 {
					i >>= 1
					e++
				}
			} else {
				for (n&i) == 0 && i != 0 {
					i >>= 1
					e++
				}
			}
			if e > 15 {
				coef = int16(int32(n) * int32(dsp1ROM[0x0012+e]) << 1)
			} else {
				coef += n
			}
		}
	} else {
		coef = m
	}
	expOut = e
	return
}

// truncate mirrors snes9x DSP1_Truncate, saturating positive/negative
// exponents and shifting via DSP1ROM[0x0031+E] when E < 0.
func truncate(c, e int16) int16 {
	if e > 0 {
		if c > 0 {
			return 32767
		}
		if c < 0 {
			return -32767
		}
		return c
	}
	if e < 0 {
		return int16(int32(c) * int32(dsp1ROM[0x0031+e]) >> 15)
	}
	return c
}

// shiftR mirrors snes9x DSP1_ShiftR (a one-line helper used by Project).
func shiftR(c, e int16) int16 {
	return int16(int32(c) * int32(dsp1ROM[0x0031+e]) >> 15)
}
