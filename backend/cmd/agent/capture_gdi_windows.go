//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"image"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	gdi32                  = syscall.NewLazyDLL("gdi32.dll")
	getDC                  = user32.NewProc("GetDC")
	releaseDC              = user32.NewProc("ReleaseDC")
	getSystemMetrics       = user32.NewProc("GetSystemMetrics")
	setThreadDPI           = user32.NewProc("SetThreadDpiAwarenessContext")
	createCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	createCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	selectObject           = gdi32.NewProc("SelectObject")
	deleteObject           = gdi32.NewProc("DeleteObject")
	deleteDC               = gdi32.NewProc("DeleteDC")
	bitBlt                 = gdi32.NewProc("BitBlt")
	getDIBits              = gdi32.NewProc("GetDIBits")
)

var errDesktopSuspended = errors.New("desktop capture suspended")

type bitmapInfoHeader struct {
	Size                         uint32
	Width, Height                int32
	Planes, BitCount             uint16
	Compression, SizeImage       uint32
	XPelsPerMeter, YPelsPerMeter int32
	ClrUsed, ClrImportant        uint32
}

// captureDesktop acquires the complete virtual desktop in physical pixels. Its
// caller supplies the current session/secure-desktop guard; no unsafe frame is
// returned if the desktop changes while a GDI copy is in progress.
func captureDesktop(ctx context.Context, suspended func() bool) (*image.RGBA, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if suspended == nil || suspended() {
		return nil, errDesktopSuspended
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := setThreadDPI.Find(); err != nil {
		return nil, err
	}
	previous, _, err := setThreadDPI.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
	if previous == 0 {
		return nil, fmt.Errorf("set capture DPI context: %w", err)
	}
	defer setThreadDPI.Call(previous)
	metric := func(n uintptr) int { v, _, _ := getSystemMetrics.Call(n); return int(int32(v)) }
	rect, err := desktopBounds(metric(76), metric(77), metric(78), metric(79))
	if err != nil {
		return nil, err
	}
	w, h := rect.Dx(), rect.Dy()
	dc, _, err := getDC.Call(0)
	if dc == 0 {
		return nil, fmt.Errorf("get desktop DC: %w", err)
	}
	defer releaseDC.Call(0, dc)
	mem, _, err := createCompatibleDC.Call(dc)
	if mem == 0 {
		return nil, fmt.Errorf("create capture DC: %w", err)
	}
	defer deleteDC.Call(mem)
	bitmap, _, err := createCompatibleBitmap.Call(dc, uintptr(w), uintptr(h))
	if bitmap == 0 {
		return nil, fmt.Errorf("create bitmap: %w", err)
	}
	defer deleteObject.Call(bitmap)
	previousBitmap, _, err := selectObject.Call(mem, bitmap)
	if previousBitmap == 0 || previousBitmap == ^uintptr(0) {
		return nil, fmt.Errorf("select bitmap: %w", err)
	}
	selected := true
	defer func() {
		if selected {
			selectObject.Call(mem, previousBitmap)
		}
	}()
	if suspended() {
		return nil, errDesktopSuspended
	}
	ok, _, err := bitBlt.Call(mem, 0, 0, uintptr(w), uintptr(h), dc, uintptr(rect.Min.X), uintptr(rect.Min.Y), 0x00CC0020|0x40000000)
	if ok == 0 {
		return nil, fmt.Errorf("copy desktop pixels: %w", err)
	}
	// GetDIBits requires that the bitmap is no longer selected in a DC.
	if old, _, err := selectObject.Call(mem, previousBitmap); old == 0 || old == ^uintptr(0) {
		return nil, fmt.Errorf("restore capture bitmap: %w", err)
	}
	selected = false
	raw := make([]byte, w*h*4)
	info := bitmapInfoHeader{Size: 40, Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32, SizeImage: uint32(len(raw))}
	lines, _, err := getDIBits.Call(dc, bitmap, 0, uintptr(h), uintptr(unsafe.Pointer(&raw[0])), uintptr(unsafe.Pointer(&info)), 0)
	if int(lines) != h {
		return nil, fmt.Errorf("read desktop pixels: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if suspended() {
		return nil, errDesktopSuspended
	}
	return bitmapRGBA(raw, w, h)
}
