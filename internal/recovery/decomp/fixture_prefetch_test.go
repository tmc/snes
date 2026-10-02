package decomp

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hexSHA(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// writeFixture writes a gzip fixture with a run header, one cpu_insn per seq
// and an interleaved non-instruction event, returning its raw and decompressed
// digests.
func writeFixture(t *testing.T, path string, seqs []uint64) (string, string) {
	t.Helper()
	var plain bytes.Buffer
	plain.WriteString(`{"run":{"rom_sha256":"rom","start":"power_on"}}` + "\n")
	for _, s := range seqs {
		fmt.Fprintf(&plain, `{"kind":"bus","seq":%d}`+"\n", s)
		fmt.Fprintf(&plain, `{"kind":"cpu_insn","seq":%d,"insn":{"seq":%d,"length":%d,"status":"ok"}}`+"\n", s, s, s%4+1)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(plain.Bytes())
	zw.Close()
	if err := os.WriteFile(path, gz.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return hexSHA(gz.Bytes()), hexSHA(plain.Bytes())
}

// writeGzip writes plain gzip-compressed to path and returns the raw and
// decompressed digests.
func writeGzip(t *testing.T, path, plain string) (string, string) {
	t.Helper()
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte(plain))
	zw.Close()
	if err := os.WriteFile(path, gz.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return hexSHA(gz.Bytes()), hexSHA([]byte(plain))
}

func seqSet(seqs ...uint64) map[uint64]bool {
	m := make(map[uint64]bool)
	for _, s := range seqs {
		m[s] = true
	}
	return m
}

func TestReadVerifiedFixture(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.jsonl.gz")
	sha, dsha := writeFixture(t, good, []uint64{1, 2, 3, 4, 5})
	dup := filepath.Join(dir, "dup.jsonl.gz")
	dupSHA, dupDSHA := writeFixture(t, dup, []uint64{1, 2, 2, 3})
	trunc := filepath.Join(dir, "trunc.jsonl.gz")
	b, _ := os.ReadFile(good)
	os.WriteFile(trunc, b[:len(b)-12], 0o644)
	truncSHA := hexSHA(b[:len(b)-12])
	trailing := filepath.Join(dir, "trailing.jsonl.gz")
	os.WriteFile(trailing, append(append([]byte{}, b...), 0), 0o644)

	tests := []struct {
		name       string
		path       string
		sha, dsha  string
		seqs       map[uint64]bool
		wantSeqs   []uint64
		wantErrSub string
	}{
		{"retains only requested", good, sha, dsha, seqSet(2, 4, 9), []uint64{2, 4}, ""},
		{"raw digest mismatch", good, strings.Repeat("0", 64), dsha, seqSet(2), nil, "want pinned"},
		{"decompressed digest mismatch", good, sha, strings.Repeat("0", 64), seqSet(2), nil, "decompressed sha256"},
		{"duplicate seq", dup, dupSHA, dupDSHA, seqSet(2), nil, "duplicate cpu_insn seq 2"},
		{"truncated stream", trunc, truncSHA, dsha, seqSet(2), nil, "unexpected EOF"},
		{"trailing garbage", trailing, sha, dsha, seqSet(2), nil, "unexpected EOF"},
		{"missing file", filepath.Join(dir, "none.gz"), sha, dsha, seqSet(2), nil, "no such file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vf, err := readVerifiedFixture(tt.path, tt.sha, tt.dsha, tt.seqs)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErrSub)
				}
				if vf != nil {
					t.Fatal("returned a fixture with an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got []uint64
			for s := range vf.fd.recs {
				got = append(got, s)
			}
			if len(got) != len(tt.wantSeqs) {
				t.Fatalf("records for seqs %v, want %v", got, tt.wantSeqs)
			}
			for _, s := range tt.wantSeqs {
				if r, ok := vf.fd.recs[s]; !ok || r.Seq != s || r.Length != int(s%4+1) {
					t.Errorf("seq %d: record %+v ok=%v", s, r, ok)
				}
			}
			for s := range tt.seqs {
				if !vf.requested[s] {
					t.Errorf("seq %d not recorded as requested", s)
				}
			}
			if vf.fd.header.ROMSHA256 != "rom" || vf.fd.header.Start != "power_on" {
				t.Errorf("header = %+v", vf.fd.header)
			}
		})
	}
}

func TestPrefetchFixtures_PublishesOnlyVerified(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fx.jsonl.gz")
	sha, dsha := writeFixture(t, path, []uint64{10, 11, 12, 13, 14})
	const corpus = "test-prefetch"
	testCorpora[corpus] = CorpusTrustRoot{FixtureSHA256: sha, DecompressedSHA: dsha}
	defer delete(testCorpora, corpus)
	root := testCorpora[corpus]

	c := ReplayCase{
		SchemaVersion: "snes-routine-case-v1",
		CallSeq:       10, EntrySeq: 11, ExitSeq: 12, ReturnSeq: 13,
		Evidence: &CaseEvidence{Corpus: corpus, Fixture: &EvidenceFileRef{Path: path, SHA256: sha}},
	}
	v := newTestEvidenceVerifier(dir)
	if err := v.PrefetchFixtures([]ReplayCase{c}); err != nil {
		t.Fatal(err)
	}
	fd, ok := v.verifiedFixtureFor(path, root, seqSet(10, 11, 12, 13))
	if !ok || len(fd.recs) != 4 {
		t.Fatalf("prefetched fixture ok=%v recs=%d, want 4", ok, len(fd.recs))
	}
	if _, ok := v.verifiedFixtureFor(path, root, seqSet(14)); ok {
		t.Error("prefetched fixture served a seq it was not asked for")
	}
	other := root
	other.DecompressedSHA = strings.Repeat("0", 64)
	if _, ok := v.verifiedFixtureFor(path, other, seqSet(11)); ok {
		t.Error("prefetched fixture served under a different pin")
	}

	// A fixture that no longer matches its pin publishes nothing.
	writeFixture(t, path, []uint64{10, 11, 12, 13, 99})
	v2 := newTestEvidenceVerifier(dir)
	if err := v2.PrefetchFixtures([]ReplayCase{c}); err == nil {
		t.Fatal("prefetch of modified fixture succeeded")
	}
	if len(v2.verified) != 0 {
		t.Fatalf("prefetch error published %d fixtures", len(v2.verified))
	}
	if _, ok := v2.verifiedFixtureFor(path, root, seqSet(11)); ok {
		t.Error("verifiedFixtureFor served after failed prefetch")
	}
}

func TestReadVerifiedFixture_Malformed(t *testing.T) {
	const hdr = `{"kind":"run","run":{"rom_sha256":"rom","start":"power_on"}}` + "\n"
	insn := func(env, seq string) string {
		return `{"kind":"` + env + `","insn":{"seq":` + seq + `,"length":1}}` + "\n"
	}
	tests := []struct {
		name  string
		plain string
		want  string
	}{
		{"empty", "", "empty"},
		{"no header", insn("cpu_insn", "2"), "not a run header"},
		{"malformed header", "{\n" + insn("cpu_insn", "2"), "not a run header"},
		{"wanted record bad json", hdr + `{"kind":"cpu_insn","insn":{"seq":2,"length":"x"}}` + "\n", "seq 2"},
		{"wanted record without insn", hdr + `{"kind":"cpu_insn","seq":2}` + "\n", "not a cpu_insn event"},
		{"wanted seq in other event kind", hdr + `{"kind":"bus","note":"cpu_insn","seq":2}` + "\n", "not a cpu_insn event"},
		{"insn seq differs from envelope seq", hdr + `{"kind":"cpu_insn","seq":2,"insn":{"seq":3,"length":1}}` + "\n", "not a cpu_insn event"},
		{"seq overflow", hdr + insn("cpu_insn", "18446744073709551618"), "overflows"},
		{"seq overflow on unwanted line", hdr + insn("cpu_insn", "99999999999999999999") + insn("cpu_insn", "2"), "overflows"},
		{"negative seq", hdr + insn("cpu_insn", "-2"), "malformed seq"},
		{"seq with suffix", hdr + insn("cpu_insn", "2x"), "malformed seq"},
		{"fractional seq", hdr + insn("cpu_insn", "2.0"), "malformed seq"},
		{"string seq", hdr + insn("cpu_insn", `"2"`), "malformed seq"},
	}
	dir := t.TempDir()
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, fmt.Sprintf("f%d.jsonl.gz", i))
			sha, dsha := writeGzip(t, path, tt.plain)
			vf, err := readVerifiedFixture(path, sha, dsha, seqSet(2))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
			if vf != nil {
				t.Fatal("returned a fixture with an error")
			}
		})
	}
}

