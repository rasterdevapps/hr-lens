package main

import (
	"errors"
	"image"
)

const maxDesktopPixels = 64 << 20

func desktopBounds(x, y, w, h int) (image.Rectangle, error) {
	if w <= 0 || h <= 0 || int64(w)*int64(h) > maxDesktopPixels || w > 32768 || h > 32768 {
		return image.Rectangle{}, errors.New("unsupported desktop dimensions")
	}
	return image.Rect(x, y, x+w, y+h), nil
}

// GDI BI_RGB pixels are top-down BGRX. The unused fourth byte is not alpha.
func bitmapRGBA(raw []byte, w, h int) (*image.RGBA, error) {
	if _, err := desktopBounds(0, 0, w, h); err != nil {
		return nil, err
	}
	if len(raw) != w*h*4 {
		return nil, errors.New("invalid bitmap byte count")
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(raw); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = raw[i+2], raw[i+1], raw[i], 255
	}
	return img, nil
}

func resizeDesktop(src image.Image, maxWidth int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if maxWidth > 0 && w > maxWidth {
		h = max(1, int(int64(h)*int64(maxWidth)/int64(w)))
		w = maxWidth
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(x, y, src.At(b.Min.X+x*b.Dx()/w, b.Min.Y+y*b.Dy()/h))
		}
	}
	return dst
}
