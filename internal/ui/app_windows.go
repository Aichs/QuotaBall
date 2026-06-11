//go:build windows && legacywalk

package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"

	"krill_monitor/internal/config"
	"krill_monitor/internal/krill"
	"krill_monitor/internal/paths"
	"krill_monitor/internal/secret"
)

const transparentKey = 0x00fbfff5

type app struct {
	paths paths.Paths
	cfg   config.Config
	svc   *krill.Service

	mw                 *walk.MainWindow
	panelCanvas        *walk.CustomWidget
	panelButtons       []panelButton
	panelScrollOffset  int
	panelContentHeight int
	panelDragStart     *walk.Point
	panelWinStart      walk.Point
	panelDragging      bool
	panelPressedButton string
	notify             *walk.NotifyIcon
	tbar               *glassBall
	refreshing         bool
	visible            bool
	stop               chan struct{}
	mu                 sync.Mutex
	snap               krill.Snapshot
	status             *walk.Label
	timeLabel          *walk.Label
	authButton         *walk.PushButton
	spendValue         *walk.Label
	spendSub           *walk.Label
	walletValue        *walk.Label
	walletSub          *walk.Label
	quotaValue         *walk.Label
	quotaSub           *walk.Label
	cacheValue         *walk.Label
	cacheSub           *walk.Label
	subCount           *walk.Label
	subArea            *walk.Composite
}

func Run() error {
	p := paths.Resolve()
	cfg, err := config.Load(p.Config)
	if err != nil {
		return err
	}
	st := secret.NewStore(p.Secret)
	if cfg.Password != "" {
		_ = st.Set("password", cfg.Password)
		cfg.Password = ""
		_ = config.Save(p.Config, cfg)
	}

	a := &app{
		paths: p,
		cfg:   cfg,
		stop:  make(chan struct{}),
	}
	a.svc = &krill.Service{
		Client:    krill.NewClient(),
		Config:    &a.cfg,
		Secrets:   st,
		LegacyTok: p.LegacyTok,
	}
	a.snap = krill.EmptySnapshot("正在检查登录状态...")
	if !a.svc.HasLoginState() {
		a.snap = krill.EmptySnapshot("请登录 Krill AI")
	}

	if err := a.buildMainWindow(); err != nil {
		return err
	}
	if err := a.buildTray(); err != nil {
		return err
	}
	if a.cfg.TbarEnabled {
		if err := a.ensureGlassBall(); err != nil {
			return err
		}
		a.showGlassBallIfEnabled()
	}

	a.applySnapshot(a.snap)
	if a.svc.HasLoginState() {
		a.refresh(false)
	} else {
		a.showLogin()
	}
	go a.scheduleLoop()

	a.mw.Run()
	return nil
}

func (a *app) buildMainWindow() error {
	if err := (MainWindow{
		AssignTo: &a.mw,
		Title:    "Krill AI 额度监控",
		Size:     Size{panelWidth, panelHeight},
		MinSize:  Size{panelWidth, panelHeight},
		MaxSize:  Size{panelWidth, panelHeight},
		Layout:   VBox{MarginsZero: true},
		Background: SolidColorBrush{
			Color: walk.RGB(245, 255, 251),
		},
		Children: []Widget{
			CustomWidget{
				AssignTo:            &a.panelCanvas,
				PaintMode:           PaintBuffered,
				InvalidatesOnResize: true,
				PaintPixels:         a.paintPanel,
				OnMouseDown:         a.panelMouseDown,
				OnMouseMove:         a.panelMouseMove,
				OnMouseUp:           a.panelMouseUp,
			},
		},
	}).Create(); err != nil {
		return err
	}
	a.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if reason == walk.CloseReasonUser {
			*canceled = true
			a.hidePanel()
		}
	})
	a.visible = false
	a.positionMainWindow()
	_ = a.mw.SetClientSize(walk.Size{Width: panelWidth, Height: panelHeight})
	if a.panelCanvas != nil {
		a.panelCanvas.MouseWheel().Attach(a.panelMouseWheel)
	}
	applyFrameless(a.mw.Handle(), true)
	setLayeredColorKeyAlpha(a.mw.Handle(), transparentKey, byte(math.Round(a.cfg.Opacity*255)))
	applyTopMost(a.mw.Handle(), a.cfg.OnTop)
	a.mw.SetVisible(false)
	return nil
}

