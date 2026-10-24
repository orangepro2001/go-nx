package main

import "image"

func newRGBA(w, h int, pix []byte) *image.RGBA {
	return &image.RGBA{Pix: pix, Stride: 4 * w, Rect: image.Rect(0, 0, w, h)}
}
