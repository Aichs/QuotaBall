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

	"quotaball/internal/config"
	"quotaball/internal/krill"
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

func TestClampBallBoundsToScreenKeepsRestoredBallVisible(t *testing.T) {
	screen := walk.Rectangle{X: 0, Y: 0, Width: 2560, Height: 1440}
	got, adjusted := clampBallBoundsToScreen(walk.Rectangle{X: 2854, Y: 792, Width: ballSize, Height: ballSize}, screen)
	want := walk.Rectangle{X: 96, Y: 96, Width: ballSize, Height: ballSize}
	if !adjusted || got != want {
		t.Fatalf("clamped bounds = %+v adjusted=%v, want %+v adjusted=true", got, adjusted, want)
	}
}

func TestClampBallBoundsToScreenSupportsNegativeVirtualScreen(t *testing.T) {
	screen := walk.Rectangle{X: -1920, Y: 0, Width: 4480, Height: 1440}
	got, adjusted := clampBallBoundsToScreen(walk.Rectangle{X: -2300, Y: -40, Width: ballSize, Height: ballSize}, screen)
	want := walk.Rectangle{X: -1824, Y: 96, Width: ballSize, Height: ballSize}
	if !adjusted || got != want {
		t.Fatalf("clamped bounds = %+v adjusted=%v, want %+v adjusted=true", got, adjusted, want)
	}
}