func (a *app) statCard(title string, accent walk.Color, value, sub **walk.Label, bg walk.Color) Widget {
	return Composite{
		Layout: VBox{Margins: Margins{Left: 12, Top: 10, Right: 12, Bottom: 10}, Spacing: 3},
		Background: SolidColorBrush{
			Color: bg,
		},
		Children: []Widget{
			Label{Text: title, TextColor: walk.RGB(52, 89, 105), Font: Font{Family: "Microsoft YaHei UI", PointSize: 9}},
			Label{AssignTo: value, Text: "-", TextColor: accent, Font: Font{Family: "Cascadia Code", PointSize: 19, Bold: true}},
			Label{AssignTo: sub, Text: "", TextColor: walk.RGB(54, 89, 105), Font: Font{Family: "Microsoft YaHei UI", PointSize: 8}},
		},
	}
}

func (a *app) buildTray() error {
	icon, err := walk.NewIconFromImage(makeTrayImage())
	if err != nil {
		return err
	}
	a.mw.SetIcon(icon)
	ni, err := walk.NewNotifyIcon(a.mw)
	if err != nil {
		return err
	}
	a.notify = ni
	if err := ni.SetIcon(icon); err != nil {
		return err
	}
	ni.SetToolTip("Krill AI")
	ni.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			a.togglePanel()
		}
	})
	for _, item := range []struct {
		text string
		fn   func()
	}{
		{"显示/隐藏面板", a.togglePanel},
		{"登录", a.showLogin},
		{"退出登录", a.logout},
		{"显示/隐藏玻璃球", a.toggleGlassBall},
		{"立即刷新", func() { a.refresh(true) }},
		{"退出", a.quit},
	} {
		act := walk.NewAction()
		act.SetText(item.text)
		fn := item.fn
		act.Triggered().Attach(fn)
		if err := ni.ContextMenu().Actions().Add(act); err != nil {
			return err
		}
	}
	return ni.SetVisible(true)
}

func (a *app) applySnapshot(s krill.Snapshot) {
	a.mu.Lock()
	a.snap = s
	a.mu.Unlock()

	if a.status != nil {
		a.status.SetText(s.Err)
		a.timeLabel.SetText(formatTime(s.Time))
		if s.LoggedIn {
			a.authButton.SetText("退出登录")
		} else {
			a.authButton.SetText("登录")
		}
		tq := s.Summary.TotalDailyQuotaUSD
		fr := s.Summary.TotalForwardedRemainingUSD
		a.spendValue.SetText(money(s.Spend, 2))
		a.spendSub.SetText(fmt.Sprintf("转结 %s · 剩余 %s", money(fr, 2), money(math.Max(0, tq-s.Spend), 2)))
		a.walletValue.SetText(money(s.Wallet, 2))
		if s.Wallet == 0 {
			a.walletSub.SetText("额度用完自动消耗")
		} else {
			a.walletSub.SetText("信用 + 福利")
		}
		a.quotaValue.SetText(money(tq, 0))
		a.quotaSub.SetText(fmt.Sprintf("已用 %s / 总计 %s", money(s.Spend, 2), money(tq, 0)))
		a.cacheValue.SetText(s.Cache)
		a.cacheSub.SetText("缓存命中 / 请求数")
		a.subCount.SetText(fmt.Sprintf("%d 张", len(s.Subscriptions)))
		a.renderSubscriptions(s.Subscriptions)
		a.updateTrayTooltip()
	}
	a.invalidatePanel()
	a.updateTrayTooltip()
	if a.tbar != nil {
		a.tbar.setSnapshot(s)
	}
}

