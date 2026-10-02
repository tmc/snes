// Snesedit serves a local read-only editor evidence shell.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/tmc/snes/internal/editor/machinebranch"
	"github.com/tmc/snes/internal/editor/web"
	"github.com/tmc/snes/internal/recovery/progress"
)

func main() {
	manifest := flag.String("manifest", "", "explicit pinned target observation manifest")
	address := flag.String("listen", "127.0.0.1:8460", "loopback HTTP address")
	capture := flag.String("provenance", "", "explicit gzip provenance capture")
	pin := flag.String("provenance-sha256", "", "expected capture SHA-256")
	frame := flag.Int("through-frame", -1, "inclusive provenance host frame")
	frames := flag.String("frames", "", "explicit original frame manifest")
	framesSHA := flag.String("frames-sha256", "", "expected frame manifest SHA-256")
	experiment := flag.String("experiment-config", "", "operator-pinned machine experiment config")
	experimentSHA := flag.String("experiment-sha256", "", "expected experiment config SHA-256")
	experimentOut := flag.String("experiment-out", "", "new durable experiment output directory")
	spriteConfig := flag.String("sprite-experiment-config", "", "operator-pinned no-edit sprite machine config")
	spriteSHA := flag.String("sprite-experiment-sha256", "", "expected sprite config SHA-256")
	spriteOut := flag.String("sprite-experiment-out", "", "new durable sprite experiment directory")
	progressConfig := flag.String("progress-config", "", "explicit recovery progress config")
	progressSHA := flag.String("progress-sha256", "", "expected progress config SHA-256")
	stateWrites := flag.String("state-writes", "", "explicit observation window JSON for state writes")
	stateWritesSHA := flag.String("state-writes-sha256", "", "expected observation window file SHA-256")
	flag.Parse()
	if err := run(*manifest, *address, *capture, *pin, *frame, *frames, *framesSHA, *experiment, *experimentSHA, *experimentOut, *spriteConfig, *spriteSHA, *spriteOut, *progressConfig, *progressSHA, *stateWrites, *stateWritesSHA); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(manifest, address, capture, pin string, frame int, frames, framesSHA, experiment, experimentSHA, experimentOut string, spriteOptions ...string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address must be loopback")
	}
	m, err := web.Load(manifest)
	if err != nil {
		return err
	}
	if len(spriteOptions) >= 5 && (spriteOptions[3] != "" || spriteOptions[4] != "") {
		m.Progress, err = progress.Load(context.Background(), spriteOptions[3], spriteOptions[4])
		if err != nil {
			return err
		}
		if m.Progress.ROMSHA256 != m.Target.ROMSHA256 {
			return fmt.Errorf("progress and target ROM identities differ")
		}
	}
	if len(spriteOptions) >= 7 && (spriteOptions[5] != "" || spriteOptions[6] != "") {
		m.StateWrites, err = loadStateWrites(spriteOptions[5], spriteOptions[6])
		if err != nil {
			return err
		}
		if m.StateWrites.Identity.ROMSHA256 != m.Target.ROMSHA256 {
			return fmt.Errorf("state writes and target ROM identities differ")
		}
	}
	if capture != "" || pin != "" {
		m.Sprites, err = web.LoadSprites(capture, pin, frame)
		if err != nil {
			return err
		}
		if m.Sprites.ROMSHA256 != m.Target.ROMSHA256 {
			return fmt.Errorf("target and sprite captures use different ROMs")
		}
		m.SpriteProvenance = "observed OAM entry; visible pixel ownership unknown"
	}
	if frames != "" || framesSHA != "" {
		m.Frames, err = web.LoadFrames(frames, framesSHA)
		if err != nil {
			return err
		}
		if m.Frames.ROMSHA256 != m.Target.ROMSHA256 {
			return fmt.Errorf("frame and target ROM identities differ")
		}
		m.Frame = "Original interpreter capture; edited C frames unavailable"
	}
	if experiment != "" || experimentSHA != "" || experimentOut != "" {
		m.Experiments, err = web.NewExperiments(experiment, experimentSHA, experimentOut, machinebranch.Run)
		if err != nil {
			return err
		}
		if m.Experiments.ROMSHA256() != m.Target.ROMSHA256 {
			return fmt.Errorf("experiment and target ROM identities differ")
		}
		m.ExperimentEnabled = true
	}
	if len(spriteOptions) >= 3 && (spriteOptions[0] != "" || spriteOptions[1] != "" || spriteOptions[2] != "") {
		m.SpriteExperiments, err = web.NewSpriteExperiments(spriteOptions[0], spriteOptions[1], spriteOptions[2], m.Sprites, machinebranch.Run)
		if err != nil {
			return err
		}
		if m.SpriteExperiments.ROMSHA256() != m.Target.ROMSHA256 {
			return fmt.Errorf("sprite experiment and target ROM identities differ")
		}
		m.SpriteExperimentID = m.SpriteExperiments.SpriteID()
	}
	s := &http.Server{Addr: address, Handler: localHostHandler(web.Handler(m)), ReadHeaderTimeout: 5e9}
	return s.ListenAndServe()
}

// localHostHandler requires a literal loopback authority on the local server.
func localHostHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !ip.IsLoopback() {
			http.Error(w, "literal loopback host required", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
