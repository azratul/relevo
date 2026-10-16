package icon

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"testing"
)

func TestFormats(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(PNG(64, false)))
	if err != nil || img.Bounds().Dx() != 64 {
		t.Fatal("png", err)
	}
	// centre is white (chevron), corner transparent, disc edge blue
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Fatal("corner should be transparent")
	}
	if r, g, b, _ := img.At(32, 6).RGBA(); !(b > r && b > g) {
		t.Fatal("disc should be blue")
	}
	ico := ICO(false, 16, 32, 256)
	if binary.LittleEndian.Uint16(ico[4:]) != 3 || ico[6+2*16] != 0 {
		t.Fatal("ico header")
	}
	icns := ICNS()
	if string(icns[:4]) != "icns" || int(binary.BigEndian.Uint32(icns[4:])) != len(icns) {
		t.Fatal("icns header")
	}
}
