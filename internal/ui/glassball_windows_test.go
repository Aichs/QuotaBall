//go:build windows

package ui

import (
	"image"
	"image/color"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/lxn/walk"
)

func TestRenderEmptyBallDoesNotDrawCenterMeter(t *testing.T) {
	img := renderBallImage(0, false, 0)
	c := img.At(118, 95)
	r, g, b, _ := c.RGBA()

	if uint8(r>>8) == 10 && uint8(g>>8) == 112 && uint8(b>>8) == 158 {
		t.Fatalf("empty glass ball drew the active center meter ring at (118,95)")
	}
}

func TestRenderActiveZeroPercentDoesNotDrawProgressArc(t *testing.T) {
	img := renderBallImage(0, true, 0)
	c := img.At(95, 66)
	r, g, b, _ := c.RGBA()

	if uint8(r>>8) < 80 && uint8(g>>8) > 160 && uint8(b>>8) > 220 {
		t.Fatalf("0%% glass ball drew the bright progress arc at (95,66): rgb=(%d,%d,%d)", uint8(r>>8), uint8(g>>8), uint8(b>>8))
	}
}

func TestDraggedBallBoundsUsesScreenCoordinates(t *testing.T) {
	got := draggedBallBounds(walk.Point{X: 320, Y: 140}, walk.Point{X: 500, Y: 500}, walk.Point{X: 548, Y: 532})
	want := walk.Rectangle{X: 368, Y: 172, Width: ballSize, Height: ballSize}
	if got != want {
		t.Fatalf("dragged bounds mismatch: got %+v want %+v", got, want)
	}
}

func TestCenterClickToleratesSmallPointerJitter(t *testing.T) {
	for _, pt := range []struct {
		dx int
		dy int
	}{
		{dx: 4, dy: 3},
		{dx: -5, dy: 4},
		{dx: 6, dy: 0},
	} {
		if ballDragDistanceExceeded(pt.dx, pt.dy) {
			t.Fatalf("small center-click jitter was treated as drag: dx=%d dy=%d", pt.dx, pt.dy)
		}
	}
	if !ballDragDistanceExceeded(9, 0) {
		t.Fatalf("real drag movement was not detected")
	}
}

func TestBallCenterMatchesPySideSphereRect(t *testing.T) {
	x, y := ballCenter(ballRect())
	if x != 95 || y != 94 {
		t.Fatalf("ball center mismatch: got (%v,%v), want (95,94)", x, y)
	}
}

func TestReferenceBallHasDarkHorizontalBand(t *testing.T) {
	img := renderBallImage(34, true, 0)
	r, g, b, a := img.At(28, 94).RGBA()
	if uint8(a>>8) < 200 || uint8(r>>8) > 80 || uint8(g>>8) > 100 || uint8(b>>8) > 130 {
		t.Fatalf("center band pixel does not look like the reference dark strip: rgba=(%d,%d,%d,%d)", uint8(r>>8), uint8(g>>8), uint8(b>>8), uint8(a>>8))
	}
}

func TestReferenceBallKeepsWaterInLowerHalf(t *testing.T) {
	img := renderBallImage(34, true, 0)
	top := img.At(95, 44)
	tr, tg, tb, _ := top.RGBA()
	if uint8(tb>>8) > 210 && uint8(tg>>8) > 180 && uint8(tr>>8) < 80 {
		t.Fatalf("top glass area looks like cyan water: rgb=(%d,%d,%d)", uint8(tr>>8), uint8(tg>>8), uint8(tb>>8))
	}

	water := img.At(95, 150)
	wr, wg, wb, wa := water.RGBA()
	if uint8(wa>>8) < 210 || uint8(wg>>8) < 170 || uint8(wb>>8) < 190 || uint8(wr>>8) > 170 {
		t.Fatalf("lower half does not look like bright cyan water: rgba=(%d,%d,%d,%d)", uint8(wr>>8), uint8(wg>>8), uint8(wb>>8), uint8(wa>>8))
	}
}

func TestCenterButtonKeepsPercentRing(t *testing.T) {
	img := renderBallImage(100, true, 0)
	r, g, b, a := img.At(95, 66).RGBA()
	if uint8(a>>8) != 255 || uint8(g>>8) < 160 || uint8(b>>8) < 220 || uint8(r>>8) > 90 {
		t.Fatalf("center percent ring is not bright blue at the top: rgba=(%d,%d,%d,%d)", uint8(r>>8), uint8(g>>8), uint8(b>>8), uint8(a>>8))
	}
}

