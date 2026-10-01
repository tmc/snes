// Snesedit serves a local read-only editor evidence shell.
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/tmc/snes/internal/editor/web"
)

func main() {
	manifest := flag.String("manifest", "", "explicit pinned target observation manifest")
	address := flag.String("listen", "127.0.0.1:8460", "loopback HTTP address")
	capture := flag.String("provenance", "", "explicit gzip provenance capture")
	pin := flag.String("provenance-sha256", "", "expected capture SHA-256")
	frame := flag.Int("through-frame", -1, "inclusive provenance host frame")
	frames := flag.String("frames", "", "explicit original frame manifest")
	framesSHA := flag.String("frames-sha256", "", "expected frame manifest SHA-256")
	flag.Parse()
	if err := run(*manifest, *address, *capture, *pin, *frame, *frames, *framesSHA); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(manifest, address, capture, pin string, frame int, frames, framesSHA string) error {
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
	s := &http.Server{Addr: address, Handler: web.Handler(m), ReadHeaderTimeout: 5e9}
	return s.ListenAndServe()
}
