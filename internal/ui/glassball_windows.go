//go:build windows

package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"

	"krill_monitor/internal/config"
	"krill_monitor/internal/krill"
)

const (
	ballSize          = 190
	ballAnimationStep = 0.24
	ballDragThreshold = 8
)

type glassBall struct {
	host            glassBallHost
	win             *walk.MainWindow
	stop            chan struct{}
	mu              sync.Mutex
	snap            krill.Snapshot
	mode            string
	phase           float64
	dragStart       *walk.Point
	winStart        walk.Point
	dragging        bool
	centerHit       bool
	lastRenderError string
}

type glassBallHost interface {
	loadGlassConfig() config.Config
	mutateGlassConfig(func(*config.Config))
	togglePanel()
	refresh(bool)
	quit()
}

func newGlassBall(host glassBallHost) (*glassBall, error) {
	cfg := host.loadGlassConfig()
	gb := &glassBall{host: host, mode: normalizedGlassMetric(cfg.TbarMetric), stop: make(chan struct{})}
	if err := (MainWindow{
		AssignTo:    &gb.win,
		Title:       "",
		Size:        Size{ballSize, ballSize},
		Layout:      VBox{MarginsZero: true},
		OnMouseDown: gb.mouseDown,
		OnMouseMove: gb.mouseMove,
		OnMouseUp:   gb.mouseUp,
	}).Create(); err != nil {
		return nil, err
	}
	gb.win.SetClientSize(walk.Size{Width: ballSize, Height: ballSize})
	gb.position()
	applyFrameless(gb.win.Handle(), true)
	applyToolWindow(gb.win.Handle())
	applyTopMost(gb.win.Handle(), true)
	gb.installMenu()
	go gb.frameLoop()
	gb.win.SetVisible(false)
	return gb, nil
}

func (g *glassBall) frameLoop() {
	ticker := time.NewTicker(33 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if g.win == nil || g.win.IsDisposed() {
				return
			}
			g.win.Synchronize(func() {
				if g.dragStart != nil || !win.IsWindowVisible(g.win.Handle()) {
					return
				}
				g.phase = math.Mod(g.phase+ballAnimationStep, 10000)
				g.repaint()
			})
		case <-g.stop:
			return
		}
	}
}

func (g *glassBall) installMenu() {
	menu, _ := walk.NewMenu()
	for _, item := range []struct {
		text string
		fn   func()
	}{
		{"显示/隐藏面板", g.host.togglePanel},
		{"切换当日/转结", g.toggleMetric},
		{"立即刷新", func() { g.host.refresh(true) }},
		{"退出", g.host.quit},
	} {
		act := walk.NewAction()
		act.SetText(item.text)
		fn := item.fn
		act.Triggered().Attach(fn)
		_ = menu.Actions().Add(act)
	}
	g.win.SetContextMenu(menu)
}

func (g *glassBall) position() {
	x, y := 1200, 120
	cfg := g.host.loadGlassConfig()
	if cfg.TbarX != nil {
		x = *cfg.TbarX
	}
	if cfg.TbarY != nil {
		y = *cfg.TbarY
	}
	_ = g.win.SetBounds(walk.Rectangle{X: x, Y: y, Width: ballSize, Height: ballSize})
}

func (g *glassBall) show() {
	if g.win != nil {
		applyFrameless(g.win.Handle(), true)
		applyToolWindow(g.win.Handle())
		applyTopMost(g.win.Handle(), true)
		g.applyCurrentConfig()
		if !g.repaint() {
			g.win.SetVisible(false)
			return
		}
		g.win.SetVisible(true)
	}
}

func (g *glassBall) applyCurrentConfig() {
	cfg := g.host.loadGlassConfig()
	g.mode = normalizedGlassMetric(cfg.TbarMetric)

	x, y := 1200, 120
	if cfg.TbarX != nil {
		x = *cfg.TbarX
	}
	if cfg.TbarY != nil {
		y = *cfg.TbarY
	}
	_ = g.win.SetBounds(walk.Rectangle{X: x, Y: y, Width: ballSize, Height: ballSize})
}

func (g *glassBall) hide() {
	if g.win != nil {
		g.clearPointerState()
		g.win.SetVisible(false)
	}
}

func (g *glassBall) close() {
	g.savePos()
	close(g.stop)
	if g.win != nil {
		g.win.Dispose()
	}
}

func (g *glassBall) setSnapshot(s krill.Snapshot) {
	g.mu.Lock()
	g.snap = s
	g.mu.Unlock()
	if g.win != nil && !g.win.IsDisposed() && win.IsWindowVisible(g.win.Handle()) {
		g.repaint()
	}
}

func (g *glassBall) activeSub() (krill.Subscription, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, s := range g.snap.Subscriptions {
		if s.DailyRemaining > 0.0001 || s.ForwardedRemaining > 0.0001 {
			return s, true
		}
	}
	for _, s := range g.snap.Subscriptions {
		if s.DailyLimit > 0.0001 || s.ForwardedLimit > 0.0001 {
			return s, true
		}
	}
	if len(g.snap.Subscriptions) > 0 {
		return g.snap.Subscriptions[0], true
	}
	return krill.Subscription{}, false
}

func (g *glassBall) metric() (string, float64, float64, float64, bool) {
	g.mu.Lock()
	ok := g.snap.OK
	g.mu.Unlock()
	sub, has := g.activeSub()
	if !ok || !has {
		return "", 0, 0, 0, false
	}
	if g.mode == "forwarded" {
		return "转结", sub.ForwardedPercent, sub.ForwardedUsed, sub.ForwardedLimit, true
	}
	return "当日", sub.DailyPercent, sub.DailyUsed, sub.DailyLimit, true
}

func (g *glassBall) repaint() bool {
	if g.win == nil || g.win.IsDisposed() {
		return false
	}
	_, pct, _, _, ok := g.metric()
	img, err := renderBallFrameImage(pct, ok, g.phase)
	if err != nil {
		g.handleRenderFailure("render frame", err)
		return false
	}
	if err := updateLayeredWindowImage(g.win.Handle(), img); err != nil {
		resetLayeredForPerPixelAlpha(g.win.Handle())
		if retryErr := updateLayeredWindowImage(g.win.Handle(), img); retryErr != nil {
			g.handleRenderFailure("UpdateLayeredWindow", fmt.Errorf("%w; retry after WS_EX_LAYERED reset failed: %v", err, retryErr))
			return false
		}
	}
	g.lastRenderError = ""
	return true
}

