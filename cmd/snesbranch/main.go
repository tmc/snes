// snesbranch repeats a pinned complete-machine checkpoint and writes frame artifacts.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"

	"github.com/tmc/snes/internal/editor/machinebranch"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "snesbranch:", err)
		os.Exit(1)
	}
}
func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func run(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("snesbranch", flag.ContinueOnError)
	config := fs.String("config", "", "pinned JSON configuration path")
	pin := fs.String("config-sha256", "", "configuration SHA-256")
	out := fs.String("out", "", "new output directory, or prepared config file")
	prepare := fs.Bool("prepare-recovered", false, "derive recovered identities from explicitly pinned ROM and region")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *config == "" || *out == "" || len(*pin) != 64 {
		return fmt.Errorf("config, config-sha256 and out are required")
	}
	if _, err := os.Lstat(*out); err == nil {
		return fmt.Errorf("output already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.Open(*config)
	if err != nil {
		return err
	}
	b, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	f.Close()
	if err != nil {
		return err
	}
	if len(b) > 1<<20 || hash(b) != *pin {
		return fmt.Errorf("configuration size or hash mismatch")
	}
	var c machinebranch.Config
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("configuration has trailing data")
	}
	if *prepare {
		if c.Mode != "recovered_c" || c.Region == nil || c.SpriteEdit != nil {
			return fmt.Errorf("recovered mode and explicit region required")
		}
		romFile, err := os.Open(c.ROMPath)
		if err != nil {
			return err
		}
		rom, err := io.ReadAll(io.LimitReader(romFile, 4<<20+1))
		romFile.Close()
		if err != nil {
			return err
		}
		if len(rom) == 0 || len(rom) > 4<<20 || hash(rom) != c.ROMSHA256 {
			return fmt.Errorf("ROM identity or size mismatch")
		}
		identities, err := machinebranch.PrepareRecovered(rom, c.Addend, *c.Region)
		if err != nil {
			return err
		}
		c.Recovered = &identities
		material, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			return err
		}
		material = append(material, '\n')
		if err := os.MkdirAll(filepath.Dir(*out), 0700); err != nil {
			return err
		}
		temp, err := os.CreateTemp(filepath.Dir(*out), ".snesbranch-config-")
		if err != nil {
			return err
		}
		name := temp.Name()
		defer os.Remove(name)
		if _, err := temp.Write(material); err != nil {
			temp.Close()
			return err
		}
		if err := temp.Close(); err != nil {
			return err
		}
		if err := os.Link(name, *out); err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%s sha256=%s\n", *out, hash(material))
		return err
	}
	r, err := machinebranch.Run(context.Background(), c)
	if err != nil {
		return err
	}
	parent := filepath.Dir(*out)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".snesbranch-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err := os.WriteFile(filepath.Join(staging, "checkpoint.state"), r.Checkpoint, 0600); err != nil {
		return err
	}
	for _, branch := range []*machinebranch.Branch{&r.Baseline, &r.Replica} {
		dir := filepath.Join(staging, branch.Name)
		if err := os.Mkdir(dir, 0700); err != nil {
			return err
		}
		for i := range branch.Frames {
			frame := &branch.Frames[i]
			path := filepath.Join(dir, fmt.Sprintf("%06d.png", frame.RelativeFrame))
			if err := writePNG(path, *frame); err != nil {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			frame.PNGPath = branch.Name + "/" + filepath.Base(path)
			frame.PNGSHA256 = hash(b)
		}
	}
	if r.Mode == "recovered_c" {
		if r.Compiled == nil || r.Config.Recovered == nil || hash([]byte(r.Compiled.Source)) != r.Config.Recovered.SourceSHA256 {
			return fmt.Errorf("recovered source identity changed before publication")
		}

		p := r.Config.Recovered
		if r.Compiled.SemanticsOrigin != "generic_machine_ir" || r.Compiled.IRSHA256 != p.IRSHA256 || r.Compiled.EditedIRSHA256 != p.EditedIRSHA256 || r.Compiled.PlanSHA256 != p.PlanSHA256 || r.Compiled.EditSHA256 != p.EditSHA256 || r.Compiled.ROMSHA256 != r.Config.ROMSHA256 {
			return fmt.Errorf("recovered identities changed before publication")
		}
		for _, artifact := range []struct {
			name string
			data []byte
			sha  string
		}{
			{"original-ir.json", r.Compiled.OriginalIRJSON, r.Config.Recovered.IRSHA256},
			{"edited-ir.json", r.Compiled.EditedIRJSON, r.Config.Recovered.EditedIRSHA256},
		} {
			if len(artifact.data) == 0 || hash(artifact.data) != artifact.sha {
				return fmt.Errorf("recovered IR identity changed before publication")
			}
			if err := os.WriteFile(filepath.Join(staging, artifact.name), artifact.data, 0600); err != nil {
				return err
			}
		}
		if err := os.WriteFile(filepath.Join(staging, "recovered.c"), []byte(r.Compiled.Source), 0600); err != nil {
			return err
		}
		pins, err := json.MarshalIndent(r.Config.Recovered, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(staging, "recovered-identities.json"), append(pins, '\n'), 0600); err != nil {
			return err
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err := os.Open(exe)
	if err != nil {
		return err
	}
	executableHash := sha256.New()
	_, err = io.Copy(executableHash, executable)
	closeErr := executable.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	executableSHA := hex.EncodeToString(executableHash.Sum(nil))
	metadata, err := json.MarshalIndent(struct {
		ExecutableSHA256 string                `json:"consumer_executable_sha256"`
		ConfigFileSHA256 string                `json:"config_file_sha256"`
		Result           *machinebranch.Result `json:"result"`
	}{executableSHA, *pin, r}, "", "  ")
	if err != nil {
		return err
	}
	metadata = append(metadata, '\n')
	if err := os.WriteFile(filepath.Join(staging, "result.json"), metadata, 0600); err != nil {
		return err
	}
	if _, err := os.Lstat(*out); err == nil {
		return fmt.Errorf("output appeared during run")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(staging, *out); err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, filepath.Join(*out, "result.json"))
	return err
}

func writePNG(path string, frame machinebranch.Frame) error {
	if len(frame.Pixels) != frame.Width*frame.Height {
		return fmt.Errorf("invalid pixels")
	}
	im := image.NewRGBA(image.Rect(0, 0, frame.Width, frame.Height))
	expand := func(v uint16) uint8 { return uint8(v<<3 | v>>2) }
	for i, p := range frame.Pixels {
		im.SetRGBA(i%frame.Width, i/frame.Width, color.RGBA{expand(p & 31), expand(p >> 5 & 31), expand(p >> 10 & 31), 255})
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = png.Encode(f, im)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