func (a *app) renderSubscriptions(subs []krill.Subscription) {
	if a.subArea == nil {
		return
	}
	children := a.subArea.Children()
	old := make([]walk.Widget, 0, children.Len())
	for i := 0; i < children.Len(); i++ {
		old = append(old, children.At(i))
	}
	_ = children.Clear()
	for _, w := range old {
		w.Dispose()
	}
	for _, sub := range subs {
		a.addSubscriptionCard(sub)
	}
	a.subArea.RequestLayout()
}

func (a *app) addSubscriptionCard(sub krill.Subscription) {
	card, _ := walk.NewComposite(a.subArea)
	card.SetBackground(solid(walk.RGB(248, 254, 255)))
	layout := walk.NewVBoxLayout()
	layout.SetMargins(walk.Margins{HNear: 12, VNear: 10, HFar: 12, VFar: 10})
	layout.SetSpacing(5)
	card.SetLayout(layout)

	top, _ := walk.NewComposite(card)
	top.SetLayout(walk.NewHBoxLayout())
	label(top, ifEmpty(sub.Name, "套餐"), 11, true, walk.RGB(11, 38, 56))
	days := fmt.Sprintf("%v 天后到期", sub.DaysLeft)
	label(top, days, 9, false, walk.RGB(8, 112, 86))

	label(card, fmt.Sprintf("#%s  ·  %s → %s", sub.ID, sub.Start, sub.End), 8, false, walk.RGB(73, 110, 124))
	if len(sub.Routes) > 0 {
		routes, _ := walk.NewComposite(card)
		routes.SetLayout(walk.NewHBoxLayout())
		for i, r := range sub.Routes {
			if i >= 6 {
				break
			}
			label(routes, r, 8, false, walk.RGB(35, 86, 107))
		}
	}
	addQuota(card, "转结", sub.ForwardedUsed, sub.ForwardedLimit, sub.ForwardedPercent)
	addQuota(card, "当日", sub.DailyUsed, sub.DailyLimit, sub.DailyPercent)
}

func addQuota(parent walk.Container, title string, used, limit, pct float64) {
	row, _ := walk.NewComposite(parent)
	row.SetLayout(walk.NewHBoxLayout())
	label(row, title, 8, false, walk.RGB(73, 110, 124))
	label(row, fmt.Sprintf("%s / %s", money(used, 2), money(limit, 2)), 8, false, walk.RGB(73, 110, 124))
	bar, _ := walk.NewProgressBar(parent)
	bar.SetRange(0, 1000)
	bar.SetValue(int(math.Max(0, math.Min(100, pct)) * 10))
	bar.SetMinMaxSize(walk.Size{Width: 80, Height: 8}, walk.Size{})
}

func (a *app) refresh(revealPanel bool) {
	a.mu.Lock()
	if a.refreshing {
		a.mu.Unlock()
		return
	}
	a.refreshing = true
	a.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		snap, err := a.svc.Fetch(ctx)
		if err != nil && errors.Is(err, krill.ErrAuthRequired) {
			snap = krill.EmptySnapshot("请登录 Krill AI")
		}
		a.mw.Synchronize(func() {
			a.mu.Lock()
			a.refreshing = false
			a.mu.Unlock()
			a.applySnapshot(snap)
			if snap.LoggedIn {
				a.showGlassBallIfEnabled()
				if revealPanel {
					a.showPanel()
				}
			} else {
				a.hideGlassBall()
				a.showLogin()
			}
		})
	}()
}

func (a *app) scheduleLoop() {
	for {
		delay := time.Duration(maxInt(3, a.cfg.RefreshSec)) * time.Second
		select {
		case <-time.After(delay):
			if a.svc.HasLoginState() {
				a.mw.Synchronize(func() { a.refresh(false) })
			}
		case <-a.stop:
			return
		}
	}
}