func (g *glassBall) handleRenderFailure(stage string, err error) {
	var hwnd win.HWND
	if g.win != nil && !g.win.IsDisposed() {
		hwnd = g.win.Handle()
	}
	sig := stage + ": " + fmt.Sprint(err)
	if sig != g.lastRenderError {
		g.lastRenderError = sig
		writeGlassBallDiagnostic(hwnd, stage, err)
	}
	if g.win != nil && !g.win.IsDisposed() {
		g.win.SetVisible(false)
	}
}

func resetLayeredForPerPixelAlpha(hwnd win.HWND) {
	ex := win.GetWindowLong(hwnd, win.GWL_EXSTYLE)
	win.SetWindowLong(hwnd, win.GWL_EXSTYLE, ex&^int32(win.WS_EX_LAYERED))
	win.SetWindowPos(hwnd, 0, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_NOACTIVATE|win.SWP_FRAMECHANGED)
	win.SetWindowLong(hwnd, win.GWL_EXSTYLE, ex|int32(win.WS_EX_LAYERED))
	win.SetWindowPos(hwnd, 0, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_NOACTIVATE|win.SWP_FRAMECHANGED)
}

func applyToolWindow(hwnd win.HWND) {
	ex := win.GetWindowLong(hwnd, win.GWL_EXSTYLE)
	ex &^= int32(win.WS_EX_APPWINDOW)
	ex |= int32(win.WS_EX_TOOLWINDOW)
	win.SetWindowLong(hwnd, win.GWL_EXSTYLE, ex)
	win.SetWindowPos(hwnd, 0, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_NOACTIVATE|win.SWP_FRAMECHANGED)
}

func writeGlassBallDiagnostic(hwnd win.HWND, stage string, err error) {
	path := glassBallDiagnosticPath()
	if dir := filepath.Dir(path); dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	f, openErr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if openErr != nil {
		return
	}
	defer f.Close()
	var style, exStyle int32
	var rect win.RECT
	visible := false
	if hwnd != 0 {
		style = win.GetWindowLong(hwnd, win.GWL_STYLE)
		exStyle = win.GetWindowLong(hwnd, win.GWL_EXSTYLE)
		visible = win.IsWindowVisible(hwnd)
		_ = win.GetWindowRect(hwnd, &rect)
	}
	_, _ = fmt.Fprintf(
		f,
		"%s glass ball %s failed: %v hwnd=0x%x visible=%t style=0x%08x exstyle=0x%08x rect=(%d,%d,%d,%d) size=%dx%d\n",
		time.Now().Format(time.RFC3339),
		stage,
		err,
		uintptr(hwnd),
		visible,
		uint32(style),
		uint32(exStyle),
		rect.Left,
		rect.Top,
		rect.Right,
		rect.Bottom,
		ballSize,
		ballSize,
	)
}

func glassBallDiagnosticPath() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "glassball-diagnostics.log")
	}
	return filepath.Join(os.TempDir(), "krill-glassball-diagnostics.log")
}

func (g *glassBall) mouseDown(x, y int, button walk.MouseButton) {
	if button != walk.LeftButton {
		return
	}
	pt := cursorPoint()
	g.dragStart = &pt
	b := g.win.Bounds()
	g.winStart = walk.Point{X: b.X, Y: b.Y}
	g.dragging = false
	g.centerHit = hitCenter(x, y)
}

func (g *glassBall) mouseMove(x, y int, button walk.MouseButton) {
	if button != walk.LeftButton || g.dragStart == nil {
		return
	}
	pt := cursorPoint()
	dx := pt.X - g.dragStart.X
	dy := pt.Y - g.dragStart.Y
	if ballDragDistanceExceeded(dx, dy) {
		g.dragging = true
	}
	if g.dragging {
		_ = g.win.SetBounds(draggedBallBounds(g.winStart, *g.dragStart, pt))
	}
}

func (g *glassBall) mouseUp(x, y int, button walk.MouseButton) {
	if button != walk.LeftButton {
		g.clearPointerState()
		return
	}
	if g.dragging {
		g.savePos()
	} else if g.centerHit && hitCenter(x, y) {
		g.toggleMetric()
	} else {
		g.host.togglePanel()
	}
	g.clearPointerState()
}

func (g *glassBall) clearPointerState() {
	g.dragStart = nil
	g.dragging = false
	g.centerHit = false
}

func draggedBallBounds(startWindow, dragStart, cursor walk.Point) walk.Rectangle {
	return walk.Rectangle{
		X:      startWindow.X + cursor.X - dragStart.X,
		Y:      startWindow.Y + cursor.Y - dragStart.Y,
		Width:  ballSize,
		Height: ballSize,
	}
}

func (g *glassBall) toggleMetric() {
	if g.mode == "daily" {
		g.mode = "forwarded"
	} else {
		g.mode = "daily"
	}
	g.host.mutateGlassConfig(func(cfg *config.Config) {
		cfg.TbarMetric = g.mode
	})
	g.repaint()
}

func (g *glassBall) savePos() {
	if g.win == nil || g.win.IsDisposed() {
		return
	}
	b := g.win.Bounds()
	g.host.mutateGlassConfig(func(cfg *config.Config) {
		cfg.TbarX = &b.X
		cfg.TbarY = &b.Y
	})
}

func renderBallImage(pct float64, ok bool, phase float64) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, ballSize, ballSize))
	fillImageRGBA(img, transparentKeyColor())

	rect := ballRect()
	if !ok {
		drawEmptyBall(img, rect)
		drawReferenceRim(img, rect)
		clearOutsideSphere(img, rect, 0.9)
		clearDetachedBottomTail(img, rect)
		return img
	}

	drawSphereShadow(img, rect)
	drawBackGlass(img, rect)
	drawWater(img, rect, pct, phase)
	drawEquatorBand(img, rect)
	drawFrontGlass(img, rect)
	drawCenter(img, pct)
	drawReferenceRim(img, rect)
	clearOutsideSphere(img, rect, 0.9)
	clearDetachedBottomTail(img, rect)
	return img
}