func TestActiveFrameDrawsCenterPercentText(t *testing.T) {
	base := toRGBA(renderBallImage(34, true, 0))
	frame, err := renderBallFrameImage(34, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	changed := 0
	for y := 84; y <= 102; y++ {
		for x := 78; x <= 112; x++ {
			a := base.RGBAAt(x, y)
			b := frame.RGBAAt(x, y)
			if abs(int(a.R)-int(b.R))+abs(int(a.G)-int(b.G))+abs(int(a.B)-int(b.B))+abs(int(a.A)-int(b.A)) > 18 {
				changed++
			}
		}
	}
	if changed < 12 {
		t.Fatalf("active glass ball frame did not draw center percent text: changed=%d", changed)
	}
}

func TestActiveFrameWaterChangesAcrossAnimationPhases(t *testing.T) {
	a, err := renderBallFrameImage(34, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := renderBallFrameImage(34, true, ballAnimationStep*6)
	if err != nil {
		t.Fatal(err)
	}
	changed := 0
	for y := 112; y <= 150; y++ {
		for x := 32; x <= 158; x++ {
			c0 := a.RGBAAt(x, y)
			c1 := b.RGBAAt(x, y)
			if abs(int(c0.R)-int(c1.R))+abs(int(c0.G)-int(c1.G))+abs(int(c0.B)-int(c1.B))+abs(int(c0.A)-int(c1.A)) > 18 {
				changed++
			}
		}
	}
	if changed < 40 {
		t.Fatalf("active frame water barely changes across animation phases: changed=%d", changed)
	}
}

func TestActivePercentTextMaskIsCachedAcrossAnimationFrames(t *testing.T) {
	textMaskCacheMu.Lock()
	textMaskCache = make(map[string]*image.RGBA)
	textMaskCacheMu.Unlock()

	for i := 0; i < 5; i++ {
		if _, err := renderBallFrameImage(34, true, float64(i)*ballAnimationStep); err != nil {
			t.Fatal(err)
		}
	}

	textMaskCacheMu.Lock()
	got := len(textMaskCache)
	textMaskCacheMu.Unlock()
	if got != 1 {
		t.Fatalf("percent text mask should be cached across animation frames: got %d cached masks, want 1", got)
	}
}

func TestGlassBallUsesPureLayeredRenderingPath(t *testing.T) {
	raw, err := os.ReadFile("glassball_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if strings.Contains(src, "CustomWidget{") || strings.Contains(src, "PaintPixels:") || strings.Contains(src, "walk.Canvas") {
		t.Fatalf("glass ball must not use a child canvas fallback; it can expose an opaque gray square on layered windows")
	}
	if strings.Contains(src, "setColorKey(g") || strings.Contains(src, "SetLayeredWindowAttributes") {
		t.Fatalf("glass ball must not mix color-key layered attributes with UpdateLayeredWindow")
	}
	if !strings.Contains(src, "applyToolWindow(gb.win.Handle())") || !strings.Contains(src, "WS_EX_TOOLWINDOW") || !strings.Contains(src, "WS_EX_APPWINDOW") {
		t.Fatalf("glass ball must be a tool window so the floating ball never appears in the taskbar")
	}
	if !strings.Contains(src, "OnMouseDown: gb.mouseDown") || !strings.Contains(src, "OnMouseUp:   gb.mouseUp") {
		t.Fatalf("glass ball must keep top-level mouse handlers for dragging and center-button toggling")
	}
	if !strings.Contains(src, "if err := updateLayeredWindowImage") {
		t.Fatalf("glass ball must observe UpdateLayeredWindow failures instead of ignoring them")
	}
	if !strings.Contains(src, "resetLayeredForPerPixelAlpha") || !strings.Contains(src, "retry after WS_EX_LAYERED reset failed") {
		t.Fatalf("glass ball must reset and retry the layered style before hiding after an UpdateLayeredWindow failure")
	}
	if !strings.Contains(src, "handleRenderFailure") || !strings.Contains(src, "SetVisible(false)") {
		t.Fatalf("glass ball must hide on layered rendering failure so a gray square is never shown")
	}
}

func TestGlassBallShowPreparesLayeredFrameBeforeVisible(t *testing.T) {
	raw, err := os.ReadFile("glassball_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	showStart := strings.Index(src, "func (g *glassBall) show()")
	if showStart < 0 {
		t.Fatal("glassBall.show not found")
	}
	showSrc := src[showStart:]
	configIdx := strings.Index(showSrc, "g.applyCurrentConfig()")
	repaintIdx := strings.Index(showSrc, "if !g.repaint()")
	visibleIdx := strings.Index(showSrc, "g.win.SetVisible(true)")
	if configIdx < 0 || repaintIdx < 0 || visibleIdx < 0 {
		t.Fatalf("show must apply current config, repaint, and then show; indexes config=%d repaint=%d visible=%d", configIdx, repaintIdx, visibleIdx)
	}
	if !(configIdx < repaintIdx && repaintIdx < visibleIdx) {
		t.Fatalf("show must prepare the current configured frame before SetVisible(true); indexes config=%d repaint=%d visible=%d", configIdx, repaintIdx, visibleIdx)
	}
}

func TestGlassControllerCloseAvoidsUIThreadSelfDeadlock(t *testing.T) {
	raw, err := os.ReadFile("glass_controller_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []string{
		"uiThreadID uint32",
		"win.GetCurrentThreadId()",
		"if isUIThread {",
		"closeFn()",
		"if !isUIThread {",
		"<-c.done",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("glass controller Close must avoid queuing a close and waiting from the UI thread; missing %q", want)
		}
	}
}

func TestBackGlassUsesReferenceTranslucentCompositing(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, ballSize, ballSize))
	fillImageRGBA(img, transparentKeyColor())

	drawBackGlass(img, ballRect())

	c := img.RGBAAt(95, 170)
	if c.A >= 255 {
		t.Fatalf("back glass bottom is fully opaque: got %+v, want reference-style translucent alpha", c)
	}
	if c.A < 70 {
		t.Fatalf("back glass bottom is too transparent: got %+v, want a visible glass body", c)
	}
	if sameRGB(c, color.RGBA{0, 23, 58, 255}) && c.A == 255 {
		t.Fatalf("back glass wrote the raw opaque gradient stop instead of translucent glass")
	}
}

func TestPremultipliedBGRAUsesSourceAlpha(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	src.SetNRGBA(0, 0, color.NRGBA{R: 80, G: 160, B: 240, A: 128})

	got := premultipliedBGRA(src)
	want := []byte{120, 80, 40, 128}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("premultiplied BGRA mismatch at %d: got %d want %d; full=%v", i, got[i], want[i], got)
		}
	}
}

