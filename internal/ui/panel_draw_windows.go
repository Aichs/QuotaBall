//go:build windows && legacywalk

package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"unicode/utf8"

	"github.com/lxn/walk"
	"github.com/lxn/win"

	"krill_monitor/internal/krill"
)

const (
	panelWidth  = 540
	panelHeight = 820
)

var panelKeyColor = color.RGBA{245, 255, 251, 255}

type panelButton struct {
	name string
	rect walk.Rectangle
}

type panelSubLayout struct {
	sub  krill.Subscription
	rect walk.Rectangle
}

type panelLayout struct {
	buttons       []panelButton
	errRect       walk.Rectangle
	cards         [4]walk.Rectangle
	sectionTitle  walk.Rectangle
	subCount      walk.Rectangle
	scrollRect    walk.Rectangle
	subCards      []panelSubLayout
	contentHeight int
}

type panelFonts struct {
	logo, title, subtitle, muted, section, button, auth, err *walk.Font
	cardTitle, cardValue, cardSub, subName, badge, route     *walk.Font
}

func newPanelFonts() (*panelFonts, error) {
	makeFont := func(family string, size int, style walk.FontStyle) (*walk.Font, error) {
		return walk.NewFont(family, size, style)
	}
	f := &panelFonts{}
	var err error
	if f.logo, err = makeFont("Segoe UI", 22, walk.FontBold); err != nil {
		return nil, err
	}
	if f.title, err = makeFont("Segoe UI", 18, walk.FontBold); err != nil {
		return nil, err
	}
	if f.subtitle, err = makeFont("Microsoft YaHei UI", 9, 0); err != nil {
		return nil, err
	}
	if f.muted, err = makeFont("Microsoft YaHei UI", 9, 0); err != nil {
		return nil, err
	}
	if f.section, err = makeFont("Microsoft YaHei UI", 13, walk.FontBold); err != nil {
		return nil, err
	}
	if f.button, err = makeFont("Segoe UI Symbol", 12, walk.FontBold); err != nil {
		return nil, err
	}
	if f.auth, err = makeFont("Microsoft YaHei UI", 9, walk.FontBold); err != nil {
		return nil, err
	}
	if f.err, err = makeFont("Microsoft YaHei UI", 9, 0); err != nil {
		return nil, err
	}
	if f.cardTitle, err = makeFont("Microsoft YaHei UI", 9, 0); err != nil {
		return nil, err
	}
	if f.cardValue, err = makeFont("Cascadia Code", 18, walk.FontBold); err != nil {
		f.cardValue, err = makeFont("Consolas", 18, walk.FontBold)
		if err != nil {
			return nil, err
		}
	}
	if f.cardSub, err = makeFont("Microsoft YaHei UI", 8, 0); err != nil {
		return nil, err
	}
	if f.subName, err = makeFont("Microsoft YaHei UI", 11, walk.FontBold); err != nil {
		return nil, err
	}
	if f.badge, err = makeFont("Microsoft YaHei UI", 8, 0); err != nil {
		return nil, err
	}
	if f.route, err = makeFont("Microsoft YaHei UI", 7, 0); err != nil {
		return nil, err
	}
	return f, nil
}

func (f *panelFonts) dispose() {
	for _, font := range []*walk.Font{
		f.logo, f.title, f.subtitle, f.muted, f.section, f.button, f.auth, f.err,
		f.cardTitle, f.cardValue, f.cardSub, f.subName, f.badge, f.route,
	} {
		if font != nil {
			font.Dispose()
		}
	}
}

