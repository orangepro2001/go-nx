// Package tile implements lossless tile diffing and encoding for BGRA frames.
package tile

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"io"
)

const (
	Size = 64 // tile edge in pixels
	bpp  = 4  // BGRA
)

// Tile record types.
const (
	typeSolid   = 0 // payload: 4 bytes color
	typePalette = 1 // payload: n-1 (1 byte), n*4 palette bytes, RLE pairs (run-1, index)
	typeRaw     = 2 // payload: deflate-compressed raw pixels
)

// Frame is a top-down BGRA image with stride W*4.
type Frame struct {
	W, H int
	Pix  []byte
}

func NewFrame(w, h int) *Frame {
	return &Frame{W: w, H: h, Pix: make([]byte, w*h*bpp)}
}

// Cols and Rows give the tile grid dimensions.
func (f *Frame) Cols() int { return (f.W + Size - 1) / Size }
func (f *Frame) Rows() int { return (f.H + Size - 1) / Size }

// bounds returns the pixel rectangle of tile (tx, ty), clipped to the frame.
func (f *Frame) bounds(tx, ty int) (x0, y0, w, h int) {
	x0, y0 = tx*Size, ty*Size
	w, h = min(Size, f.W-x0), min(Size, f.H-y0)
	return
}

// Differ remembers the previous frame and reports which tiles changed.
type Differ struct {
	prev *Frame
}

// Diff returns the (ty*cols+tx) indices of tiles that differ from the previous
// frame passed in, then stores cur as the new baseline. The first call, or a
// call after a size change, reports every tile.
func (d *Differ) Diff(cur *Frame) []int {
	full := d.prev == nil || d.prev.W != cur.W || d.prev.H != cur.H
	if full {
		d.prev = NewFrame(cur.W, cur.H)
	}
	cols, rows := cur.Cols(), cur.Rows()
	var changed []int
	for ty := 0; ty < rows; ty++ {
		for tx := 0; tx < cols; tx++ {
			x0, y0, w, h := cur.bounds(tx, ty)
			same := !full
			for y := y0; same && y < y0+h; y++ {
				o := (y*cur.W + x0) * bpp
				same = bytes.Equal(cur.Pix[o:o+w*bpp], d.prev.Pix[o:o+w*bpp])
			}
			if !same {
				changed = append(changed, ty*cols+tx)
			}
		}
	}
	copy(d.prev.Pix, cur.Pix)
	return changed
}

// Encode serializes the given tiles of f. Layout per record:
// tx u16, ty u16, type u8, len u32, payload.
func Encode(f *Frame, idxs []int) ([]byte, error) {
	var out bytes.Buffer
	cols := f.Cols()
	for _, i := range idxs {
		tx, ty := i%cols, i/cols
		x0, y0, w, h := f.bounds(tx, ty)
		px := gather(f, x0, y0, w, h)
		typ, payload, err := encodeTile(px)
		if err != nil {
			return nil, err
		}
		var hdr [9]byte
		binary.LittleEndian.PutUint16(hdr[0:], uint16(tx))
		binary.LittleEndian.PutUint16(hdr[2:], uint16(ty))
		hdr[4] = typ
		binary.LittleEndian.PutUint32(hdr[5:], uint32(len(payload)))
		out.Write(hdr[:])
		out.Write(payload)
	}
	return out.Bytes(), nil
}

// Rect is a pixel rectangle.
type Rect struct{ X, Y, W, H int }

// Decode applies encoded tile records onto dst and returns the rectangles it
// changed, one per tile record.
func Decode(dst *Frame, data []byte) ([]Rect, error) {
	var rects []Rect
	for len(data) > 0 {
		if len(data) < 9 {
			return nil, errors.New("tile: truncated header")
		}
		tx := int(binary.LittleEndian.Uint16(data[0:]))
		ty := int(binary.LittleEndian.Uint16(data[2:]))
		typ := data[4]
		n := int(binary.LittleEndian.Uint32(data[5:]))
		data = data[9:]
		if len(data) < n {
			return nil, errors.New("tile: truncated payload")
		}
		payload := data[:n]
		data = data[n:]
		if tx >= dst.Cols() || ty >= dst.Rows() {
			return nil, errors.New("tile: index out of range")
		}
		x0, y0, w, h := dst.bounds(tx, ty)
		px, err := decodeTile(typ, payload, w*h*bpp)
		if err != nil {
			return nil, err
		}
		scatter(dst, x0, y0, w, h, px)
		rects = append(rects, Rect{x0, y0, w, h})
	}
	return rects, nil
}