func TestLineSeq(t *testing.T) {
	tests := []struct {
		line    string
		want    uint64
		ok      bool
		wantErr bool
	}{
		{`{"kind":"bus"}`, 0, false, false},
		{`{"insn":{"seq":0,"x":1}}`, 0, true, false},
		{`{"insn":{"seq": 42}}`, 42, true, false},
		{`{"insn":{"seq":18446744073709551615}}`, math.MaxUint64, true, false},
		{`{"insn":{"seq":18446744073709551616}}`, 0, false, true},
		{`{"insn":{"seq":}}`, 0, false, true},
		{`{"insn":{"seq":7`, 0, false, true},
		{`{"insn":{"seq":1e3}}`, 0, false, true},
		{`{"insn":{"seq":null}}`, 0, false, true},
	}
	for _, tt := range tests {
		got, ok, err := lineSeq([]byte(tt.line))
		if got != tt.want || ok != tt.ok || (err != nil) != tt.wantErr {
			t.Errorf("lineSeq(%s) = %d, %v, %v; want %d, %v, err=%v", tt.line, got, ok, err, tt.want, tt.ok, tt.wantErr)
		}
	}
}

func TestCaseFixtureSeqs(t *testing.T) {
	const max = math.MaxUint64
	tests := []struct {
		name               string
		schema             string
		call, entry, exit  uint64
		ret                uint64
		wantLen            int
		wantFirst, wantEnd uint64
	}{
		{"routine", "snes-routine-case-v1", 9, 10, 12, 13, 5, 9, 12},
		{"unobserved continuation", "snes-routine-case-v1", 9, 10, 10, 0, 2, 9, 10},
		{"observed continuation", "snes-routine-case-v1", 9, 10, 10, 11, 3, 9, 10},
		{"actual zero entry", "snes-routine-case-v1", 0, 0, 0, 0, 1, 0, 0},
		{"block", "snes-replay-case-v2", 0, 10, 13, 0, 4, 10, 13},
		{"entry=exit=max", "snes-routine-case-v1", max - 1, max, max, 0, 2, max - 1, max},
		{"entry=0 exit=max", "snes-routine-case-v1", 0, 0, max, 0, 0, 0, 0},
		{"entry=1 exit=max", "snes-replay-case-v2", 0, 1, max, 0, 0, 0, 0},
		{"reversed", "snes-routine-case-v1", 0, 12, 10, 0, 0, 0, 0},
		{"span 5000", "snes-replay-case-v2", 0, 1, 5000, 0, 5000, 1, 5000},
		{"span 5001", "snes-replay-case-v2", 0, 1, 5001, 0, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ReplayCase{SchemaVersion: tt.schema, CallSeq: tt.call, EntrySeq: tt.entry, ExitSeq: tt.exit, ReturnSeq: tt.ret}
			got := caseFixtureSeqs(&c)
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tt.wantLen)
			}
			if len(got) > 0 && (got[0] != tt.wantFirst || got[len(got)-1] != tt.wantEnd) {
				t.Errorf("seqs = %d..%d, want %d..%d", got[0], got[len(got)-1], tt.wantFirst, tt.wantEnd)
			}
		})
	}
}