func (a *app) paintPanel(canvas *walk.Canvas, _ walk.Rectangle) error {
	a.mu.Lock()
	s := a.snap
	a.mu.Unlock()

	layout := a.buildPanelLayout(s)
	maxScroll := maxInt(0, layout.contentHeight-layout.scrollRect.Height)
	if a.panelScrollOffset > maxScroll {
		a.panelScrollOffset = maxScroll
		layout = a.buildPanelLayout(s)
	}
	if a.panelScrollOffset < 0 {
		a.panelScrollOffset = 0
		layout = a.buildPanelLayout(s)
	}
	a.panelButtons = layout.buttons
	a.panelContentHeight = layout.contentHeight

	bg := renderPanelImage(s, layout, a.panelScrollOffset)
	bmp, err := walk.NewBitmapFromImage(bg)
	if err != nil {
		return err
	}
	defer bmp.Dispose()
	if err := canvas.DrawImagePixels(bmp, walk.Point{}); err != nil {
		return err
	}
	return a.drawPanelText(canvas, s, layout)
}

func (a *app) buildPanelLayout(s krill.Snapshot) panelLayout {
	const marginX = 20
	y := 16
	layout := panelLayout{
		buttons: []panelButton{
			{name: "settings", rect: walk.Rectangle{X: 346, Y: y + 2, Width: 28, Height: 28}},
			{name: "refresh", rect: walk.Rectangle{X: 384, Y: y + 2, Width: 28, Height: 28}},
			{name: "hide", rect: walk.Rectangle{X: 422, Y: y + 2, Width: 28, Height: 28}},
			{name: "auth", rect: walk.Rectangle{X: 456, Y: y, Width: 66, Height: 30}},
		},
	}
	y += 42
	if s.Err != "" {
		errH := 32
		if utf8.RuneCountInString(s.Err) > 70 {
			errH = 48
		}
		layout.errRect = walk.Rectangle{X: marginX, Y: y, Width: panelWidth - marginX*2, Height: errH}
		y += errH + 10
	}

	cardW := (panelWidth - marginX*2 - 8) / 2
	cardH := 92
	layout.cards[0] = walk.Rectangle{X: marginX, Y: y, Width: cardW, Height: cardH}
	layout.cards[1] = walk.Rectangle{X: marginX + cardW + 8, Y: y, Width: cardW, Height: cardH}
	layout.cards[2] = walk.Rectangle{X: marginX, Y: y + cardH + 8, Width: cardW, Height: cardH}
	layout.cards[3] = walk.Rectangle{X: marginX + cardW + 8, Y: y + cardH + 8, Width: cardW, Height: cardH}
	y += cardH*2 + 8 + 14

	layout.sectionTitle = walk.Rectangle{X: marginX + 2, Y: y, Width: 52, Height: 24}
	layout.subCount = walk.Rectangle{X: marginX + 58, Y: y + 2, Width: 100, Height: 22}
	y += 34

	layout.scrollRect = walk.Rectangle{X: marginX, Y: y, Width: panelWidth - marginX*2, Height: panelHeight - y - 18}

	contentY := 0
	for _, sub := range s.Subscriptions {
		h := 132
		if len(sub.Routes) > 0 {
			h = 158
		}
		layout.subCards = append(layout.subCards, panelSubLayout{
			sub: sub,
			rect: walk.Rectangle{
				X:      layout.scrollRect.X,
				Y:      layout.scrollRect.Y + contentY - a.panelScrollOffset,
				Width:  layout.scrollRect.Width - 10,
				Height: h,
			},
		})
		contentY += h + 10
	}
	if contentY > 0 {
		contentY -= 10
	}
	layout.contentHeight = contentY
	return layout
}

