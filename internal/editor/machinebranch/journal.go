package machinebranch

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"

	"github.com/tmc/snes"
)

type busJournal struct {
	hash   hash.Hash
	events uint64
}

func journal(s *snes.System) *busJournal {
	j := &busJournal{hash: sha256.New()}
	record := func(op byte, address uint32, value uint8) {
		var b [14]byte
		b[0] = op
		binary.LittleEndian.PutUint32(b[1:5], address)
		b[5] = value
		binary.LittleEndian.PutUint64(b[6:], s.CPU.Cycles)
		j.hash.Write(b[:])
		j.events++
	}
	s.Bus.ReadHook = func(a uint32, v uint8) { record('R', a, v) }
	s.Bus.WriteHook = func(a uint32, v uint8) { record('W', a, v) }
	return j
}
func (j *busJournal) finish() (string, uint64) {
	sum := hex.EncodeToString(j.hash.Sum(nil))
	n := j.events
	j.hash.Reset()
	j.events = 0
	return sum, n
}