func renderBallFrameImage(pct float64, ok bool, phase float64) (*image.RGBA, error) {
	base := toRGBA(renderBallImage(pct, ok, phase))
	if ok {
		_ = drawCenterPercentImage(base, pct)
		return base, nil
	}
	_ = drawBallTextImage(base, ok)
	return base, nil
}

func drawCenterPercentImage(img *image.RGBA, pct float64) error {
	return applyCachedTextMask(
		img,
		"percent:"+centerPercentText(pct),
		centerPercentText(pct),
		"Microsoft YaHei UI",
		8,
		walk.FontBold,
		color.RGBA{30, 58, 66, 235},
		centerPercentRect(),
		walk.TextCenter|walk.TextVCenter|walk.TextSingleLine,
	)
}

func centerPercentText(pct float64) string {
	pct = math.Max(0, math.Min(100, pct))
	return strconv.Itoa(int(math.Round(pct))) + "%"
}

func centerPercentRect() walk.Rectangle {
	cx, cy := ballCenter(ballRect())
	return walk.Rectangle{X: int(math.Round(cx)) - 20, Y: int(math.Round(cy)) - 9, Width: 40, Height: 18}
}

func toRGBA(src image.Image) *image.RGBA {
	if rgba, ok := src.(*image.RGBA); ok {
		clone := image.NewRGBA(rgba.Bounds())
		copy(clone.Pix, rgba.Pix)
		return clone
	}
	b := src.Bounds()
	dst := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dst.Set(x, y, src.At(x, y))
		}
	}
	return dst
}

func drawBallTextImage(img *image.RGBA, ok bool) error {
	if !ok {
		return applyCachedTextMask(
			img,
			"title:refreshing",
			"Krill AI\n刷新中",
			"Microsoft YaHei UI",
			13,
			walk.FontBold,
			color.RGBA{32, 76, 88, 230},
			walk.Rectangle{X: 35, Y: 72, Width: 120, Height: 52},
			walk.TextCenter|walk.TextVCenter|walk.TextWordbreak,
		)
	}
	return nil
}

var (
	textMaskCacheMu sync.Mutex
	textMaskCache   = make(map[string]*image.RGBA)
)

func applyCachedTextMask(dst *image.RGBA, key, text, family string, pointSize int, style walk.FontStyle, c color.RGBA, rect walk.Rectangle, format walk.DrawTextFormat) error {
	mask, err := cachedTextMask(key, text, family, pointSize, style, rect, format)
	if err != nil {
		return err
	}
	applyTextMask(dst, mask, c)
	return nil
}

func cachedTextMask(key, text, family string, pointSize int, style walk.FontStyle, rect walk.Rectangle, format walk.DrawTextFormat) (*image.RGBA, error) {
	textMaskCacheMu.Lock()
	if mask := textMaskCache[key]; mask != nil {
		textMaskCacheMu.Unlock()
		return mask, nil
	}
	textMaskCacheMu.Unlock()

	font, err := walk.NewFont(family, pointSize, style)
	if err != nil {
		return nil, err
	}
	defer font.Dispose()
	mask, err := textMask(text, font, rect, format)
	if err != nil {
		return nil, err
	}

	textMaskCacheMu.Lock()
	if existing := textMaskCache[key]; existing != nil {
		textMaskCacheMu.Unlock()
		return existing, nil
	}
	textMaskCache[key] = mask
	textMaskCacheMu.Unlock()
	return mask, nil
}

func applyTextMask(dst *image.RGBA, mask *image.RGBA, c color.RGBA) {
	for y := 0; y < ballSize; y++ {
		for x := 0; x < ballSize; x++ {
			m := mask.RGBAAt(x, y)
			alpha := maxByte(m.R, maxByte(m.G, m.B))
			if alpha == 0 {
				continue
			}
			src := c
			src.A = uint8(uint16(c.A) * uint16(alpha) / 255)
			sourceOverPixel(dst, x, y, src, 1)
		}
	}
}

func textMask(text string, font *walk.Font, rect walk.Rectangle, format walk.DrawTextFormat) (*image.RGBA, error) {
	bg := image.NewRGBA(image.Rect(0, 0, ballSize, ballSize))
	fillImageRGBA(bg, color.RGBA{0, 0, 0, 255})

	bmp, err := walk.NewBitmapFromImage(bg)
	if err != nil {
		return nil, err
	}
	defer bmp.Dispose()

	canvas, err := walk.NewCanvasFromImage(bmp)
	if err != nil {
		return nil, err
	}

	err = canvas.DrawTextPixels(text, font, walk.RGB(255, 255, 255), rect, format)
	canvas.Dispose()
	if err != nil {
		return nil, err
	}
	return bmp.ToImage()
}

func maxByte(a, b byte) byte {
	if a > b {
		return a
	}
	return b
}

func clearDetachedBottomTail(img *image.RGBA, r ballFloatRect) {
	startY := int(math.Ceil(r.bottom)) + 2
	for y := startY; y < ballSize; y++ {
		for x := 0; x < ballSize; x++ {
			img.SetRGBA(x, y, color.RGBA{})
		}
	}
}

func clearOutsideSphere(img *image.RGBA, r ballFloatRect, margin float64) {
	cx, cy, rad := r.cx(), r.cy(), r.rad()
	feather := math.Max(1.25, margin*1.9)
	for y := 0; y < ballSize; y++ {
		for x := 0; x < ballSize; x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			if d > rad+feather {
				img.SetRGBA(x, y, color.RGBA{})
				continue
			}
			if d > rad-feather {
				c := img.RGBAAt(x, y)
				scale := clamp01((rad + feather - d) / (2 * feather))
				img.SetRGBA(x, y, scalePremultiplied(c, scale))
			}
		}
	}
}