func TestClampBallBoundsToScreenHandlesScreenSmallerThanBall(t *testing.T) {
	screen := walk.Rectangle{X: 10, Y: 20, Width: 120, Height: 90}
	got, adjusted := clampBallBoundsToScreen(walk.Rectangle{X: 400, Y: 500, Width: ballSize, Height: ballSize}, screen)
	want := walk.Rectangle{X: 10, Y: 20, Width: ballSize, Height: ballSize}
	if !adjusted || got != want {
		t.Fatalf("clamped bounds = %+v adjusted=%v, want %+v adjusted=true", got, adjusted, want)
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

func TestNormalizedGlassMetricAllowsWeeklyAndMonthly(t *testing.T) {
	if got := normalizedGlassMetric("daily"); got != "daily" {
		t.Fatalf("normalizedGlassMetric(daily) = %q", got)
	}
	if got := normalizedGlassMetric("weekly"); got != "weekly" {
		t.Fatalf("normalizedGlassMetric(weekly) = %q", got)
	}
	if got := normalizedGlassMetric("monthly"); got != "monthly" {
		t.Fatalf("normalizedGlassMetric(monthly) = %q", got)
	}
	if got := normalizedGlassMetricForProvider(config.ProviderKrill, "daily"); got != "weekly" {
		t.Fatalf("Krill daily metric normalized to %q, want weekly", got)
	}
}

func TestGlassBallToggleMetricCyclesWeeklyMonthlyAndPersists(t *testing.T) {
	host := &fakeGlassBallHost{cfg: config.Default()}
	g := &glassBall{host: host, mode: "weekly"}

	g.toggleMetric()
	if g.mode != "monthly" || host.cfg.TbarMetric != "monthly" {
		t.Fatalf("first toggle mode=%q config=%q, want monthly", g.mode, host.cfg.TbarMetric)
	}

	g.toggleMetric()
	if g.mode != "weekly" || host.cfg.TbarMetric != "weekly" {
		t.Fatalf("second toggle mode=%q config=%q, want weekly", g.mode, host.cfg.TbarMetric)
	}
}

func TestGlassBallSub2ToggleMetricCyclesDailyWeeklyMonthlyAndPersists(t *testing.T) {
	host := &fakeGlassBallHost{cfg: config.Default()}
	host.cfg.Provider = config.ProviderSub2
	host.cfg.TbarMetric = "daily"
	g := &glassBall{
		host: host,
		mode: "daily",
		snap: krill.Snapshot{Provider: config.ProviderSub2, OK: true},
	}

	g.toggleMetric()
	if g.mode != "weekly" || host.cfg.TbarMetric != "weekly" {
		t.Fatalf("first Sub2 toggle mode=%q config=%q, want weekly", g.mode, host.cfg.TbarMetric)
	}

	g.toggleMetric()
	if g.mode != "monthly" || host.cfg.TbarMetric != "monthly" {
		t.Fatalf("second Sub2 toggle mode=%q config=%q, want monthly", g.mode, host.cfg.TbarMetric)
	}

	g.toggleMetric()
	if g.mode != "daily" || host.cfg.TbarMetric != "daily" {
		t.Fatalf("third Sub2 toggle mode=%q config=%q, want daily", g.mode, host.cfg.TbarMetric)
	}
}

func TestGlassBallMetricUsesSelectedMonthlyQuota(t *testing.T) {
	g := &glassBall{
		mode: "monthly",
		snap: krill.Snapshot{
			OK: true,
			Subscriptions: []krill.Subscription{{
				WeeklyLimit:      600,
				WeeklyUsed:       300,
				WeeklyRemaining:  300,
				WeeklyPercent:    50,
				MonthlyLimit:     2400,
				MonthlyUsed:      600,
				MonthlyRemaining: 1800,
				MonthlyPercent:   25,
			}},
		},
	}

	label, pct, used, limit, ok := g.metric()
	if !ok {
		t.Fatal("metric returned not ok")
	}
	if label != "月总额度" || pct != 25 || used != 600 || limit != 2400 {
		t.Fatalf("monthly metric = (%q, %v, %v, %v), want monthly quota fields", label, pct, used, limit)
	}
}

func TestGlassBallActiveSubPrefersCurrentQuotaWindow(t *testing.T) {
	snap := krill.Snapshot{
		OK: true,
		Subscriptions: []krill.Subscription{{
			ID:               "stale",
			WeeklyLimit:      600,
			WeeklyUsed:       400,
			WeeklyRemaining:  200,
			MonthlyLimit:     2400,
			MonthlyUsed:      400,
			MonthlyRemaining: 2000,
		}, {
			ID:               "current",
			CurrentWindow:    true,
			WeeklyLimit:      600,
			WeeklyUsed:       20,
			WeeklyRemaining:  580,
			MonthlyLimit:     2400,
			MonthlyUsed:      20,
			MonthlyRemaining: 2380,
		}},
	}

	sub, ok := activeSubFromSnapshot(snap)
	if !ok || sub.ID != "current" {
		t.Fatalf("active sub = %#v ok=%v, want current quota window", sub, ok)
	}
}

func TestGlassBallSub2MetricUsesSelectedDailyWeeklyMonthlyQuota(t *testing.T) {
	sub := krill.Subscription{
		DailyLimit:       100,
		DailyUsed:        25,
		DailyRemaining:   75,
		DailyPercent:     25,
		WeeklyLimit:      700,
		WeeklyUsed:       140,
		WeeklyRemaining:  560,
		WeeklyPercent:    20,
		MonthlyLimit:     3000,
		MonthlyUsed:      1500,
		MonthlyRemaining: 1500,
		MonthlyPercent:   50,
	}
	for _, tc := range []struct {
		mode  string
		label string
		pct   float64
		used  float64
		limit float64
	}{
		{"daily", "每日额度", 25, 25, 100},
		{"weekly", "每周额度", 20, 140, 700},
		{"monthly", "每月额度", 50, 1500, 3000},
	} {
		g := &glassBall{
			mode: tc.mode,
			snap: krill.Snapshot{
				Provider:      config.ProviderSub2,
				OK:            true,
				Subscriptions: []krill.Subscription{sub},
			},
		}
		label, pct, used, limit, ok := g.metric()
		if !ok {
			t.Fatalf("%s metric returned not ok", tc.mode)
		}
		if label != tc.label || pct != tc.pct || used != tc.used || limit != tc.limit {
			t.Fatalf("%s metric = (%q, %v, %v, %v), want (%q, %v, %v, %v)", tc.mode, label, pct, used, limit, tc.label, tc.pct, tc.used, tc.limit)
		}
	}
}

func TestGlassBallNewAPIMetricIsFullBalanceAndNotToggleable(t *testing.T) {
	host := &fakeGlassBallHost{cfg: config.Default()}
	host.cfg.TbarMetric = "monthly"
	g := &glassBall{
		host: host,
		mode: "monthly",
		snap: krill.Snapshot{
			Provider: "newapi",
			OK:       true,
			Wallet:   1.8,
			Spend:    0.2,
			Req:      "618",
		},
	}

	state := g.metricState()
	if !state.ok {
		t.Fatal("metricState returned not ok")
	}
	if state.label != "当前余额" || state.pct != 100 || state.centerText != "$1.80" || state.mode != "weekly" || state.togglable {
		t.Fatalf("NewAPI metricState = %+v, want full blue balance metric without toggle", state)
	}

	g.toggleMetric()
	if g.mode != "monthly" || host.cfg.TbarMetric != "monthly" {
		t.Fatalf("NewAPI toggle should be disabled, mode=%q config=%q", g.mode, host.cfg.TbarMetric)
	}
}

func TestGlassBallMenuToggleActionFollowsMetricState(t *testing.T) {
	raw, err := os.ReadFile("glassball_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []string{
		"metricAction",
		"g.metricAction = act",
		"func (g *glassBall) syncMetricAction()",
		"SetVisible(enabled)",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("glass ball menu toggle action must be hidden when metric is not togglable; missing %q", want)
		}
	}
}

func TestNewAPIFrameUsesFullBlueWaterAndBalanceText(t *testing.T) {
	base := toRGBA(renderBallImageWithMode(100, true, 0, "weekly"))
	frame, err := renderBallFrameImageWithCenterText(100, true, 0, "weekly", "$1.80")
	if err != nil {
		t.Fatal(err)
	}
	water := frame.RGBAAt(95, 44)
	if water.B <= water.R || water.G <= water.R {
		t.Fatalf("NewAPI full water should keep the blue weekly palette at top sample: %+v", water)
	}
	changed := 0
	for y := 84; y <= 102; y++ {
		for x := 74; x <= 116; x++ {
			a := base.RGBAAt(x, y)
			b := frame.RGBAAt(x, y)
			if abs(int(a.R)-int(b.R))+abs(int(a.G)-int(b.G))+abs(int(a.B)-int(b.B))+abs(int(a.A)-int(b.A)) > 18 {
				changed++
			}
		}
	}
	if changed < 12 {
		t.Fatalf("NewAPI frame did not draw center balance text: changed=%d", changed)
	}
}

func TestGlassBallMonthlyRenderingUsesWarmWaterPalette(t *testing.T) {
	weekly, err := renderBallFrameImage(34, true, 0, "weekly")
	if err != nil {
		t.Fatal(err)
	}
	monthly, err := renderBallFrameImage(34, true, 0, "monthly")
	if err != nil {
		t.Fatal(err)
	}
	w := weekly.RGBAAt(95, 150)
	m := monthly.RGBAAt(95, 150)
	if m.R <= m.G || m.R <= m.B {
		t.Fatalf("monthly water should be warm red at sample pixel: weekly=%+v monthly=%+v", w, m)
	}
	if int(m.R)-int(w.R) < 45 || int(w.B)-int(m.B) < 35 {
		t.Fatalf("monthly water is not visually distinct from weekly cyan: weekly=%+v monthly=%+v", w, m)
	}
}

func TestGlassBallDailyRenderingUsesBlueWaterPalette(t *testing.T) {
	daily, err := renderBallFrameImage(34, true, 0, "daily")
	if err != nil {
		t.Fatal(err)
	}
	water := daily.RGBAAt(95, 150)
	if water.B <= water.R || water.G <= water.R {
		t.Fatalf("daily water should be blue at sample pixel: %+v", water)
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

func TestLoadingFrameUsesReferenceBallWithTurbineWater(t *testing.T) {
	a, err := renderLoadingBallFrameImage(0, "登录中")
	if err != nil {
		t.Fatal(err)
	}
	b, err := renderLoadingBallFrameImage(ballAnimationStep*8, "登录中")
	if err != nil {
		t.Fatal(err)
	}
	topWater := a.RGBAAt(95, 30)
	if topWater.B <= topWater.R || topWater.G <= topWater.R {
		t.Fatalf("loading turbine water should fill the ball with the blue glass palette near the top: %+v", topWater)
	}
	water := a.RGBAAt(95, 54)
	if water.B <= water.R || water.G <= water.R {
		t.Fatalf("loading turbine water should keep the blue glass palette near the upper body: %+v", water)
	}
	band := a.RGBAAt(28, 94)
	if band.A < 200 || band.R > 90 || band.G > 110 || band.B > 140 {
		t.Fatalf("loading ball should keep the reference dark center band: %+v", band)
	}
	changed := 0
	for y := 42; y <= 148; y++ {
		for x := 42; x <= 148; x++ {
			c0 := a.RGBAAt(x, y)
			c1 := b.RGBAAt(x, y)
			if abs(int(c0.R)-int(c1.R))+abs(int(c0.G)-int(c1.G))+abs(int(c0.B)-int(c1.B))+abs(int(c0.A)-int(c1.A)) > 18 {
				changed++
			}
		}
	}
	if changed < 220 {
		t.Fatalf("loading turbine water barely changes across phases: changed=%d", changed)
	}
}

func TestGlassBallDoesNotIncludeLongPressFanMenu(t *testing.T) {
	raw, err := os.ReadFile("glassball_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, forbidden := range []string{
		"fanMenu",
		"LongPress",
		"renderBallFrameImageWithFanMenu",
		"drawFrostedMenuButton",
	} {
		if strings.Contains(src, forbidden) {
			t.Fatalf("glass ball should not include long-press fan menu code; found %q", forbidden)
		}
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

type fakeGlassBallHost struct {
	cfg config.Config
}

func (h *fakeGlassBallHost) loadGlassConfig() config.Config {
	return h.cfg
}

func (h *fakeGlassBallHost) mutateGlassConfig(fn func(*config.Config)) {
	fn(&h.cfg)
}

func (h *fakeGlassBallHost) togglePanel() {}
func (h *fakeGlassBallHost) refresh(bool) {}
func (h *fakeGlassBallHost) quit()        {}