func (a *app) authAction() {
	a.mu.Lock()
	loggedIn := a.snap.LoggedIn
	a.mu.Unlock()
	if loggedIn {
		a.logout()
	} else {
		a.showLogin()
	}
}

func (a *app) showLogin() {
	var dlg *walk.Dialog
	var emailEdit, passEdit *walk.LineEdit
	var remember *walk.CheckBox
	var errLabel *walk.Label
	var loginBtn, cancelBtn *walk.PushButton
	_, err := (Dialog{
		AssignTo:      &dlg,
		Title:         "登录 Krill AI",
		MinSize:       Size{430, 330},
		DefaultButton: &loginBtn,
		CancelButton:  &cancelBtn,
		Layout:        VBox{Margins: Margins{Left: 24, Top: 22, Right: 24, Bottom: 18}, Spacing: 10},
		Children: []Widget{
			Label{Text: "◒  登录 Krill AI", Font: Font{Family: "Microsoft YaHei UI", PointSize: 18, Bold: true}, TextColor: walk.RGB(7, 29, 45)},
			Label{Text: "额度监控", TextColor: walk.RGB(48, 98, 116)},
			LineEdit{AssignTo: &emailEdit, Text: a.cfg.Email, CueBanner: "邮箱"},
			LineEdit{AssignTo: &passEdit, PasswordMode: true, CueBanner: "密码"},
			CheckBox{AssignTo: &remember, Text: "记住登录状态", Checked: a.cfg.RememberLogin},
			Label{AssignTo: &errLabel, TextColor: walk.RGB(217, 72, 72)},
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 8},
				Children: []Widget{
					PushButton{AssignTo: &cancelBtn, Text: "取消", OnClicked: func() { dlg.Cancel() }},
					PushButton{AssignTo: &loginBtn, Text: "登录", OnClicked: func() {
						email := emailEdit.Text()
						password := passEdit.Text()
						if email == "" || password == "" {
							errLabel.SetText("请输入邮箱和密码")
							return
						}
						loginBtn.SetEnabled(false)
						errLabel.SetText("登录中...")
						go func() {
							ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
							defer cancel()
							err := a.svc.Login(ctx, email, password, remember.Checked())
							a.mw.Synchronize(func() {
								loginBtn.SetEnabled(true)
								if err != nil {
									errLabel.SetText(err.Error())
									return
								}
								a.cfg.Email = email
								a.cfg.RememberLogin = remember.Checked()
								a.cfg.Password = ""
								_ = config.Save(a.paths.Config, a.cfg)
								dlg.Accept()
								a.refresh(true)
							})
						}()
					}},
				},
			},
		},
	}.Run(a.mw))
	if err != nil {
		walk.MsgBox(a.mw, "Krill AI", err.Error(), walk.MsgBoxIconError)
	}
}