func scalePremultiplied(c color.RGBA, scale float64) color.RGBA {
	scale = clamp01(scale)
	return color.RGBA{
		R: uint8(float64(c.R)*scale + 0.5),
		G: uint8(float64(c.G)*scale + 0.5),
		B: uint8(float64(c.B)*scale + 0.5),
		A: uint8(float64(c.A)*scale + 0.5),
	}
}

func premultipliedBGRA(src image.Image) []byte {
	bounds := src.Bounds()
	buf := make([]byte, bounds.Dx()*bounds.Dy()*4)
	i := 0
	if rgba, ok := src.(*image.RGBA); ok {
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				off := rgba.PixOffset(x, y)
				buf[i+0] = rgba.Pix[off+2]
				buf[i+1] = rgba.Pix[off+1]
				buf[i+2] = rgba.Pix[off+0]
				buf[i+3] = rgba.Pix[off+3]
				i += 4
			}
		}
		return buf
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := src.At(x, y).RGBA()
			buf[i+0] = byte(b >> 8)
			buf[i+1] = byte(g >> 8)
			buf[i+2] = byte(r >> 8)
			buf[i+3] = byte(a >> 8)
			i += 4
		}
	}
	return buf
}

func updateLayeredWindowImage(hwnd win.HWND, img image.Image) error {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil
	}

	pixels := premultipliedBGRA(img)
	hdcScreen := win.GetDC(0)
	if hdcScreen == 0 {
		return syscall.EINVAL
	}
	defer win.ReleaseDC(0, hdcScreen)

	hdcMem := win.CreateCompatibleDC(hdcScreen)
	if hdcMem == 0 {
		return syscall.EINVAL
	}
	defer win.DeleteDC(hdcMem)

	var hdr win.BITMAPINFOHEADER
	hdr.BiSize = uint32(unsafe.Sizeof(hdr))
	hdr.BiWidth = int32(width)
	hdr.BiHeight = -int32(height)
	hdr.BiPlanes = 1
	hdr.BiBitCount = 32
	hdr.BiCompression = win.BI_RGB
	hdr.BiSizeImage = uint32(len(pixels))

	var bitsPtr unsafe.Pointer
	hBmp := win.CreateDIBSection(hdcScreen, &hdr, win.DIB_RGB_COLORS, &bitsPtr, 0, 0)
	if hBmp == 0 || bitsPtr == nil {
		return syscall.EINVAL
	}
	defer win.DeleteObject(win.HGDIOBJ(hBmp))

	bits := unsafe.Slice((*byte)(bitsPtr), len(pixels))
	copy(bits, pixels)

	old := win.SelectObject(hdcMem, win.HGDIOBJ(hBmp))
	if old == 0 {
		return syscall.EINVAL
	}
	defer win.SelectObject(hdcMem, old)

	var rect win.RECT
	if !win.GetWindowRect(hwnd, &rect) {
		return syscall.EINVAL
	}

	dst := win.POINT{X: rect.Left, Y: rect.Top}
	size := win.SIZE{CX: int32(width), CY: int32(height)}
	src := win.POINT{}
	blend := win.BLENDFUNCTION{
		BlendOp:             0,
		SourceConstantAlpha: 255,
		AlphaFormat:         win.AC_SRC_ALPHA,
	}

	ret, _, err := procUpdateLayeredWindow.Call(
		uintptr(hwnd),
		uintptr(hdcScreen),
		uintptr(unsafe.Pointer(&dst)),
		uintptr(unsafe.Pointer(&size)),
		uintptr(hdcMem),
		uintptr(unsafe.Pointer(&src)),
		0,
		uintptr(unsafe.Pointer(&blend)),
		uintptr(0x00000002),
	)
	if ret == 0 {
		if err != syscall.Errno(0) {
			return err
		}
		return syscall.EINVAL
	}
	return nil
}

type ballFloatRect struct {
	left, top, right, bottom float64
}

func ballRect() ballFloatRect {
	return ballFloatRect{left: 7.5, top: 6.5, right: 182.5, bottom: 181.5}
}

func (r ballFloatRect) cx() float64 { return (r.left + r.right) / 2 }
func (r ballFloatRect) cy() float64 { return (r.top + r.bottom) / 2 }
func (r ballFloatRect) w() float64  { return r.right - r.left }
func (r ballFloatRect) h() float64  { return r.bottom - r.top }
func (r ballFloatRect) rad() float64 {
	return math.Min(r.w(), r.h()) / 2
}

func transparentKeyColor() color.RGBA {
	return color.RGBA{}
}

func normalizedGlassMetric(mode string) string {
	if mode == "forwarded" {
		return "forwarded"
	}
	return "daily"
}

func ballCenter(r ballFloatRect) (float64, float64) {
	return r.cx(), r.cy()
}

func fillImageRGBA(img *image.RGBA, c color.RGBA) {
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func drawEmptyBall(img *image.RGBA, r ballFloatRect) {
	cx, cy, rad := r.cx(), r.cy(), r.rad()
	fx, fy := cx-36, cy-52
	for y := 0; y < ballSize; y++ {
		for x := 0; x < ballSize; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			if math.Hypot(px-cx, py-cy) > rad {
				continue
			}
			t := clamp01(math.Hypot(px-fx, py-fy) / (r.w() * 0.62))
			col := multiStopColor(t,
				colorStop{0.0, color.RGBA{255, 255, 255, 130}},
				colorStop{0.56, color.RGBA{229, 235, 233, 92}},
				colorStop{1.0, color.RGBA{178, 194, 198, 128}},
			)
			sourceOverPixel(img, x, y, col, 1)
		}
	}
	drawGlassHighlights(img, r)
}

func drawSphereShadow(img *image.RGBA, r ballFloatRect) {
	cx, cy, rad := r.cx(), r.cy(), r.rad()
	for y := 0; y < ballSize; y++ {
		for x := 0; x < ballSize; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			d := math.Hypot(px-cx, py-cy)
			if d > rad {
				continue
			}
			bottom := clamp01((py - cy) / (rad * 0.92))
			right := clamp01((px - cx) / (rad * 0.95))
			edge := clamp01((d/rad - 0.70) / 0.30)
			sourceOverPixel(img, x, y, color.RGBA{36, 45, 54, 50}, bottom*edge)
			sourceOverPixel(img, x, y, color.RGBA{80, 210, 232, 34}, right*bottom)
		}
	}
}

