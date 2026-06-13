//go:build windows

package ui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteGlassBallPreview(t *testing.T) {
	out := os.Getenv("KRILL_GLASSBALL_PREVIEW")
	if out == "" {
		t.Skip("preview disabled")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := renderBallFrameImage(34, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestWriteGlassBallBasePreview(t *testing.T) {
	out := os.Getenv("KRILL_GLASSBALL_BASE_PREVIEW")
	if out == "" {
		t.Skip("preview disabled")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, renderBallImage(17, true, 0)); err != nil {
		t.Fatal(err)
	}
}

func TestTraceGlassBallPixel(t *testing.T) {
	if os.Getenv("KRILL_GLASSBALL_TRACE") == "" {
		t.Skip("trace disabled")
	}
	img := image.NewRGBA(image.Rect(0, 0, ballSize, ballSize))
	fillImageRGBA(img, color.RGBA{})
	r := ballRect()
	trace := func(name string) {
		c := img.RGBAAt(100, 20)
		t.Logf("%s: R=%d G=%d B=%d A=%d", name, c.R, c.G, c.B, c.A)
	}
	trace("start")
	drawSphereShadow(img, r)
	trace("shadow")
	drawBackGlass(img, r)
	trace("back")
	drawWater(img, r, 17, 0, weeklyWaterPalette())
	trace("water")
	drawEquatorBand(img, r)
	trace("band")
	drawFrontGlass(img, r)
	trace("front")
	drawCenter(img, 17)
	trace("center")
	drawReferenceRim(img, r)
	trace("rim")
}