func renderPanelImage(s krill.Snapshot, layout panelLayout, scrollOffset int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, panelWidth, panelHeight))
	fillImage(img, panelKeyColor)
	panelFillRoundedGradient(img, walk.Rectangle{X: 0, Y: 0, Width: panelWidth, Height: panelHeight}, 22,
		color.RGBA{255, 255, 255, 255},
		color.RGBA{236, 253, 255, 255},
		color.RGBA{184, 236, 248, 255},
		color.RGBA{98, 192, 221, 255})
	panelAddShellEffects(img)

	for _, b := range layout.buttons {
		if b.name == "auth" {
			continue
		}
		panelFillRoundedSolid(img, b.rect, 14, color.RGBA{232, 252, 255, 255})
		panelStrokeRounded(img, b.rect, 14, color.RGBA{137, 217, 232, 255}, 1)
	}

	if layout.errRect.Height > 0 {
		panelFillRoundedSolid(img, layout.errRect, 8, color.RGBA{255, 247, 247, 255})
		panelStrokeRounded(img, layout.errRect, 8, color.RGBA{238, 166, 166, 255}, 1)
	}

	for _, card := range layout.cards {
		panelFillRoundedGradient(img, card, 12,
			color.RGBA{255, 255, 255, 255},
			color.RGBA{241, 253, 255, 255},
			color.RGBA{228, 249, 253, 255},
			color.RGBA{199, 239, 247, 255})
		panelStrokeRounded(img, card, 12, color.RGBA{96, 199, 226, 255}, 1)
	}

	clip := layout.scrollRect
	for _, sub := range layout.subCards {
		if sub.rect.Y+sub.rect.Height < clip.Y || sub.rect.Y > clip.Y+clip.Height {
			continue
		}
		panelFillRoundedGradientClipped(img, sub.rect, 12, clip,
			color.RGBA{255, 255, 255, 255},
			color.RGBA{249, 254, 255, 255},
			color.RGBA{232, 250, 253, 255},
			color.RGBA{202, 241, 249, 255})
		panelStrokeRoundedClipped(img, sub.rect, 12, clip, color.RGBA{105, 199, 223, 255}, 1)
		panelDrawQuotaBars(img, sub.rect, sub.sub, clip)
		panelDrawRouteTags(img, sub.rect, sub.sub, clip)
		panelDrawBadge(img, sub.rect, sub.sub, clip)
	}

	if layout.contentHeight > layout.scrollRect.Height {
		drawPanelScrollbar(img, layout.scrollRect, layout.contentHeight, scrollOffset)
	}
	return img
}