// TestPrefetchFixtures_ErrorPublishesNothing checks that one bad fixture in a
// batch keeps every fixture of the batch unpublished.
func TestPrefetchFixtures_ErrorPublishesNothing(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.jsonl.gz")
	gsha, gdsha := writeFixture(t, good, []uint64{10, 11, 12, 13})
	bad := filepath.Join(dir, "bad.jsonl.gz")
	bsha, bdsha := writeGzip(t, bad, `{"run":{"rom_sha256":"rom"}}`+"\n"+
		`{"kind":"cpu_insn","insn":{"seq":11,"length":"x"}}`+"\n")
	testCorpora["test-good"] = CorpusTrustRoot{FixtureSHA256: gsha, DecompressedSHA: gdsha}
	testCorpora["test-bad"] = CorpusTrustRoot{FixtureSHA256: bsha, DecompressedSHA: bdsha}
	defer delete(testCorpora, "test-good")
	defer delete(testCorpora, "test-bad")

	mk := func(corpus, path, sha string) ReplayCase {
		return ReplayCase{
			SchemaVersion: "snes-routine-case-v1",
			CallSeq:       10, EntrySeq: 11, ExitSeq: 12, ReturnSeq: 13,
			Evidence: &CaseEvidence{Corpus: corpus, Fixture: &EvidenceFileRef{Path: path, SHA256: sha}},
		}
	}
	v := newTestEvidenceVerifier(dir)
	err := v.PrefetchFixtures([]ReplayCase{mk("test-good", good, gsha), mk("test-bad", bad, bsha)})
	if err == nil || !strings.Contains(err.Error(), "seq 11") {
		t.Fatalf("err = %v, want malformed seq 11 record", err)
	}
	if len(v.verified) != 0 {
		t.Fatalf("failed batch published %d fixtures", len(v.verified))
	}
	if _, ok := v.verifiedFixtureFor(good, testCorpora["test-good"], seqSet(11)); ok {
		t.Error("good fixture of a failed batch was served")
	}
}
