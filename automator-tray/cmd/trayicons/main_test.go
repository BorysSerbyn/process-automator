package main

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/BorysSerbyn/process-automator/automator-tray/internal/desktop"
	"github.com/BorysSerbyn/process-automator/automator-tray/internal/tray"
)

func decode(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// From the logo the macOS app is bundled from: every frame the tray shows,
// its dot in its light (dimmed in a pulse's dim half), and the app's icon.
func TestIconsFromTheLogo(t *testing.T) {
	out := t.TempDir()
	if err := run(filepath.Join("..", "..", "..", "automator-ui", "Resources", "Logo"), out); err != nil {
		t.Fatal(err)
	}
	// The dot's middle, in frame pixels (dot.svg: r=2.7 at (10.928, 3.25) of 18).
	cx, cy, size := 10.928, 3.25, float64(frameSize)
	x, y := int(cx/18*size), int(cy/18*size)
	for _, f := range tray.Frames() {
		img := decode(t, filepath.Join(out, f.File()))
		if img.Bounds().Dx() != frameSize || img.Bounds().Dy() != frameSize {
			t.Fatalf("%s: %v", f.File(), img.Bounds())
		}
		want := desktop.Colours[lights[f.Light]]
		r, g, b, a := img.At(x, y).RGBA()
		if f.Dim {
			if a>>8 > 0x50 || a == 0 {
				t.Errorf("%s: the dot isn't dimmed (alpha %#x)", f.File(), a>>8)
			}
			continue
		}
		if uint8(r>>8) != want.R || uint8(g>>8) != want.G || uint8(b>>8) != want.B || a>>8 != 0xff {
			t.Errorf("%s: dot %#x %#x %#x %#x, want %v", f.File(), r>>8, g>>8, b>>8, a>>8, want)
		}
	}
	if img := decode(t, filepath.Join(out, "app.png")); img.Bounds().Dx() != appSize {
		t.Fatalf("app icon %v", img.Bounds())
	}
}

func TestNotALogo(t *testing.T) {
	if err := run(t.TempDir(), t.TempDir()); err == nil {
		t.Fatal("made icons from nothing")
	}
}