func (a *app) drawPanelText(canvas *walk.Canvas, s krill.Snapshot, layout panelLayout) error {
	fonts, err := newPanelFonts()
	if err != nil {
		return err
	}
	defer fonts.dispose()

	draw := func(text string, font *walk.Font, col walk.Color, rect walk.Rectangle, format walk.DrawTextFormat) {
		_ = canvas.DrawTextPixels(text, font, col, rect, format)
	}

	draw("◒", fonts.logo, walk.RGB(8, 191, 215), walk.Rectangle{X: 22, Y: 17, Width: 32, Height: 32}, walk.TextCenter|walk.TextVCenter|walk.TextSingleLine)
	draw("Krill", fonts.title, walk.RGB(7, 29, 45), walk.Rectangle{X: 60, Y: 14, Width: 105, Height: 26}, walk.TextLeft|walk.TextVCenter|walk.TextSingleLine)
	draw("额度监控", fonts.subtitle, walk.RGB(58, 96, 112), walk.Rectangle{X: 61, Y: 39, Width: 90, Height: 20}, walk.TextLeft|walk.TextVCenter|walk.TextSingleLine)
	draw(formatTime(s.Time), fonts.muted, walk.RGB(73, 110, 124), walk.Rectangle{X: 156, Y: 27, Width: 70, Height: 18}, walk.TextLeft|walk.TextVCenter|walk.TextSingleLine)

	for _, b := range layout.buttons {
		switch b.name {
		case "settings":
			draw("⚙", fonts.button, walk.RGB(28, 102, 130), b.rect, walk.TextCenter|walk.TextVCenter|walk.TextSingleLine)
		case "refresh":
			draw("↻", fonts.button, walk.RGB(28, 102, 130), b.rect, walk.TextCenter|walk.TextVCenter|walk.TextSingleLine)
		case "hide":
			draw("—", fonts.button, walk.RGB(28, 102, 130), b.rect, walk.TextCenter|walk.TextVCenter|walk.TextSingleLine)
		case "auth":
			text := "登录"
			if s.LoggedIn {
				text = "退出登录"
			}
			draw(text, fonts.auth, walk.RGB(236, 95, 72), b.rect, walk.TextCenter|walk.TextVCenter|walk.TextSingleLine)
		}
	}

	if layout.errRect.Height > 0 {
		draw(s.Err, fonts.err, walk.RGB(217, 72, 72), insetRect(layout.errRect, 8, 5), walk.TextLeft|walk.TextVCenter|walk.TextWordbreak)
	}

	tq := s.Summary.TotalDailyQuotaUSD
	fr := s.Summary.TotalForwardedRemainingUSD
	values := []struct {
		title  string
		value  string
		sub    string
		accent walk.Color
	}{
		{"今日花费", money(s.Spend, 2), fmt.Sprintf("转结 %s · 剩余 %s", money(fr, 2), money(math.Max(0, tq-s.Spend), 2)), walk.RGB(255, 173, 47)},
		{"钱包余额", money(s.Wallet, 2), walletSubText(s.Wallet), walk.RGB(40, 184, 255)},
		{"日额度", money(tq, 0), fmt.Sprintf("已用 %s / 总计 %s", money(s.Spend, 2), money(tq, 0)), walk.RGB(49, 223, 154)},
		{"缓存率", nonEmpty(s.Cache, "-"), "缓存命中 / 请求数", walk.RGB(155, 124, 255)},
	}
	for i, item := range values {
		r := layout.cards[i]
		draw(item.title, fonts.cardTitle, walk.RGB(52, 89, 105), walk.Rectangle{X: r.X + 12, Y: r.Y + 9, Width: r.Width - 24, Height: 18}, walk.TextLeft|walk.TextVCenter|walk.TextSingleLine)
		draw(item.value, fonts.cardValue, item.accent, walk.Rectangle{X: r.X + 12, Y: r.Y + 29, Width: r.Width - 24, Height: 30}, walk.TextLeft|walk.TextVCenter|walk.TextSingleLine)
		draw(item.sub, fonts.cardSub, walk.RGB(54, 89, 105), walk.Rectangle{X: r.X + 12, Y: r.Y + 62, Width: r.Width - 24, Height: 28}, walk.TextLeft|walk.TextTop|walk.TextWordbreak)
	}

	draw("套餐", fonts.section, walk.RGB(9, 36, 55), layout.sectionTitle, walk.TextLeft|walk.TextVCenter|walk.TextSingleLine)
	draw(fmt.Sprintf("%d 张", len(s.Subscriptions)), fonts.muted, walk.RGB(73, 110, 124), layout.subCount, walk.TextLeft|walk.TextVCenter|walk.TextSingleLine)

	for _, sub := range layout.subCards {
		if sub.rect.Y+sub.rect.Height < layout.scrollRect.Y || sub.rect.Y > layout.scrollRect.Y+layout.scrollRect.Height {
			continue
		}
		drawSubscriptionText(canvas, fonts, sub, layout.scrollRect)
	}
	return nil
}

