package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"runtime"
)

// trayIcon draws a 32x32 icon (two arrows) so no binary asset is needed.
func trayIcon(paused bool) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	fg := color.NRGBA{0x1f, 0x6f, 0xeb, 0xff}
	if paused {
		fg = color.NRGBA{0x9a, 0x67, 0x00, 0xff}
	}
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			dx, dy := x-16, y-16
			if dx*dx+dy*dy <= 15*15 {
				img.Set(x, y, fg)
			}
		}
	}
	white := color.NRGBA{0xff, 0xff, 0xff, 0xff}
	for i := 0; i < 7; i++ { // ">" chevron
		for t := 0; t < 3; t++ {
			img.Set(12+i+t, 9+i, white)
			img.Set(12+i+t, 23-i, white)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	if runtime.GOOS != "windows" {
		return buf.Bytes()
	}
	// Windows wants .ico; an ICO may embed PNG data (Vista+).
	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, []uint16{0, 1, 1})
	ico.Write([]byte{32, 32, 0, 0})
	binary.Write(&ico, binary.LittleEndian, []uint16{1, 32})
	binary.Write(&ico, binary.LittleEndian, []uint32{uint32(buf.Len()), 22})
	ico.Write(buf.Bytes())
	return ico.Bytes()
}