func drawBackGlass(img *image.RGBA, r ballFloatRect) {
	cx, cy, rad := r.cx(), r.cy(), r.rad()
	for y := 0; y < ballSize; y++ {
		for x := 0; x < ballSize; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			dc := math.Hypot(px-cx, py-cy)
			if dc > rad {
				continue
			}
			vertical := clamp01((py - r.top) / r.h())
			col := multiStopColor(vertical,
				colorStop{0.0, color.RGBA{255, 255, 255, 72}},
				colorStop{0.35, color.RGBA{244, 244, 241, 46}},
				colorStop{0.68, color.RGBA{226, 232, 231, 48}},
				colorStop{1.0, color.RGBA{204, 219, 222, 78}},
			)
			sourceOverPixel(img, x, y, col, 1)

			edge := clamp01((dc/rad - 0.78) / 0.22)
			if edge > 0 {
				sourceOverPixel(img, x, y, color.RGBA{54, 61, 70, 92}, edge)
				sourceOverPixel(img, x, y, color.RGBA{255, 255, 255, 56}, edge*(1-vertical))
			}
		}
	}
}

func drawWater(img *image.RGBA, r ballFloatRect, pct, phase float64) {
	pct = math.Max(0, math.Min(100, pct))
	cx, cy, rad := r.cx(), r.cy(), r.rad()
	waterY := waterBaseY(r, pct)
	for y := 0; y < ballSize; y++ {
		for x := 0; x < ballSize; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			if math.Hypot(px-cx, py-cy) > rad {
				continue
			}
			surface := waterSurfaceY(r, px, waterY, phase, pct)
			if py <= surface {
				continue
			}
			depth := clamp01((py - surface) / math.Max(1, r.bottom-surface))
			col := verticalWater(depth)
			sourceOverPixel(img, x, y, col, clamp01((py-surface+1.6)/3.2))

			rightT := clamp01(math.Hypot(px-(cx+62), py-(cy+49)) / 72)
			sourceOverPixel(img, x, y, multiStopColor(rightT,
				colorStop{0.0, color.RGBA{238, 255, 255, 152}},
				colorStop{0.40, color.RGBA{130, 242, 255, 92}},
				colorStop{0.78, color.RGBA{0, 178, 222, 20}},
				colorStop{1.0, color.RGBA{0, 0, 0, 0}},
			), 1)

			leftT := clamp01(math.Hypot(px-(cx-72), py-(cy+34)) / 88)
			sourceOverPixel(img, x, y, multiStopColor(leftT,
				colorStop{0.0, color.RGBA{0, 92, 135, 46}},
				colorStop{0.58, color.RGBA{0, 166, 205, 22}},
				colorStop{1.0, color.RGBA{0, 0, 0, 0}},
			), 1)

			bottomT := clamp01(math.Hypot(px-(cx-8), py-(cy+74)) / 86)
			sourceOverPixel(img, x, y, multiStopColor(bottomT,
				colorStop{0.0, color.RGBA{255, 255, 246, 138}},
				colorStop{0.46, color.RGBA{170, 250, 248, 80}},
				colorStop{1.0, color.RGBA{0, 32, 84, 0}},
			), 1)
		}
	}
	drawWaterSurface(img, r, waterY, phase, pct)
	drawWaterArcs(img, r, pct, phase)
	drawBubbles(img, r, pct, phase)
}

func waterBaseY(r ballFloatRect, pct float64) float64 {
	return r.bottom - r.h()*clamp01(pct/100)
}

func waterSurfaceY(r ballFloatRect, x, baseY, phase, pct float64) float64 {
	u := clamp01((x - r.left) / r.w())
	centerLift := -8.2 * math.Sin(math.Pi*u)
	rightSag := 2.6 * (u - 0.46)
	motion := math.Sin(u*math.Pi*2.0+phase*0.82)*1.35 + math.Sin(u*math.Pi*1.1-phase*0.56)*0.82
	return baseY + centerLift + rightSag + motion
}

func ballWave(x, phase, pct float64) float64 {
	amp := 1.2 + math.Max(0, 100-pct)*0.006
	return math.Sin(x*0.035+phase*0.82)*amp + math.Sin(x*0.018-phase*0.56)*0.7
}

func drawWaveLine(img *image.RGBA, r ballFloatRect, waterY, phase, pct float64, c color.RGBA, width int) {
	cx, cy, rad := r.cx(), r.cy(), r.rad()
	for x := int(r.left); x <= int(r.right); x++ {
		y := int(math.Round(waterSurfaceY(r, float64(x), waterY, phase, pct)))
		for dy := -width; dy <= width; dy++ {
			yy := y + dy
			if yy < 0 || yy >= ballSize {
				continue
			}
			if math.Hypot(float64(x)+0.5-cx, float64(yy)+0.5-cy) <= rad {
				blendPixel(img, x, yy, c, 0.74)
			}
		}
	}
}

func drawWaterSurface(img *image.RGBA, r ballFloatRect, waterY, phase, pct float64) {
	drawWaveLine(img, r, waterY, phase, pct, color.RGBA{235, 255, 255, 196}, 2)
	drawWaveLine(img, r, waterY+2.6, phase, pct, color.RGBA{0, 141, 184, 78}, 1)
	drawWaveLine(img, r, waterY+12, phase+1.8, pct, color.RGBA{202, 255, 255, 62}, 1)
}

func drawWaterArcs(img *image.RGBA, r ballFloatRect, pct, phase float64) {
	waterY := waterBaseY(r, pct)
	drawArcRect(img, r.left+22, waterY+8, r.w()+14, 58, 196, 118, color.RGBA{230, 255, 255, 76}, 1.2)
	drawArcRect(img, r.left+34, waterY+25, r.w()-12, 72, 198, 110, color.RGBA{19, 151, 187, 38}, 1.0)
}