func (a *app) showSettings() {
	var dlg *walk.Dialog
	var refreshEdit *walk.LineEdit
	var onTop, tbarEnabled, remember *walk.CheckBox
	var saveBtn, cancelBtn *walk.PushButton
	_, err := (Dialog{
		AssignTo:      &dlg,
		Title:         "设置",
		MinSize:       Size{360, 300},
		DefaultButton: &saveBtn,
		CancelButton:  &cancelBtn,
		Layout:        VBox{Margins: Margins{Left: 22, Top: 20, Right: 22, Bottom: 18}, Spacing: 10},
		Children: []Widget{
			Label{Text: "设置", Font: Font{Family: "Microsoft YaHei UI", PointSize: 18, Bold: true}, TextColor: walk.RGB(7, 29, 45)},
			Label{Text: "刷新间隔", TextColor: walk.RGB(48, 98, 116)},
			LineEdit{AssignTo: &refreshEdit, Text: strconv.Itoa(a.cfg.RefreshSec), CueBanner: "秒刷新"},
			CheckBox{AssignTo: &onTop, Text: "窗口置顶", Checked: a.cfg.OnTop},
			CheckBox{AssignTo: &tbarEnabled, Text: "显示玻璃球", Checked: a.cfg.TbarEnabled},
			CheckBox{AssignTo: &remember, Text: "记住登录状态", Checked: a.cfg.RememberLogin},
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 8},
				Children: []Widget{
					PushButton{AssignTo: &cancelBtn, Text: "取消", OnClicked: func() { dlg.Cancel() }},
					PushButton{AssignTo: &saveBtn, Text: "保存", OnClicked: func() {
						refresh, _ := strconv.Atoi(refreshEdit.Text())
						a.cfg.RefreshSec = maxInt(3, refresh)
						a.cfg.OnTop = onTop.Checked()
						a.cfg.TbarEnabled = tbarEnabled.Checked()
						a.cfg.RememberLogin = remember.Checked()
						_ = config.Save(a.paths.Config, a.cfg)
						applyTopMost(a.mw.Handle(), a.cfg.OnTop)
						if a.cfg.TbarEnabled {
							a.showGlassBallIfEnabled()
						} else {
							a.hideGlassBall()
						}
						dlg.Accept()
					}},
				},
			},
		},
	}.Run(a.mw))
	if err != nil {
		walk.MsgBox(a.mw, "Krill AI", err.Error(), walk.MsgBoxIconError)
	}
}

func (a *app) logout() {
	a.svc.Logout()
	a.cfg.Password = ""
	_ = config.Save(a.paths.Config, a.cfg)
	a.applySnapshot(krill.EmptySnapshot("已退出登录"))
	a.hideGlassBall()
	a.showLogin()
}

func (a *app) togglePanel() {
	if a.visible {
		a.hidePanel()
	} else {
		a.showPanel()
	}
}

func (a *app) showPanel() {
	a.visible = true
	a.mw.SetVisible(true)
	a.mw.Show()
	a.mw.SetFocus()
	a.invalidatePanel()
}

func (a *app) hidePanel() {
	a.visible = false
	a.saveMainPosition()
	a.mw.SetVisible(false)
}

func (a *app) quit() {
	close(a.stop)
	a.saveMainPosition()
	if a.tbar != nil {
		a.tbar.close()
	}
	if a.notify != nil {
		a.notify.Dispose()
	}
	walk.App().Exit(0)
}

func (a *app) toggleGlassBall() {
	a.cfg.TbarEnabled = !a.cfg.TbarEnabled
	_ = config.Save(a.paths.Config, a.cfg)
	if a.cfg.TbarEnabled {
		a.showGlassBallIfEnabled()
	} else {
		a.hideGlassBall()
	}
}

func (a *app) showGlassBallIfEnabled() {
	if !a.cfg.TbarEnabled {
		return
	}
	if err := a.ensureGlassBall(); err == nil && a.tbar != nil {
		a.tbar.show()
	}
}

func (a *app) ensureGlassBall() error {
	if a.tbar != nil {
		return nil
	}
	gb, err := newGlassBall(a)
	if err != nil {
		return err
	}
	a.tbar = gb
	a.tbar.setSnapshot(a.snap)
	return nil
}

func (a *app) hideGlassBall() {
	if a.tbar != nil {
		a.tbar.hide()
	}
}

func (a *app) loadGlassConfig() config.Config {
	return a.cfg
}

func (a *app) mutateGlassConfig(fn func(*config.Config)) {
	fn(&a.cfg)
	_ = config.Save(a.paths.Config, a.cfg)
}

func (a *app) updateTrayTooltip() {
	if a.notify == nil {
		return
	}
	a.mu.Lock()
	s := a.snap
	a.mu.Unlock()
	tip := "Krill AI"
	if s.OK {
		tip = fmt.Sprintf("剩余 %s · 今日 %s", money(s.RemainingDaily(), 2), money(s.Spend, 2))
	} else if s.Err != "" {
		tip = s.Err
	}
	_ = a.notify.SetToolTip(tip)
}