func drawSubscriptionText(canvas *walk.Canvas, fonts *panelFonts, item panelSubLayout, clip walk.Rectangle) {
	draw := func(text string, font *walk.Font, col walk.Color, rect walk.Rectangle, format walk.DrawTextFormat) {
		if rect.Y+rect.Height < clip.Y || rect.Y > clip.Y+clip.Height {
			return
		}
		_ = canvas.DrawTextPixels(text, font, col, rect, format)
	}
	r := item.rect
	sub := item.sub
	draw(nonEmpty(sub.Name, "套餐"), fonts.subName, walk.RGB(11, 38, 56), walk.Rectangle{X: r.X + 12, Y: r.Y + 9, Width: r.Width - 130, Height: 22}, walk.TextLeft|walk.TextVCenter|walk.TextSingleLine|walk.TextEndEllipsis)
	draw(daysText(sub.DaysLeft), fonts.badge, walk.RGB(8, 112, 86), walk.Rectangle{X: r.X + r.Width - 116, Y: r.Y + 10, Width: 100, Height: 20}, walk.TextCenter|walk.TextVCenter|walk.TextSingleLine)
	draw(fmt.Sprintf("#%s  ·  %s → %s", sub.ID, sub.Start, sub.End), fonts.muted, walk.RGB(73, 110, 124), walk.Rectangle{X: r.X + 12, Y: r.Y + 35, Width: r.Width - 24, Height: 18}, walk.TextLeft|walk.TextVCenter|walk.TextSingleLine|walk.TextEndEllipsis)
	y := r.Y + 58
	if len(sub.Routes) > 0 {
		x := r.X + 12
		for i, route := range sub.Routes {
			if i >= 6 {
				break
			}
			w := minInt(110, 16+utf8.RuneCountInString(route)*8)
			draw(route, fonts.route, walk.RGB(35, 86, 107), walk.Rectangle{X: x, Y: y, Width: w, Height: 20}, walk.TextCenter|walk.TextVCenter|walk.TextSingleLine|walk.TextEndEllipsis)
			x += w + 5
		}
		y += 25
	}
	drawQuotaText(canvas, fonts, r.X+12, y, r.Width-24, "转结", sub.ForwardedUsed, sub.ForwardedLimit, clip)
	drawQuotaText(canvas, fonts, r.X+12, y+38, r.Width-24, "当日", sub.DailyUsed, sub.DailyLimit, clip)
}

func drawQuotaText(canvas *walk.Canvas, fonts *panelFonts, x, y, w int, title string, used, limit float64, clip walk.Rectangle) {
	if y+30 < clip.Y || y > clip.Y+clip.Height {
		return
	}
	_ = canvas.DrawTextPixels(title, fonts.muted, walk.RGB(73, 110, 124), walk.Rectangle{X: x, Y: y, Width: 60, Height: 18}, walk.TextLeft|walk.TextVCenter|walk.TextSingleLine)
	_ = canvas.DrawTextPixels(fmt.Sprintf("%s / %s", money(used, 2), money(limit, 2)), fonts.muted, walk.RGB(73, 110, 124), walk.Rectangle{X: x + 68, Y: y, Width: w - 68, Height: 18}, walk.TextRight|walk.TextVCenter|walk.TextSingleLine)
}

func (a *app) panelMouseDown(x, y int, button walk.MouseButton) {
	if button != walk.LeftButton {
		return
	}
	a.panelPressedButton = hitPanelButton(a.panelButtons, x, y)
	pt := cursorPoint()
	a.panelDragStart = &pt
	b := a.mw.Bounds()
	a.panelWinStart = walk.Point{X: b.X, Y: b.Y}
	a.panelDragging = false
}

func (a *app) panelMouseMove(x, y int, button walk.MouseButton) {
	if button != walk.LeftButton || a.panelDragStart == nil || a.panelPressedButton != "" {
		return
	}
	pt := cursorPoint()
	dx := pt.X - a.panelDragStart.X
	dy := pt.Y - a.panelDragStart.Y
	if abs(dx)+abs(dy) > 3 {
		a.panelDragging = true
	}
	if a.panelDragging {
		_ = a.mw.SetBounds(walk.Rectangle{X: a.panelWinStart.X + dx, Y: a.panelWinStart.Y + dy, Width: panelWidth, Height: panelHeight})
	}
}

func (a *app) panelMouseUp(x, y int, button walk.MouseButton) {
	if button != walk.LeftButton {
		return
	}
	pressed := a.panelPressedButton
	a.panelPressedButton = ""
	a.panelDragStart = nil
	if pressed != "" {
		if hitPanelButton(a.panelButtons, x, y) == pressed {
			a.handlePanelButton(pressed)
		}
		return
	}
	if a.panelDragging {
		a.saveMainPosition()
	}
	a.panelDragging = false
}