func drawBubbles(img *image.RGBA, r ballFloatRect, pct, phase float64) {
	if pct < 5 {
		return
	}
	waterY := waterBaseY(r, pct)
	seeds := []struct {
		px, py, rad, speed float64
	}{
		{0.30, 0.74, 4.2, 0.42},
		{0.48, 0.68, 5.3, 0.31},
		{0.60, 0.79, 3.8, 0.38},
		{0.72, 0.86, 2.7, 0.46},
		{0.83, 0.69, 2.9, 0.28},
		{0.55, 0.88, 2.2, 0.52},
	}
	for _, s := range seeds {
		x := r.left + s.px*r.w() + math.Sin(phase*s.speed+s.px*7)*2.7
		y := r.top + s.py*r.h() - math.Sin(phase*s.speed+s.py*8)*3.8
		surface := waterSurfaceY(r, x, waterY, phase, pct)
		if y <= surface+5 {
			y = surface + 11 + s.rad
		}
		drawEllipseOutline(img, x, y, s.rad, s.rad, color.RGBA{232, 255, 255, 152}, 1.2, 1.0)
		drawEllipseOutline(img, x-0.7, y-0.7, s.rad*0.62, s.rad*0.62, color.RGBA{255, 255, 255, 74}, 0.8, 1.0)
	}
}

func drawEquatorBand(img *image.RGBA, r ballFloatRect) {
	cx, cy, rad := r.cx(), r.cy(), r.rad()
	bandTop := cy - 13.5
	bandBottom := cy + 13.5
	for y := int(bandTop); y <= int(bandBottom); y++ {
		for x := int(r.left); x <= int(r.right); x++ {
			if x < 0 || x >= ballSize || y < 0 || y >= ballSize {
				continue
			}
			if math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) > rad {
				continue
			}
			t := clamp01((float64(y) - bandTop) / (bandBottom - bandTop))
			midLight := clamp01(1 - math.Abs(float64(x)+0.5-cx)/(rad*0.72))
			col := multiStopColor(t,
				colorStop{0.0, color.RGBA{50, 68, 88, 232}},
				colorStop{0.46, color.RGBA{20, 33, 51, 244}},
				colorStop{1.0, color.RGBA{41, 56, 76, 232}},
			)
			sourceOverPixel(img, x, y, col, 1)
			sourceOverPixel(img, x, y, color.RGBA{95, 135, 154, 42}, midLight)
			if math.Abs(float64(y)-cy) <= 2 {
				sourceOverPixel(img, x, y, color.RGBA{8, 19, 35, 64}, 1)
			}
		}
	}
	for _, line := range []struct {
		y float64
		c color.RGBA
	}{
		{bandTop, color.RGBA{165, 190, 202, 82}},
		{bandBottom, color.RGBA{8, 17, 31, 84}},
	} {
		yy := int(math.Round(line.y))
		for x := int(r.left + 6); x <= int(r.right-6); x++ {
			if math.Hypot(float64(x)+0.5-cx, float64(yy)+0.5-cy) <= rad {
				sourceOverPixel(img, x, yy, line.c, 1)
			}
		}
	}
}

func drawFrontGlass(img *image.RGBA, r ballFloatRect) {
	cx, cy, rad := r.cx(), r.cy(), r.rad()
	for y := int(r.top); y <= int(cy+10); y++ {
		for x := int(r.left); x <= int(r.right); x++ {
			if x < 0 || x >= ballSize || y < 0 || y >= ballSize {
				continue
			}
			px, py := float64(x)+0.5, float64(y)+0.5
			if math.Hypot(px-cx, py-cy) > rad {
				continue
			}
			t := clamp01((py - r.top) / (cy + 10 - r.top))
			col := multiStopColor(t,
				colorStop{0.0, color.RGBA{255, 255, 255, 98}},
				colorStop{0.48, color.RGBA{244, 245, 242, 54}},
				colorStop{1.0, color.RGBA{235, 240, 239, 8}},
			)
			sourceOverPixel(img, x, y, col, 1)
		}
	}
	for y := int(r.top); y <= int(r.bottom); y++ {
		for x := int(r.left); x <= int(r.right); x++ {
			if x < 0 || x >= ballSize || y < 0 || y >= ballSize {
				continue
			}
			px, py := float64(x)+0.5, float64(y)+0.5
			d := math.Hypot(px-cx, py-cy) / rad
			if d > 1 {
				continue
			}
			edge := clamp01((d - 0.83) / 0.17)
			lower := clamp01((py - cy) / (rad * 0.86))
			right := clamp01((px - cx) / (rad * 0.82))
			sourceOverPixel(img, x, y, color.RGBA{34, 42, 52, 92}, edge*(0.34+lower*0.42))
			sourceOverPixel(img, x, y, color.RGBA{222, 255, 255, 108}, edge*right*(0.28+lower*0.72))
		}
	}
	drawGlassHighlights(img, r)
}

func drawGlassHighlights(img *image.RGBA, r ballFloatRect) {
	cx, cy := r.cx(), r.cy()
	drawArcRect(img, r.left+34, r.top+8, r.w()-68, 48, 194, 150, color.RGBA{255, 255, 255, 170}, 1.8)
	drawArcRect(img, r.left+44, r.top+18, r.w()-86, 50, 197, 138, color.RGBA{255, 255, 255, 78}, 1.1)
	drawArcRect(img, r.left+14, r.top+12, r.w()-28, r.h()-24, 104, 102, color.RGBA{45, 53, 63, 54}, 1.1)
	drawRadialEllipse(img, cx-48, cy-48, 17, 15, cx-52, cy-50, 28,
		colorStop{0.0, color.RGBA{255, 255, 255, 202}},
		colorStop{0.38, color.RGBA{255, 255, 255, 88}},
		colorStop{1.0, color.RGBA{255, 255, 255, 0}},
	)
	drawRadialEllipse(img, cx+61, cy+47, 13, 36, cx+66, cy+66, 44,
		colorStop{0.0, color.RGBA{232, 255, 255, 126}},
		colorStop{0.48, color.RGBA{145, 242, 255, 70}},
		colorStop{1.0, color.RGBA{255, 255, 255, 0}},
	)
}

