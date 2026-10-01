package decomp

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

// maxPrefetchSeqs bounds the number of cpu_insn records one prefetch retains.
const maxPrefetchSeqs = 1 << 20

// verifiedFixture holds the cpu_insn records for a fixed set of requested
// seqs, read in one pass from fixture bytes whose compressed and decompressed
// SHA-256 were computed over exactly the bytes parsed and matched the pinned
// trust root. A requested seq that is absent from fd.recs does not occur in
// the fixture; Admit rejects any case that needs it. It is immutable once
// published.
type verifiedFixture struct {
	sha256       string
	decompSHA256 string
	requested    map[uint64]bool
	fd           *fixtureData
}

type fixtureSnapshotKey struct {
	path string
	root CorpusTrustRoot
}

func verifiedFixtureKey(absPath string, root CorpusTrustRoot) fixtureSnapshotKey {
	return fixtureSnapshotKey{absPath, root}
}

// PrefetchFixtures reads each pinned fixture referenced by cases once and
// retains only the cpu_insn records that admission of those cases needs.
// Records are published only if the bytes read hash to the corpus trust root;
// on any error nothing is published. Admit uses a prefetched fixture when it
// covers every seq of the case and fully verifies the fixture otherwise.
func (v *EvidenceVerifier) PrefetchFixtures(cases []ReplayCase) error {
	if v == nil || v.policy == nil {
		return fmt.Errorf("uninitialized evidence verifier")
	}
	type group struct {
		absPath string
		root    CorpusTrustRoot
		seqs    map[uint64]bool
	}
	groups := make(map[fixtureSnapshotKey]*group)
	var order []fixtureSnapshotKey
	for i := range cases {
		c := &cases[i]
		if c.Evidence == nil || c.Evidence.Fixture == nil {
			continue
		}
		root, ok := v.policy.roots[c.Evidence.Corpus]
		if !ok || c.Evidence.Fixture.SHA256 != root.FixtureSHA256 {
			continue
		}
		seqs := caseFixtureSeqs(c)
		if len(seqs) == 0 {
			continue
		}
		absPath := v.resolvePath(c.Evidence.Fixture.Path)
		k := verifiedFixtureKey(absPath, root)
		g := groups[k]
		if g == nil {
			g = &group{absPath: absPath, root: root, seqs: make(map[uint64]bool)}
			groups[k] = g
			order = append(order, k)
		}
		for _, s := range seqs {
			g.seqs[s] = true
		}
		if len(g.seqs) > maxPrefetchSeqs {
			return fmt.Errorf("prefetch %s: more than %d seqs", c.Evidence.Fixture.Path, maxPrefetchSeqs)
		}
	}
	read := make(map[fixtureSnapshotKey]*verifiedFixture, len(order))
	for _, k := range order {
		g := groups[k]
		vf, err := readVerifiedFixture(g.absPath, g.root.FixtureSHA256, g.root.DecompressedSHA, g.seqs)
		if err != nil {
			return err
		}
		read[k] = vf
	}
	v.mu.Lock()
	for k, vf := range read {
		v.verified[k] = vf
	}
	v.mu.Unlock()
	return nil
}

// caseFixtureSeqs returns the fixture seqs Admit may request for c: the
// entry..exit interval plus, for routine cases, the call and return seqs.
// Bounds that are reversed or span more than maxRoutineLen seqs yield nil;
// Admit rejects those cases before reading the fixture.
func caseFixtureSeqs(c *ReplayCase) []uint64 {
	const maxRoutineLen = 5000
	if c.ExitSeq < c.EntrySeq || c.ExitSeq-c.EntrySeq >= maxRoutineLen {
		return nil
	}
	n := c.ExitSeq - c.EntrySeq + 1 // at most maxRoutineLen, no overflow
	seqs := make([]uint64, 0, n+2)
	if c.SchemaVersion == "snes-routine-case-v1" {
		seqs = append(seqs, c.CallSeq, c.ReturnSeq)
	}
	for i := uint64(0); i < n; i++ {
		seqs = append(seqs, c.EntrySeq+i)
	}
	return seqs
}

// verifiedFixtureSnapshot selects a measured immutable snapshot under the full
// trust root when it covers every requested sequence. Source freshness is checked
// by checkFiles before admission consumes the snapshot.
func (v *EvidenceVerifier) verifiedFixtureSnapshot(relPath string, root CorpusTrustRoot, want map[uint64]bool) (*verifiedFixture, bool) {
	k := verifiedFixtureKey(v.resolvePath(relPath), root)
	v.mu.Lock()
	vf := v.verified[k]
	v.mu.Unlock()
	if vf == nil || vf.sha256 != root.FixtureSHA256 || vf.decompSHA256 != root.DecompressedSHA {
		return nil, false
	}
	for s := range want {
		if !vf.requested[s] {
			return nil, false
		}
	}
	return vf, true
}

// verifiedFixtureFor returns a private copy of the requested records. Callers
// cannot mutate the measured snapshot, including instruction fetch slices.
func (v *EvidenceVerifier) verifiedFixtureFor(relPath string, root CorpusTrustRoot, want map[uint64]bool) (*fixtureData, bool) {
	vf, ok := v.verifiedFixtureSnapshot(relPath, root, want)
	if !ok {
		return nil, false
	}
	fd := &fixtureData{header: vf.fd.header, recs: make(map[uint64]captureCPUInsn, len(want))}
	if p := fd.header.ReplayInputSHA256; p != nil {
		value := *p
		fd.header.ReplayInputSHA256 = &value
	}
	for seq := range want {
		if in, ok := vf.fd.recs[seq]; ok {
			in.Fetches = append([]captureFetch(nil), in.Fetches...)
			fd.recs[seq] = in
		}
	}
	return fd, true
}

