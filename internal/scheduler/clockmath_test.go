package scheduler

import (
	"math/big"
	"math/rand"
	"testing"
)

func TestClockConversion(t *testing.T) {
	max := ^uint64(0)
	for _, tt := range []struct {
		name                            string
		cycles, from, to, after, before uint64
	}{
		{"zero frequency", 10, 0, 1, 0, 0},
		{"zero target", 10, 1, 0, 0, 0},
		{"zero cycles", 0, 3, 2, 0, 0},
		{"exact", 12, 3, 2, 8, 7},
		{"fractional", 13, 3, 2, 9, 8},
		{"same maximum", max, 21_477_272, 21_477_272, max, max - 1},
		{"wide product", max - 3, 100, 25, (max - 3) / 4, (max-3)/4 - 1},
		{"wide fractional", max, 100, 25, max/4 + 1, max / 4},
		{"maximum quotient with remainder", max - 1, max - 2, max - 1, max, max},
		{"unrepresentable quotient", max, 1, 2, max, max},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := targetCyclesAtOrAfter(tt.cycles, tt.from, tt.to); got != tt.after {
				t.Fatalf("at or after=%d want %d", got, tt.after)
			}
			if got := targetCyclesBefore(tt.cycles, tt.from, tt.to); got != tt.before {
				t.Fatalf("before=%d want %d", got, tt.before)
			}
		})
	}
}

func TestClockMathAgainstBigInt(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	max := new(big.Int).SetUint64(^uint64(0))
	for i := 0; i < 1000; i++ {
		a, af, b, bf := rng.Uint64(), rng.Uint64()|1, rng.Uint64(), rng.Uint64()|1
		left := new(big.Int).Mul(new(big.Int).SetUint64(a), new(big.Int).SetUint64(af))
		right := new(big.Int).Mul(new(big.Int).SetUint64(b), new(big.Int).SetUint64(bf))
		if clockBefore(a, af, b, bf) != (left.Cmp(right) < 0) {
			t.Fatal("clock ordering mismatch")
		}
		q, r := new(big.Int), new(big.Int)
		q.QuoRem(right, new(big.Int).SetUint64(af), r)
		before := new(big.Int).Set(q)
		if r.Sign() != 0 {
			q.Add(q, big.NewInt(1))
		} else if before.Sign() != 0 {
			before.Sub(before, big.NewInt(1))
		}
		if q.Cmp(max) > 0 {
			q.Set(max)
		}
		if before.Cmp(max) > 0 {
			before.Set(max)
		}
		if targetCyclesAtOrAfter(b, af, bf) != q.Uint64() || targetCyclesBefore(b, af, bf) != before.Uint64() {
			t.Fatal("clock conversion mismatch")
		}
	}
}

func TestSyncLongRuntime(t *testing.T) {
	const frequency = 21_477_272
	// This boundary is approximately 11 hours into emulation. The old product
	// comparison wraps between the target and master, skipping required work.
	boundary := ^uint64(0) / frequency
	for _, before := range []bool{false, true} {
		s := NewScheduler()
		cpu := &fakeThread{cycles: boundary + 2, frequency: frequency}
		ppu := &fakeThread{cycles: boundary - 2, step: 1, frequency: frequency}
		s.RegisterCPU(cpu, frequency)
		s.RegisterPPU(ppu, frequency)
		want := cpu.cycles
		if before {
			s.SyncBefore(ppu)
			want--
		} else {
			s.Sync(ppu)
		}
		if ppu.cycles != want {
			t.Fatalf("before=%v: cycles=%d want %d", before, ppu.cycles, want)
		}
	}
}

func TestSyncBeforeAlreadyAheadMaximum(t *testing.T) {
	s := NewScheduler()
	cpu := &fakeThread{cycles: 100, frequency: 1}
	ppu := &fakeThread{cycles: ^uint64(0), frequency: 1}
	s.RegisterCPU(cpu, 1)
	s.RegisterPPU(ppu, 1)
	s.Sync(ppu)
	s.SyncBefore(ppu)
	if ppu.runs != 0 {
		t.Fatalf("ran already-ahead thread %d times", ppu.runs)
	}
}

func TestSyncMixedFrequencyLongRuntime(t *testing.T) {
	master := ^uint64(0) - 3
	want := master / 4
	s := NewScheduler()
	cpu := &fakeThread{cycles: master, frequency: 100}
	target := &fakeThread{cycles: want - 2, step: 1, frequency: 25}
	s.RegisterCPU(cpu, 100)
	s.RegisterAPU(target, 25)
	s.Sync(target)
	if target.cycles != want || target.runs != 2 {
		t.Fatalf("mixed-frequency cycles=%d runs=%d, want %d and 2", target.cycles, target.runs, want)
	}
}
