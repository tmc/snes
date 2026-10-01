package decomp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFixtureDigestReadsDecompressedBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.jsonl.gz")
	raw, decoded := writeFixture(t, path, []uint64{1})
	const corpus = "test-decoded-digest"
	TrustedCorpora[corpus] = CorpusTrustRoot{FixtureSHA256: raw, DecompressedSHA: raw}
	defer delete(TrustedCorpora, corpus)
	v := NewEvidenceVerifier("")
	got, err := v.sha(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if got != decoded {
		t.Fatalf("decompressed digest = %s, want measured %s", got, decoded)
	}
}

func TestFixtureDigestWrongPinLazyAndPrefetch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.jsonl.gz")
	raw, _ := writeFixture(t, path, []uint64{1})
	const corpus = "test-wrong-decoded-pin"
	root := CorpusTrustRoot{FixtureSHA256: raw, DecompressedSHA: raw}
	TrustedCorpora[corpus] = root
	defer delete(TrustedCorpora, corpus)
	ref := &EvidenceFileRef{Path: path, SHA256: raw}
	fx := &EvidenceFileRef{Path: path, SHA256: raw, Receipt: ref, Summary: ref}
	c := ReplayCase{EntrySeq: 1, ExitSeq: 1, RunID: raw, StreamSHA256: raw,
		Evidence: &CaseEvidence{Corpus: corpus, Fixture: fx, Capture: fx, History: fx}}
	for _, prefetch := range []bool{false, true} {
		v := NewEvidenceVerifier("")
		var err error
		if prefetch {
			err = v.PrefetchFixtures([]ReplayCase{c})
		} else {
			_, err = v.checkFiles(&c, root)
		}
		if err == nil || !strings.Contains(err.Error(), "decompressed") {
			t.Errorf("prefetch=%v: error = %v, want decompressed digest rejection", prefetch, err)
		}
		if len(v.verified) != 0 || len(v.fixtures) != 0 || len(v.admittedCases) != 0 {
			t.Errorf("prefetch=%v: failure published fixture or admission", prefetch)
		}
	}
}

func TestFixtureDigestEmbeddedMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.jsonl.gz")
	raw, decoded := writeFixture(t, path, []uint64{1})
	ref := &EvidenceFileRef{Path: path, SHA256: raw}
	fx := &EvidenceFileRef{Path: path, SHA256: raw, DecompressedSHA256: raw, Receipt: ref, Summary: ref}
	c := ReplayCase{RunID: raw, StreamSHA256: raw, Evidence: &CaseEvidence{Fixture: fx, Capture: fx, History: fx}}
	v := NewEvidenceVerifier("")
	_, err := v.checkFiles(&c, CorpusTrustRoot{FixtureSHA256: raw, DecompressedSHA: decoded})
	if err == nil || !strings.Contains(err.Error(), "embedded decompressed") {
		t.Fatalf("error = %v, want embedded decompressed mismatch", err)
	}
	if c.DecompressedStreamSHA256 != "" || len(v.fixtures) != 0 || len(v.admittedCases) != 0 {
		t.Fatal("mismatch published case metadata, fixture or admission")
	}
}

func TestFixtureDigestTamperNewVerifier(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.jsonl.gz")
	_, first := writeFixture(t, path, []uint64{1})
	got, err := NewEvidenceVerifier("").sha(path, true)
	if err != nil || got != first {
		t.Fatalf("first digest=%s err=%v", got, err)
	}
	_, second := writeFixture(t, path, []uint64{2})
	got, err = NewEvidenceVerifier("").sha(path, true)
	if err != nil || got != second || got == first {
		t.Fatalf("modified artifact digest=%s err=%v want=%s", got, err, second)
	}
}

func TestFixtureDigestTamperSameVerifier(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.jsonl.gz")
	firstRaw, first := writeFixture(t, path, []uint64{1})
	v := NewEvidenceVerifier("")
	for _, decoded := range []bool{false, true} {
		if _, err := v.sha(path, decoded); err != nil {
			t.Fatal(err)
		}
	}
	secondRaw, second := writeFixture(t, path, []uint64{2})
	for _, tt := range []struct {
		decoded   bool
		want, old string
	}{{false, secondRaw, firstRaw}, {true, second, first}} {
		got, err := v.sha(path, tt.decoded)
		if err != nil || got != tt.want || got == tt.old {
			t.Errorf("decoded=%v modified artifact digest=%s err=%v want=%s", tt.decoded, got, err, tt.want)
		}
	}
}
