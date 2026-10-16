// Package icon draws the Relevo icon (a blue disc with a "next" chevron, the
// same artwork as the web remote's favicon) at any size, and packs it as PNG,
// ICO (Windows) and ICNS (macOS). No binary assets are kept by hand.
package icon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
)

var (
	blue  = color.NRGBA{0x1f, 0x6f, 0xeb, 0xff}
	amber = color.NRGBA{0x9a, 0x67, 0x00, 0xff}
	white = color.NRGBA{0xff, 0xff, 0xff, 0xff}
)

// Geometry on a 32×32 canvas (matches the SVG below).
const (
	cx, cy, radius = 16.0, 16.0, 15.0
	strokeHalf     = 1.6
)

var chevron = [][2]float64{{13, 9}, {20, 16}, {13, 23}}

// SVG returns the scalable icon.
func SVG() string {
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><circle cx="16" cy="16" r="15" fill="#1f6feb"/>` +
		`<path d="M13 9l7 7-7 7" stroke="#fff" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round" fill="none"/></svg>` + "\n"
}

func segDist(px, py float64, a, b [2]float64) float64 {
	dx, dy := b[0]-a[0], b[1]-a[1]
	t := ((px-a[0])*dx + (py-a[1])*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-(a[0]+t*dx), py-(a[1]+t*dy))
}

// Image renders the icon at size×size with 4×4 supersampling; paused draws
// the disc in amber (used by the tray while control is paused).
func Image(size int, paused bool) *image.NRGBA {
	fg := blue
	if paused {
		fg = amber
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const ss = 4
	scale := 32.0 / float64(size)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var disc, mark int
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px := (float64(x) + (float64(sx)+0.5)/ss) * scale
					py := (float64(y) + (float64(sy)+0.5)/ss) * scale
					if math.Hypot(px-cx, py-cy) > radius {
						continue
					}
					disc++
					if segDist(px, py, chevron[0], chevron[1]) <= strokeHalf || segDist(px, py, chevron[1], chevron[2]) <= strokeHalf {
						mark++
					}
				}
			}
			if disc == 0 {
				continue
			}
			// colour = mix(fg, white, mark/disc); alpha = coverage
			f := float64(mark) / float64(disc)
			mix := func(a, b uint8) uint8 { return uint8(math.Round(float64(a)*(1-f) + float64(b)*f)) }
			img.SetNRGBA(x, y, color.NRGBA{mix(fg.R, white.R), mix(fg.G, white.G), mix(fg.B, white.B), uint8(math.Round(255 * float64(disc) / ss / ss))})
		}
	}
	return img
}

// PNG encodes the icon at size.
func PNG(size int, paused bool) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, Image(size, paused))
	return b.Bytes()
}

// ICO packs PNG images of the given sizes into a .ico (Vista+ PNG entries).
func ICO(paused bool, sizes ...int) []byte {
	var imgs [][]byte
	for _, s := range sizes {
		imgs = append(imgs, PNG(s, paused))
	}
	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, []uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		dim := byte(s)
		if s >= 256 {
			dim = 0 // 0 means 256
		}
		out.Write([]byte{dim, dim, 0, 0})
		binary.Write(&out, binary.LittleEndian, []uint16{1, 32})
		binary.Write(&out, binary.LittleEndian, []uint32{uint32(len(imgs[i])), uint32(offset)})
		offset += len(imgs[i])
	}
	for _, b := range imgs {
		out.Write(b)
	}
	return out.Bytes()
}

// ICNS packs the macOS icon family (PNG-based entries, 16 pt to 512 pt @2x).
func ICNS() []byte {
	entries := []struct {
		typ  string
		size int
	}{{"icp4", 16}, {"icp5", 32}, {"icp6", 64}, {"ic07", 128}, {"ic08", 256}, {"ic09", 512}, {"ic10", 1024},
		{"ic11", 32}, {"ic12", 64}, {"ic13", 256}, {"ic14", 512}}
	var body bytes.Buffer
	for _, e := range entries {
		p := PNG(e.size, false)
		body.WriteString(e.typ)
		binary.Write(&body, binary.BigEndian, uint32(8+len(p)))
		body.Write(p)
	}
	var out bytes.Buffer
	out.WriteString("icns")
	binary.Write(&out, binary.BigEndian, uint32(8+body.Len()))
	out.Write(body.Bytes())
	return out.Bytes()
}
