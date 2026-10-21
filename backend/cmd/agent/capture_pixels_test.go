package main

import (
	"image"
	"image/color"
	"testing"
)

func TestDesktopBoundsAndPixels(t *testing.T) {
	bounds, err := desktopBounds(-1920, -100, 3840, 2160)
	if err != nil || bounds.Min.X != -1920 || bounds.Max.X != 1920 {
		t.Fatalf("virtual desktop: %v %v", bounds, err)
	}
	for _, size := range [][2]int{{0, 10}, {-1, 10}, {100000, 100000}, {32768, 32768}} {
		if _, err := desktopBounds(0, 0, size[0], size[1]); err == nil {
			t.Fatal("accepted invalid geometry", size)
		}
	}
	img, err := bitmapRGBA([]byte{3, 2, 1, 0, 6, 5, 4, 0}, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := img.RGBAAt(0, 0); got != (color.RGBA{1, 2, 3, 255}) {
		t.Fatalf("BGRX conversion: %v", got)
	}
	if _, err := bitmapRGBA([]byte{1}, 2, 1); err == nil {
		t.Fatal("accepted short bitmap")
	}
}
func TestDesktopResize(t *testing.T) {
	src := image.NewRGBA(image.Rect(-100, -50, 300, 150))
	src.Set(-100, -50, color.RGBA{255, 0, 0, 255})
	dst := resizeDesktop(src, 128)
	if dst.Bounds() != image.Rect(0, 0, 128, 64) || dst.RGBAAt(0, 0).R != 255 {
		t.Fatal("scaling lost geometry/origin")
	}
	if got := resizeDesktop(src, 800).Bounds(); got != image.Rect(0, 0, 400, 200) {
		t.Fatal("upscaled desktop", got)
	}
}