func gather(f *Frame, x0, y0, w, h int) []byte {
	px := make([]byte, w*h*bpp)
	for y := 0; y < h; y++ {
		o := ((y0+y)*f.W + x0) * bpp
		copy(px[y*w*bpp:], f.Pix[o:o+w*bpp])
	}
	return px
}

func scatter(f *Frame, x0, y0, w, h int, px []byte) {
	for y := 0; y < h; y++ {
		o := ((y0+y)*f.W + x0) * bpp
		copy(f.Pix[o:o+w*bpp], px[y*w*bpp:(y+1)*w*bpp])
	}
}

func encodeTile(px []byte) (byte, []byte, error) {
	// Build a palette; give up past 256 colors.
	idx := make(map[[4]byte]byte, 16)
	var pal [][4]byte
	indices := make([]byte, len(px)/bpp)
	for i := range indices {
		c := [4]byte(px[i*bpp : i*bpp+bpp])
		k, ok := idx[c]
		if !ok {
			if len(pal) == 256 {
				return encodeRaw(px)
			}
			k = byte(len(pal))
			idx[c] = k
			pal = append(pal, c)
		}
		indices[i] = k
	}
	if len(pal) == 1 {
		return typeSolid, append([]byte(nil), pal[0][:]...), nil
	}
	out := []byte{byte(len(pal) - 1)}
	for _, c := range pal {
		out = append(out, c[:]...)
	}
	for i := 0; i < len(indices); {
		j := i + 1
		for j < len(indices) && indices[j] == indices[i] && j-i < 256 {
			j++
		}
		out = append(out, byte(j-i-1), indices[i])
		i = j
	}
	if len(out) >= len(px) {
		return encodeRaw(px)
	}
	return typePalette, out, nil
}

func encodeRaw(px []byte) (byte, []byte, error) {
	var buf bytes.Buffer
	zw, err := flate.NewWriter(&buf, flate.BestSpeed)
	if err != nil {
		return 0, nil, err
	}
	if _, err := zw.Write(px); err != nil {
		return 0, nil, err
	}
	if err := zw.Close(); err != nil {
		return 0, nil, err
	}
	return typeRaw, buf.Bytes(), nil
}

func decodeTile(typ byte, p []byte, size int) ([]byte, error) {
	switch typ {
	case typeSolid:
		if len(p) != bpp {
			return nil, errors.New("tile: bad solid payload")
		}
		px := make([]byte, size)
		for i := 0; i < size; i += bpp {
			copy(px[i:], p)
		}
		return px, nil
	case typePalette:
		if len(p) < 1 {
			return nil, errors.New("tile: bad palette payload")
		}
		n := int(p[0]) + 1
		if len(p) < 1+n*bpp || (len(p)-1-n*bpp)%2 != 0 {
			return nil, errors.New("tile: bad palette payload")
		}
		pal := p[1 : 1+n*bpp]
		rle := p[1+n*bpp:]
		px := make([]byte, 0, size)
		for i := 0; i < len(rle); i += 2 {
			k := int(rle[i+1])
			if k >= n {
				return nil, errors.New("tile: palette index out of range")
			}
			for r := 0; r <= int(rle[i]); r++ {
				px = append(px, pal[k*bpp:k*bpp+bpp]...)
			}
		}
		if len(px) != size {
			return nil, errors.New("tile: palette size mismatch")
		}
		return px, nil
	case typeRaw:
		px, err := io.ReadAll(flate.NewReader(bytes.NewReader(p)))
		if err != nil {
			return nil, err
		}
		if len(px) != size {
			return nil, errors.New("tile: raw size mismatch")
		}
		return px, nil
	}
	return nil, errors.New("tile: unknown type")
}
