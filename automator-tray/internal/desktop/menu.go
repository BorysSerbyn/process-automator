package desktop

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
)

// Label is a menu row's text as dbusmenu shows it as is: an underscore
// marks a mnemonic there, and two make one.
func Label(text string) string { return strings.ReplaceAll(text, "_", "__") }

// Colours are the lights' (automator-ui's system green, red and grey), as
// the routines' dots show them.
var Colours = map[string]color.NRGBA{
	"ok":    {R: 0x34, G: 0xC7, B: 0x59, A: 0xFF},
	"error": {R: 0xFF, G: 0x3B, B: 0x30, A: 0xFF},
	"none":  {R: 0x8E, G: 0x8E, B: 0x93, A: 0xFF},
}

// Dot is a routine's health dot for its menu row, as a PNG.
func Dot(health string) []byte {
	const size, r = 16, 4.5
	c, ok := Colours[health]
	if !ok {
		c = Colours["none"]
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			// Coverage of the pixel by the circle, sampled 4×4, for smooth edges.
			n := 0
			for sy := range 4 {
				for sx := range 4 {
					dx := float64(x) + (float64(sx)+0.5)/4 - size/2
					dy := float64(y) + (float64(sy)+0.5)/4 - size/2
					if dx*dx+dy*dy <= r*r {
						n++
					}
				}
			}
			if n > 0 {
				img.SetNRGBA(x, y, color.NRGBA{R: c.R, G: c.G, B: c.B, A: uint8(int(c.A) * n / 16)})
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}