func (a *app) positionMainWindow() {
	x := int(win.GetSystemMetrics(win.SM_CXSCREEN)) - panelWidth - 24
	y := 70
	if a.cfg.WX != nil && a.cfg.WY != nil {
		x = *a.cfg.WX
		y = *a.cfg.WY
	}
	screenW := int(win.GetSystemMetrics(win.SM_CXSCREEN))
	screenH := int(win.GetSystemMetrics(win.SM_CYSCREEN))
	x = clampInt(x, 14, maxInt(14, screenW-panelWidth-14))
	y = clampInt(y, 14, maxInt(14, screenH-panelHeight-14))
	_ = a.mw.SetBounds(walk.Rectangle{X: x, Y: y, Width: panelWidth, Height: panelHeight})
}

func (a *app) saveMainPosition() {
	if a.mw == nil || a.mw.IsDisposed() {
		return
	}
	b := a.mw.Bounds()
	a.cfg.WX = &b.X
	a.cfg.WY = &b.Y
	_ = config.Save(a.paths.Config, a.cfg)
}

func solid(c walk.Color) *walk.SolidColorBrush {
	b, _ := walk.NewSolidColorBrush(c)
	return b
}

func label(parent walk.Container, text string, size int, bold bool, color walk.Color) *walk.TextLabel {
	l, _ := walk.NewTextLabel(parent)
	l.SetText(text)
	l.SetTextColor(color)
	fontStyle := walk.FontStyle(0)
	if bold {
		fontStyle = walk.FontBold
	}
	f, _ := walk.NewFont("Microsoft YaHei UI", size, fontStyle)
	l.SetFont(f)
	return l
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("15:04")
}

func money(v float64, digits int) string {
	return fmt.Sprintf("$%s", commaFloat(v, digits))
}

func commaFloat(v float64, digits int) string {
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	s := fmt.Sprintf("%.*f", digits, v)
	dot := len(s)
	for i, ch := range s {
		if ch == '.' {
			dot = i
			break
		}
	}
	intPart, frac := s[:dot], s[dot:]
	out := ""
	for i, ch := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			out += ","
		}
		out += string(ch)
	}
	return sign + out + frac
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func ifEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func makeTrayImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	fillRoundRect(img, 2, 2, 30, 30, 8, color.RGBA{249, 115, 22, 255})
	fillRect(img, 9, 8, 14, 24, color.RGBA{255, 255, 255, 255})
	fillRect(img, 18, 8, 23, 24, color.RGBA{255, 255, 255, 255})
	return img
}

func applyFrameless(hwnd win.HWND, layered bool) {
	style := win.GetWindowLong(hwnd, win.GWL_STYLE)
	style &^= int32(win.WS_CAPTION | win.WS_THICKFRAME | win.WS_BORDER | win.WS_DLGFRAME)
	win.SetWindowLong(hwnd, win.GWL_STYLE, style)
	ex := win.GetWindowLong(hwnd, win.GWL_EXSTYLE)
	ex &^= int32(win.WS_EX_CLIENTEDGE | win.WS_EX_DLGMODALFRAME | win.WS_EX_WINDOWEDGE)
	if layered {
		ex |= int32(win.WS_EX_LAYERED)
	}
	win.SetWindowLong(hwnd, win.GWL_EXSTYLE, ex)
	win.SetWindowPos(hwnd, 0, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_NOACTIVATE|win.SWP_FRAMECHANGED)
}

func applyTopMost(hwnd win.HWND, top bool) {
	after := win.HWND_NOTOPMOST
	if top {
		after = win.HWND_TOPMOST
	}
	win.SetWindowPos(hwnd, after, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOACTIVATE)
}
