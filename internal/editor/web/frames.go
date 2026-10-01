package web

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
)

// Frames is a pinned original-interpreter frame inventory, unrelated to edited C.
type Frames struct {
	ManifestSHA256 string  `json:"manifest_sha256"`
	ROMSHA256      string  `json:"rom_sha256"`
	EngineRevision string  `json:"engine_revision"`
	Mode           string  `json:"mode"`
	Records        []Frame `json:"records"`
	images         map[int][]byte
}

// Frame retains host/PPU identity and image pins without claiming sprite ownership.
type Frame struct {
	Index     int    `json:"index"`
	Number    int    `json:"number"`
	HostFrame int    `json:"trace_frame"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	PNGSHA256 string `json:"png_sha256"`
	ContentID string `json:"content_id"`
}

// LoadFrames reads a bounded manifest and snapshots pinned PNGs for safe serving.
func LoadFrames(path, pin string) (*Frames, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 8<<20+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 8<<20 || len(pin) != 64 || fmt.Sprintf("%x", sha256.Sum256(b)) != pin {
		return nil, fmt.Errorf("frame manifest differs from pin")
	}
	out := &Frames{ManifestSHA256: pin, Mode: "original_interpreter", images: map[int][]byte{}}
	scan := bufio.NewScanner(bytes.NewReader(b))
	scan.Buffer(make([]byte, 4096), 1<<20)
	line := 0
	for scan.Scan() {
		line++
		var rec struct {
			Frame
			Kind      string `json:"kind"`
			Schema    int    `json:"schema"`
			Stored    bool   `json:"stored"`
			PNG       string `json:"png"`
			Format    string `json:"format"`
			Interlace bool   `json:"interlace"`
			Run       struct {
				ROM      string `json:"rom_sha256"`
				Revision string `json:"engine_revision"`
				Dirty    bool   `json:"engine_dirty"`
			} `json:"run"`
		}
		if err := json.Unmarshal(scan.Bytes(), &rec); err != nil {
			return nil, err
		}
		if line == 1 {
			if rec.Kind != "frame_run" || rec.Schema != 1 || rec.Run.Dirty || len(rec.Run.ROM) != 64 || rec.Run.Revision == "" {
				return nil, fmt.Errorf("unsupported frame producer")
			}
			out.ROMSHA256 = rec.Run.ROM
			out.EngineRevision = rec.Run.Revision
			continue
		}
		if rec.Kind != "frame" {
			return nil, fmt.Errorf("invalid frame record")
		}
		if !rec.Stored {
			continue
		}
		if rec.Index < 0 || rec.Number < 0 || rec.HostFrame < 0 || rec.PNG != fmt.Sprintf("png/%06d.png", rec.Index) || rec.Format != "bgr555le" || rec.Interlace || rec.Width != 256 || (rec.Height != 224 && rec.Height != 239) || len(rec.ContentID) != 64 {
			return nil, fmt.Errorf("unsupported frame profile or path")
		}
		if _, ok := out.images[rec.Index]; ok {
			return nil, fmt.Errorf("duplicate frame index")
		}
		img, err := os.ReadFile(filepath.Join(filepath.Dir(path), rec.PNG))
		if err != nil {
			return nil, err
		}
		if len(img) > 2<<20 || fmt.Sprintf("%x", sha256.Sum256(img)) != rec.PNGSHA256 {
			return nil, fmt.Errorf("frame PNG differs from pin")
		}
		config, err := png.DecodeConfig(bytes.NewReader(img))
		if err != nil || config.Width != rec.Width || config.Height != rec.Height {
			return nil, fmt.Errorf("frame image dimensions differ")
		}
		out.images[rec.Index] = append([]byte(nil), img...)
		out.Records = append(out.Records, rec.Frame)
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	if len(out.Records) == 0 {
		return nil, fmt.Errorf("no stored frames")
	}
	return out, nil
}
