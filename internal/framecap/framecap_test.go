package framecap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/trace"
)

// newFrame returns a w×h frame numbered n whose pixels are fill+i.
// Frame n starts at cycle 1000*(n+1) and enters vblank 900 cycles later.
func newFrame(n, w, h int, fill uint16) *snes.Frame {
	f := &snes.Frame{
		Number:     n,
		Width:      w,
		Height:     h,
		Format:     snes.PixelFormatBGR555,
		Pixels:     make([]uint16, w*h),
		HiresLines: make([]bool, h),
		Start:      uint64(1000 * (n + 1)),
		VBlank:     uint64(1000*(n+1) + 900),
	}
	for i := range f.Pixels {
		f.Pixels[i] = (fill + uint16(i)) & 0x7FFF
	}
	return f
}

// feed drives w as snes.System.CaptureFrames would.
func feed(w *Writer, frames ...*snes.Frame) {
	for _, f := range frames {
		if w.Keep(snes.FrameBoundary{Number: f.Number, Start: f.Start, VBlank: f.VBlank}) {
			w.Frame(f)
		}
	}
}

func create(t *testing.T, opts Options) *Writer {
	t.Helper()
	if opts.Dir == "" {
		opts.Dir = t.TempDir()
	}
	w, err := Create(opts)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func closeOpen(t *testing.T, w *Writer) (Receipt, *Capture) {
	t.Helper()
	rc, err := w.Close(trace.OutcomeComplete)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	c, err := Open(w.opts.Dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return rc, c
}

func TestRoundTrip(t *testing.T) {
	hires := newFrame(2, 512, 224, 7)
	for y := 10; y < 20; y++ {
		hires.HiresLines[y] = true
	}
	frames := []*snes.Frame{
		newFrame(0, 256, 224, 1),
		newFrame(1, 256, 224, 2),
		hires,
		newFrame(3, 256, 240, 3),
	}
	w := create(t, Options{})
	feed(w, frames...)
	if err := w.Err(); err != nil {
		t.Fatal(err)
	}
	rc, c := closeOpen(t, w)

	if c.Receipt == nil || !reflect.DeepEqual(*c.Receipt, rc) {
		t.Fatalf("receipt on disk = %+v, want %+v", c.Receipt, rc)
	}
	if rc.Outcome != trace.OutcomeComplete || rc.Frames != 4 || rc.Stored != 4 || rc.Unique != 4 {
		t.Errorf("receipt = %+v", rc)
	}
	manifest, err := os.ReadFile(filepath.Join(c.Dir, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(manifest); hex.EncodeToString(sum[:]) != rc.ManifestSHA256 {
		t.Errorf("ManifestSHA256 = %s, want %x", rc.ManifestSHA256, sum)
	}
	if c.Header.Kind != "frame_run" || c.Header.Storage != StorageGzipNative || c.Header.Selection.Every != 1 {
		t.Errorf("header = %+v", c.Header)
	}

	if len(c.Records) != len(frames) {
		t.Fatalf("got %d records, want %d", len(c.Records), len(frames))
	}
	for i, r := range c.Records {
		f := frames[i]
		if r.Index != i || r.Number != f.Number || r.Start != f.Start || r.VBlank != f.VBlank {
			t.Errorf("record %d = %+v", i, r)
		}
		if i+1 < len(c.Records) {
			if r.End == nil || *r.End != c.Records[i+1].Start {
				t.Errorf("record %d End = %v, want %d", i, r.End, c.Records[i+1].Start)
			}
		} else if r.End != nil {
			t.Errorf("last record End = %d, want nil", *r.End)
		}
		if !r.Stored || r.Width != f.Width || r.Height != f.Height || r.Format != snes.PixelFormatBGR555 {
			t.Errorf("record %d = %+v", i, r)
		}
		px, err := c.Pixels(&c.Records[i])
		if err != nil {
			t.Fatalf("Pixels(%d): %v", i, err)
		}
		if !reflect.DeepEqual(px, f.Pixels) {
			t.Errorf("Pixels(%d) differ", i)
		}
	}
	if want := []Span{{10, 19}}; !reflect.DeepEqual(c.Records[2].HiresLines, want) {
		t.Errorf("HiresLines = %v, want %v", c.Records[2].HiresLines, want)
	}
	if c.Records[0].HiresLines != nil {
		t.Errorf("lores HiresLines = %v, want nil", c.Records[0].HiresLines)
	}

	for _, tt := range []struct {
		cycle uint64
		want  int // record index, or -1
	}{
		{999, -1},
		{1000, 0},
		{1999, 0},
		{2000, 1},
		{2999, 1},
		{3000, 2},
		{4000, 3},
		{1 << 40, 3},
	} {
		r := c.At(tt.cycle)
		got := -1
		if r != nil {
			got = r.Index
		}
		if got != tt.want {
			t.Errorf("At(%d) = %d, want %d", tt.cycle, got, tt.want)
		}
	}
}

func TestDedup(t *testing.T) {
	w := create(t, Options{})
	feed(w, newFrame(0, 256, 224, 5), newFrame(1, 256, 224, 5))
	rc, c := closeOpen(t, w)
	if rc.Frames != 2 || rc.Stored != 2 || rc.Unique != 1 {
		t.Errorf("receipt = %+v", rc)
	}
	a, b := c.Records[0], c.Records[1]
	if !a.Stored || !b.Stored || a.ContentID != b.ContentID || a.Blob != b.Blob {
		t.Errorf("records = %+v, %+v", a, b)
	}
	if a.DupOf != nil || b.DupOf == nil || *b.DupOf != 0 {
		t.Errorf("DupOf = %v, %v; want nil, 0", a.DupOf, b.DupOf)
	}
	blobs, _ := os.ReadDir(filepath.Join(c.Dir, blobDir))
	if len(blobs) != 1 {
		t.Errorf("%d blobs, want 1", len(blobs))
	}
	if _, err := c.Pixels(&c.Records[1]); err != nil {
		t.Error(err)
	}

	// Same bytes, different shape.
	w = create(t, Options{})
	feed(w, newFrame(0, 256, 2, 0), newFrame(1, 512, 1, 0))
	rc, c = closeOpen(t, w)
	if rc.Unique != 2 || c.Records[0].ContentID == c.Records[1].ContentID || c.Records[1].DupOf != nil {
		t.Errorf("256x2 and 512x1 deduplicated: %+v", c.Records)
	}
}

func frames(n int) []*snes.Frame {
	var fs []*snes.Frame
	for i := range n {
		fs = append(fs, newFrame(i, 256, 224, uint16(100*i)))
	}
	return fs
}

func TestSelectionAndLimits(t *testing.T) {
	const (
		sel = SkipSelection
		lim = SkipLimit
	)
	tests := []struct {
		name    string
		opts    Options
		skip    []string // per frame; "" means stored
		outcome trace.Outcome
		trunc   string
	}{
		{"all", Options{}, []string{"", "", "", "", ""}, trace.OutcomeComplete, ""},
		{"from", Options{Selection: Selection{From: 2}}, []string{sel, sel, "", "", ""}, trace.OutcomeComplete, ""},
		{"every", Options{Selection: Selection{From: 1, Every: 2}}, []string{sel, "", sel, "", sel}, trace.OutcomeComplete, ""},
		{"frame_limit", Options{Limits: Limits{Frames: 2}}, []string{"", "", lim, lim, lim}, trace.OutcomeLimit, "frame_limit"},
		{"frame_limit_selection", Options{Selection: Selection{Every: 2}, Limits: Limits{Frames: 1}}, []string{"", sel, lim, sel, lim}, trace.OutcomeLimit, "frame_limit"},
		{"byte_limit", Options{Limits: Limits{Bytes: 1}}, []string{lim, lim, lim, lim, lim}, trace.OutcomeLimit, "byte_limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := create(t, tt.opts)
			feed(w, frames(len(tt.skip))...)
			rc, c := closeOpen(t, w)
			if len(c.Records) != len(tt.skip) {
				t.Fatalf("got %d records, want %d", len(c.Records), len(tt.skip))
			}
			stored := 0
			for i, r := range c.Records {
				if r.Skip != tt.skip[i] || r.Stored != (tt.skip[i] == "") {
					t.Errorf("record %d: Skip %q Stored %v, want Skip %q", i, r.Skip, r.Stored, tt.skip[i])
				}
				if r.Stored {
					stored++
				} else if r.ContentID != "" || r.Blob != "" {
					t.Errorf("record %d: unstored with content %+v", i, r)
				}
			}
			if rc.Outcome != tt.outcome || rc.TruncationReason != tt.trunc || rc.Frames != len(tt.skip) || rc.Stored != stored {
				t.Errorf("receipt = %+v, want outcome %s trunc %q stored %d", rc, tt.outcome, tt.trunc, stored)
			}
			if w.Truncated() != tt.trunc {
				t.Errorf("Truncated = %q, want %q", w.Truncated(), tt.trunc)
			}
		})
	}
}

func TestLimitKeepsOtherOutcome(t *testing.T) {
	w := create(t, Options{Limits: Limits{Frames: 1}})
	feed(w, frames(3)...)
	rc, err := w.Close(trace.OutcomeTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if rc.Outcome != trace.OutcomeTimeout || rc.TruncationReason != "frame_limit" {
		t.Errorf("receipt = %+v", rc)
	}
}

func TestWriteError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root")
	}
	dir := t.TempDir()
	w := create(t, Options{Dir: dir})
	blobs := filepath.Join(dir, blobDir)
	if err := os.Chmod(blobs, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(blobs, 0o755) })

	feed(w, frames(3)...)
	if w.Err() == nil {
		t.Fatal("Err = nil after unwritable blob dir")
	}
	rc, err := w.Close(trace.OutcomeComplete)
	if err == nil {
		t.Error("Close error = nil")
	}
	if rc.Outcome != trace.OutcomeSinkError || rc.Error == "" {
		t.Errorf("receipt = %+v", rc)
	}
	c, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Receipt == nil || c.Receipt.Outcome != trace.OutcomeSinkError || c.Receipt.Error != rc.Error {
		t.Errorf("receipt on disk = %+v", c.Receipt)
	}
	// The timeline survives the failed blob writes.
	if len(c.Records) != 3 {
		t.Fatalf("%d records after write error, want 3", len(c.Records))
	}
	for _, r := range c.Records {
		if r.Stored {
			t.Errorf("record %d stored despite write error", r.Index)
		}
		if r.Index > 0 && r.Skip != SkipError {
			t.Errorf("record %d skip %q, want %q", r.Index, r.Skip, SkipError)
		}
	}
}

func TestTamper(t *testing.T) {
	w := create(t, Options{})
	feed(w, newFrame(0, 16, 4, 1))
	_, c := closeOpen(t, w)
	r := &c.Records[0]
	if _, err := c.Pixels(r); err != nil {
		t.Fatal(err)
	}

	// Well-formed gzip of the right length, wrong content.
	raw := nativeBytes(newFrame(0, 16, 4, 2).Pixels)
	if err := os.WriteFile(filepath.Join(c.Dir, r.Blob), compress(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Pixels(r); err == nil || !strings.Contains(err.Error(), "content id") {
		t.Errorf("Pixels after tamper: err = %v, want content id mismatch", err)
	}

	if err := os.WriteFile(filepath.Join(c.Dir, r.Blob), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Pixels(r); err == nil {
		t.Error("Pixels of corrupt blob: err = nil")
	}
}

func TestSeqClock(t *testing.T) {
	var nilClock *SeqClock
	if _, ok := nilClock.First(0); ok {
		t.Error("nil First ok")
	}
	c := NewSeqClock()
	if _, ok := c.First(0); ok {
		t.Error("empty First ok")
	}
	if c.Next() != 1 {
		t.Errorf("empty Next = %d, want 1", c.Next())
	}
	for i, cyc := range []uint64{10, 20, 20, 30, 40} {
		c.Observe(uint64(i+1), cyc)
	}
	tests := []struct {
		cycle uint64
		seq   uint64
		ok    bool
	}{
		{0, 1, true},
		{10, 1, true},
		{11, 2, true},
		{20, 2, true}, // first of equal cycles
		{21, 4, true},
		{30, 4, true},
		{40, 5, true},
		{41, 0, false},
	}
	for _, tt := range tests {
		seq, ok := c.First(tt.cycle)
		if seq != tt.seq || ok != tt.ok {
			t.Errorf("First(%d) = %d, %v; want %d, %v", tt.cycle, seq, ok, tt.seq, tt.ok)
		}
	}
	if c.Next() != 6 {
		t.Errorf("Next = %d, want 6", c.Next())
	}
}

func TestSeqClockForgets(t *testing.T) {
	c := NewSeqClock()
	const n = seqClockSize + 100
	for s := uint64(1); s <= n; s++ {
		c.Observe(s, 10*s)
	}
	oldest := uint64(n - seqClockSize + 1) // oldest remembered seq
	tests := []struct {
		cycle uint64
		seq   uint64
		ok    bool
	}{
		{0, 0, false},           // seq 1 qualifies but is forgotten
		{10 * 50, 0, false},     // seq 50 forgotten
		{10 * oldest, 0, false}, // an earlier forgotten seq might tie
		{10*oldest + 1, oldest + 1, true},
		{10 * (oldest + 5), oldest + 5, true},
		{10 * n, n, true},
		{10*n + 1, 0, false},
	}
	for _, tt := range tests {
		seq, ok := c.First(tt.cycle)
		if seq != tt.seq || ok != tt.ok {
			t.Errorf("First(%d) = %d, %v; want %d, %v", tt.cycle, seq, ok, tt.seq, tt.ok)
		}
	}
}

func TestWriterSeq(t *testing.T) {
	// Observation seq s enters at cycle 100*s. Frame n starts at
	// 1000*(n+1), so seq bounds are cycle/100 when observed.
	c := NewSeqClock()
	var seq uint64
	observe := func(upto uint64) {
		for 100*(seq+1) <= upto {
			seq++
			c.Observe(seq, 100*seq)
		}
	}
	fs := frames(3)
	fs[2].VBlank = 3950 // beyond the last observation
	w := create(t, Options{Seq: c})
	for _, f := range fs {
		observe(f.VBlank)
		feed(w, f)
	}
	_, capt := closeOpen(t, w)

	p := func(v uint64) *uint64 { return &v }
	want := []struct{ start, vblank, end *uint64 }{
		{p(10), p(19), p(20)},
		{p(20), p(29), p(30)},
		{p(30), p(40), nil}, // vblank unreached: Next
	}
	for i, r := range capt.Records {
		if !reflect.DeepEqual(r.SeqStart, want[i].start) || !reflect.DeepEqual(r.SeqVBlank, want[i].vblank) || !reflect.DeepEqual(r.SeqEnd, want[i].end) {
			t.Errorf("record %d seq = %v %v %v, want %v %v %v", i,
				deref(r.SeqStart), deref(r.SeqVBlank), deref(r.SeqEnd),
				deref(want[i].start), deref(want[i].vblank), deref(want[i].end))
		}
	}
}

func deref(p *uint64) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestEncodePNG(t *testing.T) {
	px := []uint16{
		0x0000, 0x7FFF,
		0x001F, 0x03E0,
		0x7C00, 1 | 2<<5 | 3<<10,
	}
	var buf bytes.Buffer
	if err := EncodePNG(&buf, 2, 3, px); err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 2 || b.Dy() != 3 {
		t.Fatalf("bounds = %v", b)
	}
	exp := func(c uint16) uint32 { c &= 0x1F; return uint32(c<<3 | c>>2) }
	for i, v := range px {
		r, g, b, a := img.At(i%2, i/2).RGBA()
		got := [4]uint32{r >> 8, g >> 8, b >> 8, a >> 8}
		want := [4]uint32{exp(v), exp(v >> 5), exp(v >> 10), 0xFF}
		if got != want {
			t.Errorf("pixel %d (%#04x) = %v, want %v", i, v, got, want)
		}
	}
	if err := EncodePNG(&buf, 2, 2, px); err == nil {
		t.Error("EncodePNG with wrong length: err = nil")
	}
}

func TestCreateRemovesStaleReceipt(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, ReceiptName)
	if err := os.WriteFile(stale, []byte(`{"outcome":"complete"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	w := create(t, Options{Dir: dir})
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale receipt still present: %v", err)
	}
	// The header is buffered until Close, so the manifest has none yet.
	if _, err := Open(dir); err == nil {
		t.Errorf("Open of a manifest without a header succeeded")
	}
	w.Close(trace.OutcomeComplete)
	c, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Receipt == nil || c.Receipt.Outcome != trace.OutcomeComplete {
		t.Errorf("receipt = %+v", c.Receipt)
	}
}

func TestPNG(t *testing.T) {
	w := create(t, Options{PNG: func(n int) bool { return n == 1 }})
	feed(w, frames(3)...)
	_, c := closeOpen(t, w)
	for i, r := range c.Records {
		if i != 1 {
			if r.PNG != "" || r.PNGSHA256 != "" {
				t.Errorf("record %d has png %q", i, r.PNG)
			}
			continue
		}
		if r.PNG != "png/000001.png" {
			t.Errorf("PNG = %q, want png/000001.png", r.PNG)
		}
		data, err := os.ReadFile(filepath.Join(c.Dir, r.PNG))
		if err != nil {
			t.Fatal(err)
		}
		if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != r.PNGSHA256 {
			t.Errorf("PNGSHA256 = %s, want %x", r.PNGSHA256, sum)
		}
		if _, err := png.Decode(bytes.NewReader(data)); err != nil {
			t.Error(err)
		}
	}
}