func (a *app) panelMouseWheel(_ int, _ int, button walk.MouseButton) {
	delta := walk.MouseWheelEventDelta(button)
	if delta == 0 || a.panelContentHeight <= 0 {
		return
	}
	maxScroll := maxInt(0, a.panelContentHeight-a.buildPanelLayout(a.snap).scrollRect.Height)
	step := 42
	a.panelScrollOffset = clampInt(a.panelScrollOffset-(delta/120)*step, 0, maxScroll)
	a.invalidatePanel()
}

func (a *app) handlePanelButton(name string) {
	switch name {
	case "settings":
		a.showSettings()
	case "refresh":
		a.refresh(true)
	case "hide":
		a.hidePanel()
	case "auth":
		a.authAction()
	}
}

func (a *app) invalidatePanel() {
	if a.panelCanvas != nil && !a.panelCanvas.IsDisposed() {
		_ = a.panelCanvas.Invalidate()
	}
}

func hitPanelButton(buttons []panelButton, x, y int) string {
	for _, b := range buttons {
		if containsPoint(b.rect, x, y) {
			return b.name
		}
	}
	return ""
}

func cursorPoint() walk.Point {
	var pt win.POINT
	if win.GetCursorPos(&pt) {
		return walk.Point{X: int(pt.X), Y: int(pt.Y)}
	}
	return walk.Point{}
}

func setLayeredColorKeyAlpha(hwnd win.HWND, key uint32, alpha byte) {
	procSetLayeredWindowAttrs.Call(uintptr(hwnd), uintptr(key), uintptr(alpha), uintptr(0x00000001|0x00000002))
}