func TestLayeredWindowBufferKeepsTransparentCorners(t *testing.T) {
	img := renderBallImage(34, true, 0)
	buf := premultipliedBGRA(img)
	if len(buf) != ballSize*ballSize*4 {
		t.Fatalf("layered buffer size mismatch: got %d", len(buf))
	}
	if alpha := buf[3]; alpha != 0 {
		t.Fatalf("top-left layered alpha is opaque, would show a square: alpha=%d", alpha)
	}
	center := (95*ballSize + 95) * 4
	if alpha := buf[center+3]; alpha == 0 {
		t.Fatalf("center layered alpha is transparent, would hide the ball")
	}
}

func TestRenderedBallUsesFeatheredOuterEdge(t *testing.T) {
	img := renderBallImage(34, true, 0)
	partial := 0
	for y := 0; y < ballSize; y++ {
		for x := 0; x < ballSize; x++ {
			a := img.(*image.RGBA).RGBAAt(x, y).A
			if a > 0 && a < 180 {
				partial++
			}
		}
	}
	if partial < 120 {
		t.Fatalf("glass ball outer edge has too few feathered pixels: got %d", partial)
	}
}

func TestWaterAnimationStepIsVisiblyResponsive(t *testing.T) {
	if ballAnimationStep < 0.18 {
		t.Fatalf("water animation step is too slow: got %.3f", ballAnimationStep)
	}
	r := ballRect()
	baseY := waterBaseY(r, 34)
	y0 := waterSurfaceY(r, r.cx(), baseY, 0, 34)
	y1 := waterSurfaceY(r, r.cx(), baseY, ballAnimationStep*3, 34)
	if math.Abs(y1-y0) < 0.25 {
		t.Fatalf("water surface movement is too subtle over several frames: y0=%.3f y1=%.3f", y0, y1)
	}
}

func TestRenderedBallHasNoDetachedBottomBlock(t *testing.T) {
	img, err := renderBallFrameImage(34, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	for y := int(math.Ceil(ballRect().bottom)) + 2; y < ballSize; y++ {
		for x := 0; x < ballSize; x++ {
			if a := img.RGBAAt(x, y).A; a != 0 {
				t.Fatalf("bottom tail pixel is not transparent at (%d,%d): alpha=%d", x, y, a)
			}
		}
	}
}

func TestRenderedBallHasNoWhiteBottomPlasterStrip(t *testing.T) {
	img, err := renderBallFrameImage(34, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	r := ballRect()
	nearWhite := 0
	for y := int(r.bottom) - 12; y <= int(r.bottom); y++ {
		for x := int(r.left) + 12; x <= int(r.right)-12; x++ {
			c := img.RGBAAt(x, y)
			if c.A > 180 && c.R > 235 && c.G > 235 && c.B > 235 {
				nearWhite++
			}
		}
	}
	if nearWhite > 16 {
		t.Fatalf("bottom of glass ball contains a detached white plaster-like strip: nearWhite=%d", nearWhite)
	}
}

func sameRGB(a, b color.RGBA) bool {
	return a.R == b.R && a.G == b.G && a.B == b.B
}