func drawReferenceRim(img *image.RGBA, r ballFloatRect) {
	cx, cy, rad := r.cx(), r.cy(), r.rad()
	drawEllipseOutline(img, cx, cy, rad+0.1, rad+0.1, color.RGBA{42, 47, 56, 112}, 1.2, 1.0)
	drawEllipseOutline(img, cx, cy, rad-1.3, rad-1.3, color.RGBA{255, 255, 255, 106}, 1.0, 1.0)
	drawEllipseOutline(img, cx, cy, rad-4.0, rad-4.0, color.RGBA{78, 96, 108, 36}, 1.0, 1.0)
}

type colorStop struct {
	at  float64
	col color.RGBA
}

func multiStopColor(t float64, stops ...colorStop) color.RGBA {
	t = clamp01(t)
	if len(stops) == 0 {
		return color.RGBA{}
	}
	if t <= stops[0].at {
		return stops[0].col
	}
	for i := 1; i < len(stops); i++ {
		if t <= stops[i].at {
			span := stops[i].at - stops[i-1].at
			if span <= 0 {
				return stops[i].col
			}
			return mix(stops[i-1].col, stops[i].col, (t-stops[i-1].at)/span)
		}
	}
	return stops[len(stops)-1].col
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func blendPixel(img *image.RGBA, x, y int, c color.RGBA, a float64) {
	sourceOverPixel(img, x, y, c, a)
}

func sourceOverPixel(img *image.RGBA, x, y int, c color.RGBA, alphaScale float64) {
	if x < 0 || y < 0 || x >= ballSize || y >= ballSize {
		return
	}
	srcA := clamp01(float64(c.A) / 255 * alphaScale)
	if srcA <= 0 {
		return
	}
	base := img.RGBAAt(x, y)
	dstA := float64(base.A) / 255
	dstR, dstG, dstB := 0.0, 0.0, 0.0
	if base.A > 0 {
		dstR = float64(base.R) / dstA
		dstG = float64(base.G) / dstA
		dstB = float64(base.B) / dstA
	}
	outA := srcA + dstA*(1-srcA)
	if outA <= 0 {
		img.SetRGBA(x, y, color.RGBA{})
		return
	}
	outR := (float64(c.R)*srcA + dstR*dstA*(1-srcA)) / outA
	outG := (float64(c.G)*srcA + dstG*dstA*(1-srcA)) / outA
	outB := (float64(c.B)*srcA + dstB*dstA*(1-srcA)) / outA
	img.SetRGBA(x, y, color.RGBA{
		R: uint8(outR*outA + 0.5),
		G: uint8(outG*outA + 0.5),
		B: uint8(outB*outA + 0.5),
		A: uint8(outA*255 + 0.5),
	})
}

func drawEllipseOutline(img *image.RGBA, cx, cy, rx, ry float64, c color.RGBA, width, alpha float64) {
	if rx <= 0 || ry <= 0 {
		return
	}
	for y := int(cy - ry - width - 1); y <= int(cy+ry+width+1); y++ {
		for x := int(cx - rx - width - 1); x <= int(cx+rx+width+1); x++ {
			if x < 0 || y < 0 || x >= ballSize || y >= ballSize {
				continue
			}
			px, py := float64(x)+0.5, float64(y)+0.5
			n := math.Hypot((px-cx)/rx, (py-cy)/ry)
			dist := math.Abs(n - 1)
			threshold := width / math.Min(rx, ry)
			if dist <= threshold {
				blendPixel(img, x, y, c, alpha*(1-dist/threshold))
			}
		}
	}
}

func drawArc(img *image.RGBA, cx, cy, rx, ry, startDeg, spanDeg float64, c color.RGBA, width float64) {
	endDeg := startDeg + spanDeg
	for y := int(cy - ry - width - 1); y <= int(cy+ry+width+1); y++ {
		for x := int(cx - rx - width - 1); x <= int(cx+rx+width+1); x++ {
			if x < 0 || y < 0 || x >= ballSize || y >= ballSize {
				continue
			}
			px, py := float64(x)+0.5, float64(y)+0.5
			n := math.Hypot((px-cx)/rx, (py-cy)/ry)
			dist := math.Abs(n - 1)
			threshold := width / math.Min(rx, ry)
			if dist > threshold {
				continue
			}
			ang := math.Atan2(cy-py, px-cx) * 180 / math.Pi
			if ang < 0 {
				ang += 360
			}
			if !angleInArc(ang, startDeg, endDeg) {
				continue
			}
			blendPixel(img, x, y, c, 1-dist/threshold)
		}
	}
}

func drawArcRect(img *image.RGBA, left, top, width, height, startDeg, spanDeg float64, c color.RGBA, penWidth float64) {
	drawArc(img, left+width/2, top+height/2, width/2, height/2, startDeg, spanDeg, c, penWidth)
}

func drawRadialEllipse(img *image.RGBA, cx, cy, rx, ry, fx, fy, radius float64, stops ...colorStop) {
	if rx <= 0 || ry <= 0 || radius <= 0 {
		return
	}
	for y := int(cy - ry - 1); y <= int(cy+ry+1); y++ {
		for x := int(cx - rx - 1); x <= int(cx+rx+1); x++ {
			if x < 0 || y < 0 || x >= ballSize || y >= ballSize {
				continue
			}
			px, py := float64(x)+0.5, float64(y)+0.5
			if math.Hypot((px-cx)/rx, (py-cy)/ry) > 1 {
				continue
			}
			t := clamp01(math.Hypot(px-fx, py-fy) / radius)
			sourceOverPixel(img, x, y, multiStopColor(t, stops...), 1)
		}
	}
}

func angleInArc(angle, start, end float64) bool {
	start = math.Mod(start+360, 360)
	end = math.Mod(end+360, 360)
	if end >= start {
		return angle >= start && angle <= end
	}
	return angle >= start || angle <= end
}

func verticalWater(depth float64) color.RGBA {
	t := clamp01(depth)
	return multiStopColor(t,
		colorStop{0.0, color.RGBA{0, 158, 205, 214}},
		colorStop{0.40, color.RGBA{38, 203, 228, 230}},
		colorStop{0.72, color.RGBA{102, 230, 238, 238}},
		colorStop{1.0, color.RGBA{198, 255, 246, 246}},
	)
}

func drawCenter(img *image.RGBA, pct float64) {
	cx, cy := ballCenter(ballRect())
	pct = math.Max(0, math.Min(100, pct))
	for y := 0; y < ballSize; y++ {
		for x := 0; x < ballSize; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			d := math.Hypot(px-cx, py-cy)
			if cov := circleCoverage(d, 37, 1.15); cov > 0 {
				t := clamp01(math.Hypot(px-(cx-10), py-(cy-12)) / 50)
				c := multiStopColor(t,
					colorStop{0.0, color.RGBA{103, 130, 136, 255}},
					colorStop{0.52, color.RGBA{70, 94, 103, 255}},
					colorStop{1.0, color.RGBA{30, 42, 52, 255}},
				)
				sourceOverPixel(img, x, y, c, cov)
			}
			if cov := annulusCoverage(d, 24, 31, 1.0); cov > 0 {
				progress := ringProgressFromTopClockwise(px, py, cx, cy)
				if pct > 0 && (pct >= 100 || progress <= pct/100) {
					sourceOverPixel(img, x, y, color.RGBA{31, 181, 239, 255}, cov)
				} else {
					sourceOverPixel(img, x, y, color.RGBA{126, 209, 245, 255}, cov)
				}
			}
			if cov := annulusCoverage(d, 20, 23.5, 0.85); cov > 0 {
				sourceOverPixel(img, x, y, color.RGBA{224, 240, 242, 255}, cov)
			}
			if cov := circleCoverage(d, 19.2, 0.95); cov > 0 {
				t := clamp01(math.Hypot(px-(cx-7), py-(cy-9)) / 31)
				c := multiStopColor(t,
					colorStop{0.0, color.RGBA{250, 251, 248, 255}},
					colorStop{0.58, color.RGBA{221, 224, 220, 255}},
					colorStop{1.0, color.RGBA{164, 169, 168, 255}},
				)
				sourceOverPixel(img, x, y, c, cov)
			}
		}
	}
	drawArc(img, cx, cy, 35.5, 35.5, 198, 118, color.RGBA{255, 255, 255, 184}, 1.8)
	drawArc(img, cx, cy, 35.5, 35.5, 332, 72, color.RGBA{8, 22, 36, 166}, 1.8)
	drawArc(img, cx, cy, 27.4, 27.4, 207, 80, color.RGBA{208, 245, 255, 132}, 1.1)
	drawEllipseOutline(img, cx, cy, 19.2, 19.2, color.RGBA{112, 119, 121, 138}, 1.0, 1.0)
	drawRadialEllipse(img, cx-17, cy-26, 11, 8, cx-19, cy-27, 18,
		colorStop{0.0, color.RGBA{255, 255, 255, 150}},
		colorStop{0.58, color.RGBA{255, 255, 255, 42}},
		colorStop{1.0, color.RGBA{255, 255, 255, 0}},
	)
}