func fillImage(img *image.RGBA, c color.RGBA) {
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func panelFillRoundedGradient(img *image.RGBA, r walk.Rectangle, radius int, c0, c1, c2, c3 color.RGBA) {
	panelFillRoundedGradientClipped(img, r, radius, r, c0, c1, c2, c3)
}

func panelFillRoundedGradientClipped(img *image.RGBA, r walk.Rectangle, radius int, clip walk.Rectangle, c0, c1, c2, c3 color.RGBA) {
	ir := intersectRect(r, clip)
	for y := ir.Y; y < ir.Y+ir.Height; y++ {
		for x := ir.X; x < ir.X+ir.Width; x++ {
			if !insideRounded(r, radius, x, y) {
				continue
			}
			tx := float64(x-r.X) / float64(maxInt(1, r.Width))
			ty := float64(y-r.Y) / float64(maxInt(1, r.Height))
			t := (tx + ty) / 2
			col := multiStop(c0, c1, c2, c3, t)
			img.SetRGBA(x, y, col)
		}
	}
}

func panelFillRoundedSolid(img *image.RGBA, r walk.Rectangle, radius int, c color.RGBA) {
	panelFillRoundedSolidClipped(img, r, radius, r, c)
}

func panelFillRoundedSolidClipped(img *image.RGBA, r walk.Rectangle, radius int, clip walk.Rectangle, c color.RGBA) {
	ir := intersectRect(r, clip)
	for y := ir.Y; y < ir.Y+ir.Height; y++ {
		for x := ir.X; x < ir.X+ir.Width; x++ {
			if insideRounded(r, radius, x, y) {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func panelStrokeRounded(img *image.RGBA, r walk.Rectangle, radius int, c color.RGBA, width int) {
	panelStrokeRoundedClipped(img, r, radius, r, c, width)
}

func panelStrokeRoundedClipped(img *image.RGBA, r walk.Rectangle, radius int, clip walk.Rectangle, c color.RGBA, width int) {
	ir := intersectRect(r, clip)
	inner := walk.Rectangle{X: r.X + width, Y: r.Y + width, Width: r.Width - width*2, Height: r.Height - width*2}
	for y := ir.Y; y < ir.Y+ir.Height; y++ {
		for x := ir.X; x < ir.X+ir.Width; x++ {
			if insideRounded(r, radius, x, y) && !insideRounded(inner, maxInt(0, radius-width), x, y) {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func panelAddShellEffects(img *image.RGBA) {
	r := walk.Rectangle{X: 0, Y: 0, Width: panelWidth, Height: panelHeight}
	for y := 0; y < panelHeight; y++ {
		for x := 0; x < panelWidth; x++ {
			if !insideRounded(r, 22, x, y) {
				continue
			}
			col := img.RGBAAt(x, y)
			topGlow := 1 - math.Hypot(float64(x-96), float64(y-20))/420
			if topGlow > 0 {
				col = mix(col, color.RGBA{255, 255, 255, 255}, topGlow*0.28)
			}
			depth := 1 - math.Hypot(float64(x-panelWidth+18), float64(y-panelHeight+18))/360
			if depth > 0 {
				col = mix(col, color.RGBA{0, 82, 112, 255}, depth*0.18)
			}
			if x > panelWidth-140 {
				col = mix(col, color.RGBA{12, 124, 162, 255}, float64(x-(panelWidth-140))/140*0.07)
			}
			img.SetRGBA(x, y, col)
		}
	}
	sheen := walk.Rectangle{X: 14, Y: 12, Width: panelWidth - 28, Height: 150}
	for y := sheen.Y; y < sheen.Y+sheen.Height; y++ {
		for x := sheen.X; x < sheen.X+sheen.Width; x++ {
			if insideRounded(sheen, 14, x, y) && insideRounded(r, 22, x, y) {
				t := 1 - float64(y-sheen.Y)/float64(sheen.Height)
				img.SetRGBA(x, y, mix(img.RGBAAt(x, y), color.RGBA{255, 255, 255, 255}, t*0.17))
			}
		}
	}
	panelStrokeRounded(img, r, 22, color.RGBA{255, 255, 255, 255}, 1)
	panelStrokeRounded(img, walk.Rectangle{X: 2, Y: 2, Width: panelWidth - 4, Height: panelHeight - 4}, 20, color.RGBA{62, 191, 224, 255}, 1)
}

func panelDrawQuotaBars(img *image.RGBA, r walk.Rectangle, sub krill.Subscription, clip walk.Rectangle) {
	y := r.Y + 83
	if len(sub.Routes) > 0 {
		y += 25
	}
	panelQuotaBar(img, r.X+12, y+20, r.Width-24, sub.ForwardedPercent, clip)
	panelQuotaBar(img, r.X+12, y+58, r.Width-24, sub.DailyPercent, clip)
}

func panelQuotaBar(img *image.RGBA, x, y, w int, pct float64, clip walk.Rectangle) {
	track := walk.Rectangle{X: x, Y: y, Width: w, Height: 7}
	panelFillRoundedSolidClipped(img, track, 4, clip, color.RGBA{219, 235, 241, 255})
	fillW := int(float64(w) * math.Max(0, math.Min(100, pct)) / 100)
	if fillW > 0 {
		panelFillRoundedGradientClipped(img, walk.Rectangle{X: x, Y: y, Width: fillW, Height: 7}, 4, clip,
			color.RGBA{14, 165, 255, 255}, color.RGBA{35, 200, 255, 255}, color.RGBA{67, 229, 255, 255}, color.RGBA{83, 242, 255, 255})
	}
}

func panelDrawRouteTags(img *image.RGBA, r walk.Rectangle, sub krill.Subscription, clip walk.Rectangle) {
	if len(sub.Routes) == 0 {
		return
	}
	x := r.X + 12
	y := r.Y + 58
	for i, route := range sub.Routes {
		if i >= 6 {
			break
		}
		w := minInt(110, 16+utf8.RuneCountInString(route)*8)
		panelFillRoundedSolidClipped(img, walk.Rectangle{X: x, Y: y, Width: w, Height: 20}, 6, clip, color.RGBA{222, 248, 252, 255})
		panelStrokeRoundedClipped(img, walk.Rectangle{X: x, Y: y, Width: w, Height: 20}, 6, clip, color.RGBA{128, 210, 224, 255}, 1)
		x += w + 5
	}
}

func panelDrawBadge(img *image.RGBA, r walk.Rectangle, _ krill.Subscription, clip walk.Rectangle) {
	badge := walk.Rectangle{X: r.X + r.Width - 116, Y: r.Y + 10, Width: 100, Height: 20}
	panelFillRoundedSolidClipped(img, badge, 8, clip, color.RGBA{198, 255, 231, 255})
}

func drawPanelScrollbar(img *image.RGBA, r walk.Rectangle, contentHeight, offset int) {
	track := walk.Rectangle{X: r.X + r.Width - 8, Y: r.Y + 2, Width: 8, Height: r.Height - 4}
	panelFillRoundedSolid(img, track, 4, color.RGBA{238, 252, 254, 255})
	thumbH := maxInt(32, int(float64(track.Height)*float64(r.Height)/float64(contentHeight)))
	maxScroll := maxInt(1, contentHeight-r.Height)
	thumbY := track.Y + int(float64(track.Height-thumbH)*float64(offset)/float64(maxScroll))
	panelFillRoundedSolid(img, walk.Rectangle{X: track.X, Y: thumbY, Width: track.Width, Height: thumbH}, 4, color.RGBA{77, 183, 209, 255})
}

func insideRounded(r walk.Rectangle, radius, x, y int) bool {
	if r.Width <= 0 || r.Height <= 0 {
		return false
	}
	if x < r.X || y < r.Y || x >= r.X+r.Width || y >= r.Y+r.Height {
		return false
	}
	rr := minInt(radius, minInt(r.Width, r.Height)/2)
	cx := x
	cy := y
	if x < r.X+rr {
		cx = r.X + rr
	} else if x >= r.X+r.Width-rr {
		cx = r.X + r.Width - rr - 1
	}
	if y < r.Y+rr {
		cy = r.Y + rr
	} else if y >= r.Y+r.Height-rr {
		cy = r.Y + r.Height - rr - 1
	}
	dx := x - cx
	dy := y - cy
	return dx*dx+dy*dy <= rr*rr
}

func multiStop(c0, c1, c2, c3 color.RGBA, t float64) color.RGBA {
	if t < 0.30 {
		return mix(c0, c1, t/0.30)
	}
	if t < 0.66 {
		return mix(c1, c2, (t-0.30)/0.36)
	}
	return mix(c2, c3, (t-0.66)/0.34)
}

func intersectRect(a, b walk.Rectangle) walk.Rectangle {
	x1 := maxInt(a.X, b.X)
	y1 := maxInt(a.Y, b.Y)
	x2 := minInt(a.X+a.Width, b.X+b.Width)
	y2 := minInt(a.Y+a.Height, b.Y+b.Height)
	if x2 <= x1 || y2 <= y1 {
		return walk.Rectangle{}
	}
	return walk.Rectangle{X: x1, Y: y1, Width: x2 - x1, Height: y2 - y1}
}

func containsPoint(r walk.Rectangle, x, y int) bool {
	return x >= r.X && y >= r.Y && x < r.X+r.Width && y < r.Y+r.Height
}

func insetRect(r walk.Rectangle, x, y int) walk.Rectangle {
	return walk.Rectangle{X: r.X + x, Y: r.Y + y, Width: r.Width - x*2, Height: r.Height - y*2}
}

func walletSubText(wallet float64) string {
	if wallet == 0 {
		return "额度用完自动消耗"
	}
	return "信用 + 福利"
}

func nonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func daysText(v any) string {
	if i, ok := v.(int); ok {
		return fmt.Sprintf("%d 天后到期", i)
	}
	s := fmt.Sprintf("%v", v)
	if s == "" || s == "<nil>" {
		return ""
	}
	return s
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