// admissionFixture uses the same measured records as the decoded digest. When
// requested coverage is absent, it verifies the complete fixture while parsing.
func (v *EvidenceVerifier) admissionFixture(relPath string, root CorpusTrustRoot, want map[uint64]bool) (*fixtureData, error) {
	fd, ok := v.verifiedFixtureFor(relPath, root, want)
	if !ok {
		vf, err := readVerifiedFixture(v.resolvePath(relPath), root.FixtureSHA256, root.DecompressedSHA, want)
		if err != nil {
			return nil, err
		}
		fd = vf.fd
	}
	for seq := range want {
		if _, ok := fd.recs[seq]; !ok {
			return nil, fmt.Errorf("fixture lacks requested cpu_insn seq %d", seq)
		}
	}
	return fd, nil
}

// readVerifiedFixture reads the whole fixture at absPath, hashing the raw and
// decompressed bytes as they are parsed, and keeps the run header and the
// cpu_insn records for seqs. It returns an error unless both digests match.
//
// The first line must be the run header. Every later line that mentions
// cpu_insn and has a "seq" key must carry a well-formed decimal seq; a line
// whose seq is requested must decode as a cpu_insn event whose insn.seq is
// that seq and must not repeat. Any violation is an error.
func readVerifiedFixture(absPath, wantSHA, wantDecompSHA string, seqs map[uint64]bool) (*verifiedFixture, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	hraw := sha256.New()
	raw := io.TeeReader(f, hraw)
	hdec := hraw
	var r io.Reader = raw
	if strings.HasSuffix(absPath, ".gz") {
		gz, err := gzip.NewReader(raw)
		if err != nil {
			return nil, fmt.Errorf("fixture %s: %w", absPath, err)
		}
		defer gz.Close()
		hdec = sha256.New()
		r = io.TeeReader(gz, hdec)
	}

	fd := &fixtureData{recs: make(map[uint64]captureCPUInsn, len(seqs))}
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, 8*1024*1024)
	first := true
	for sc.Scan() {
		line := sc.Bytes()
		if first {
			first = false
			var runEvt struct {
				Run *captureRunHeader `json:"run"`
			}
			if err := json.Unmarshal(line, &runEvt); err != nil || runEvt.Run == nil {
				return nil, fmt.Errorf("fixture %s: first line is not a run header", absPath)
			}
			fd.header = *runEvt.Run
			continue
		}
		if !bytes.Contains(line, []byte(`"cpu_insn"`)) {
			continue
		}
		s, ok, err := lineSeq(line)
		if err != nil {
			return nil, fmt.Errorf("fixture %s: %w", absPath, err)
		}
		if !ok || !seqs[s] {
			continue
		}
		var insnEvt struct {
			Kind string          `json:"kind"`
			Insn *captureCPUInsn `json:"insn"`
		}
		if err := json.Unmarshal(line, &insnEvt); err != nil {
			return nil, fmt.Errorf("fixture %s: seq %d: %w", absPath, s, err)
		}
		if insnEvt.Kind != "cpu_insn" || insnEvt.Insn == nil || insnEvt.Insn.Seq != s {
			return nil, fmt.Errorf("fixture %s: seq %d: line is not a cpu_insn event for that seq", absPath, s)
		}
		if _, dup := fd.recs[s]; dup {
			return nil, fmt.Errorf("fixture %s: duplicate cpu_insn seq %d", absPath, s)
		}
		fd.recs[s] = *insnEvt.Insn
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("fixture %s: %w", absPath, err)
	}
	if first {
		return nil, fmt.Errorf("fixture %s: empty", absPath)
	}
	// Hash any bytes the decompressor did not consume.
	if _, err := io.Copy(io.Discard, raw); err != nil {
		return nil, fmt.Errorf("fixture %s: %w", absPath, err)
	}
	vf := &verifiedFixture{
		sha256:       hex.EncodeToString(hraw.Sum(nil)),
		decompSHA256: hex.EncodeToString(hdec.Sum(nil)),
		requested:    make(map[uint64]bool, len(seqs)),
		fd:           fd,
	}
	for seq, wanted := range seqs {
		vf.requested[seq] = wanted
	}
	if vf.sha256 != wantSHA {
		return nil, fmt.Errorf("fixture %s: sha256 %s, want pinned %s", absPath, vf.sha256, wantSHA)
	}
	if vf.decompSHA256 != wantDecompSHA {
		return nil, fmt.Errorf("fixture %s: decompressed sha256 %s, want pinned %s", absPath, vf.decompSHA256, wantDecompSHA)
	}
	return vf, nil
}

// lineSeq extracts the decimal integer following the first "seq": in line.
// It reports ok=false if line has no "seq" key, and an error if the value is
// not a decimal uint64 followed by a JSON delimiter.
func lineSeq(line []byte) (seq uint64, ok bool, err error) {
	idx := bytes.Index(line, []byte(`"seq":`))
	if idx == -1 {
		return 0, false, nil
	}
	p := idx + 6
	for p < len(line) && (line[p] == ' ' || line[p] == '\t') {
		p++
	}
	start := p
	for p < len(line) && line[p] >= '0' && line[p] <= '9' {
		d := uint64(line[p] - '0')
		if seq > (math.MaxUint64-d)/10 {
			return 0, false, fmt.Errorf("seq overflows uint64")
		}
		seq = seq*10 + d
		p++
	}
	if p == start || p == len(line) || !bytes.ContainsRune([]byte(",} \t"), rune(line[p])) {
		return 0, false, fmt.Errorf("malformed seq %q", line[idx:min(p+1, len(line))])
	}
	return seq, true, nil
}
