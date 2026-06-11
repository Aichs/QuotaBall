//go:build windows && !legacywalk

package ui

import (
	"image"
	"image/color"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

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

func cursorPoint() walk.Point {
	var pt win.POINT
	if win.GetCursorPos(&pt) {
		return walk.Point{X: int(pt.X), Y: int(pt.Y)}
	}
	return walk.Point{}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
