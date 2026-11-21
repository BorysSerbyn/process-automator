// trayicons makes the tray's icons from an app's logo when the app is
// packaged, so the tray only loads PNGs: each frame of the status icon (the
// glyph with its dot in each light, and dimmed for pulsing) and the app's
// icon for its notifications.
//
// Usage: trayicons <logo-dir> <out-dir>
//
// The logo folder is the one the macOS app is bundled from: glyph.svg and
// dot.svg (one-colour shapes, drawn over each other), and AppIcon.icns.
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	xdraw "golang.org/x/image/draw"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/desktop"
	"github.com/BorysSerbyn/process-automator/automator-tray/internal/tray"
)

// The icons' sizes: the tray's frames (the desktop scales them to its
// panel), and the app's icon in notifications.
const (
	frameSize = 48
	appSize   = 256
)

// glyphColour is the glyph's: light, for the dark panels most desktops
// have, with a dark rim (rimAlpha) that keeps it legible on a light one.
var glyphColour = color.NRGBA{R: 0xF2, G: 0xF2, B: 0xF2, A: 0xFF}

const (
	rimAlpha = 0x70
	// dimAlpha is the dot's opacity in a pulse's dim half (automator-ui
	// fades it to 0.25).
	dimAlpha = 0.25
)

// lights are the dot's colours, as the routines' dots.
var lights = map[tray.Light]string{tray.Green: "ok", tray.Red: "error", tray.Grey: "none"}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: trayicons <logo-dir> <out-dir>")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "trayicons:", err)
		os.Exit(1)
	}
}

func run(logo, out string) error {
	glyph, err := mask(filepath.Join(logo, "glyph.svg"), frameSize)
	if err != nil {
		return err
	}
	dot, err := mask(filepath.Join(logo, "dot.svg"), frameSize)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	for _, f := range tray.Frames() {
		dotColour := desktop.Colours[lights[f.Light]]
		alpha := 1.0
		if f.Dim {
			alpha = dimAlpha
		}
		if err := writePNG(filepath.Join(out, f.File()), frame(glyph, dot, dotColour, alpha)); err != nil {
			return err
		}
	}
	icon, err := appIcon(filepath.Join(logo, "AppIcon.icns"))
	if err != nil {
		return err
	}
	return writePNG(filepath.Join(out, "app.png"), icon)
}

// mask renders a one-colour SVG at size×size: its alpha is the shape.
func mask(path string, size int) (*image.Alpha, error) {
	icon, err := oksvg.ReadIcon(path, oksvg.StrictErrorMode)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	icon.SetTarget(0, 0, float64(size), float64(size))
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	icon.Draw(rasterx.NewDasher(size, size, rasterx.NewScannerGV(size, size, img, img.Bounds())), 1)
	a := image.NewAlpha(img.Bounds())
	for i := range a.Pix {
		a.Pix[i] = img.Pix[i*4+3]
	}
	return a, nil
}

// frame is one status icon: a rim around both shapes, the glyph over it,
// and the dot in its light (at dotAlpha) on top.
func frame(glyph, dot *image.Alpha, dotColour color.NRGBA, dotAlpha float64) *image.NRGBA {
	b := glyph.Bounds()
	img := image.NewNRGBA(b)
	shape := image.NewAlpha(b)
	for i := range shape.Pix {
		shape.Pix[i] = max(glyph.Pix[i], dot.Pix[i])
	}
	draw.DrawMask(img, b, image.NewUniform(color.NRGBA{A: rimAlpha}), image.Point{}, dilate(shape), image.Point{}, draw.Over)
	draw.DrawMask(img, b, image.NewUniform(glyphColour), image.Point{}, glyph, image.Point{}, draw.Over)
	dim := image.NewAlpha(b)
	for i := range dim.Pix {
		dim.Pix[i] = uint8(float64(dot.Pix[i]) * dotAlpha)
	}
	// The dot fades to the panel (its rim stays), as automator-ui's does:
	// clear what's under it first.
	for i, m := range dot.Pix {
		keep := 255 - uint32(m)
		img.Pix[i*4+3] = uint8(uint32(img.Pix[i*4+3]) * keep / 255)
	}
	draw.DrawMask(img, b, image.NewUniform(dotColour), image.Point{}, dim, image.Point{}, draw.Over)
	return img
}

// dilate grows a shape by a pixel each way: its rim.
func dilate(a *image.Alpha) *image.Alpha {
	b := a.Bounds()
	out := image.NewAlpha(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			var m uint8
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if p := (image.Point{X: x + dx, Y: y + dy}); p.In(b) {
						m = max(m, a.AlphaAt(p.X, p.Y).A)
					}
				}
			}
			out.SetAlpha(x, y, color.Alpha{A: m})
		}
	}
	return out
}

// appIcon is the app's icon at appSize, from the PNGs in its .icns: the
// smallest at least appSize, scaled down.
func appIcon(path string) (image.Image, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) < 8 || string(b[:4]) != "icns" {
		return nil, fmt.Errorf("%s isn't an .icns", path)
	}
	var best image.Image
	for rest := b[8:]; len(rest) >= 8; {
		n := int(binary.BigEndian.Uint32(rest[4:8]))
		if n < 8 || n > len(rest) {
			break
		}
		data := rest[8:n]
		rest = rest[n:]
		if !bytes.HasPrefix(data, []byte("\x89PNG")) {
			continue
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			continue
		}
		w := img.Bounds().Dx()
		if best == nil || (w >= appSize && (best.Bounds().Dx() < appSize || w < best.Bounds().Dx())) ||
			(best.Bounds().Dx() < appSize && w > best.Bounds().Dx()) {
			best = img
		}
	}
	if best == nil {
		return nil, errors.New(path + " holds no PNG icon")
	}
	if best.Bounds().Dx() == appSize {
		return best, nil
	}
	out := image.NewNRGBA(image.Rect(0, 0, appSize, appSize))
	xdraw.CatmullRom.Scale(out, out.Bounds(), best, best.Bounds(), xdraw.Over, nil)
	return out, nil
}

func writePNG(path string, img image.Image) error {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}