func ringProgressFromTopClockwise(x, y, cx, cy float64) float64 {
	angle := math.Atan2(x-cx, cy-y)
	if angle < 0 {
		angle += 2 * math.Pi
	}
	return angle / (2 * math.Pi)
}

func circleCoverage(d, radius, aa float64) float64 {
	if aa <= 0 {
		if d <= radius {
			return 1
		}
		return 0
	}
	return clamp01((radius + aa - d) / (2 * aa))
}

func annulusCoverage(d, inner, outer, aa float64) float64 {
	outerCov := circleCoverage(d, outer, aa)
	innerCov := 1 - circleCoverage(d, inner, aa)
	return clamp01(outerCov * innerCov)
}

func drawEllipseRing(img *image.RGBA, cx, cy, r float64, c color.RGBA, width float64) {
	for y := 0; y < ballSize; y++ {
		for x := 0; x < ballSize; x++ {
			d := math.Abs(math.Hypot(float64(x)-cx, float64(y)-cy) - r)
			if d <= width {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func fillRoundRect(img *image.RGBA, x1, y1, x2, y2, radius int, c color.RGBA) {
	for y := y1; y < y2; y++ {
		for x := x1; x < x2; x++ {
			dx := maxInt(maxInt(x1+radius-x, 0), maxInt(x-(x2-radius), 0))
			dy := maxInt(maxInt(y1+radius-y, 0), maxInt(y-(y2-radius), 0))
			if dx*dx+dy*dy <= radius*radius {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func fillRect(img *image.RGBA, x1, y1, x2, y2 int, c color.RGBA) {
	for y := y1; y < y2; y++ {
		for x := x1; x < x2; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func mix(a, b color.RGBA, t float64) color.RGBA {
	t = math.Max(0, math.Min(1, t))
	return color.RGBA{
		R: uint8(float64(a.R)*(1-t) + float64(b.R)*t),
		G: uint8(float64(a.G)*(1-t) + float64(b.G)*t),
		B: uint8(float64(a.B)*(1-t) + float64(b.B)*t),
		A: uint8(float64(a.A)*(1-t) + float64(b.A)*t),
	}
}

func blend(a, b color.RGBA, t float64) color.RGBA {
	return mix(a, b, t)
}

func addLight(c color.RGBA, v float64) color.RGBA {
	return color.RGBA{
		R: uint8(math.Min(255, float64(c.R)+v)),
		G: uint8(math.Min(255, float64(c.G)+v)),
		B: uint8(math.Min(255, float64(c.B)+v)),
		A: c.A,
	}
}

func hitCenter(x, y int) bool {
	cx, cy := ballCenter(ballRect())
	dx, dy := float64(x)-cx, float64(y)-cy
	return dx*dx+dy*dy <= 34*34
}

func ballDragDistanceExceeded(dx, dy int) bool {
	return dx*dx+dy*dy > ballDragThreshold*ballDragThreshold
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func applyLayered(hwnd win.HWND) {
	ex := win.GetWindowLong(hwnd, win.GWL_EXSTYLE)
	if ex&int32(win.WS_EX_LAYERED) != 0 {
		return
	}
	win.SetWindowLong(hwnd, win.GWL_EXSTYLE, ex|int32(win.WS_EX_LAYERED))
	win.SetWindowPos(hwnd, 0, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_NOACTIVATE|win.SWP_FRAMECHANGED)
}
