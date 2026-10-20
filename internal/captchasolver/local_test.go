package captchasolver

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestLocalSolverFindsSyntheticGap(t *testing.T) {
	const width, height, pieceWidth, gap = 320, 160, 60, 105
	background := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			value := uint8((x*7 + y*3) % 256)
			background.Set(x, y, color.NRGBA{R: value, G: uint8(255 - value), B: uint8((x + y) % 256), A: 255})
		}
	}
	for y := 0; y < height; y++ {
		for x := gap; x < gap+pieceWidth; x++ {
			old := background.NRGBAAt(x, y)
			background.SetNRGBA(x, y, color.NRGBA{R: old.R / 3, G: old.G / 3, B: old.B / 3, A: 255})
		}
	}
	piece := image.NewNRGBA(image.Rect(0, 0, pieceWidth, height))
	for y := 0; y < height; y++ {
		for x := 0; x < pieceWidth; x++ {
			old := background.NRGBAAt(gap+x, y)
			// Restore the unshaded original in the cutout while preserving a
			// slightly irregular alpha silhouette.
			value := uint8((old.R*3 + old.G*3 + old.B*3) / 3)
			if y%37 == 0 && (x < 2 || x >= pieceWidth-2) {
				piece.SetNRGBA(x, y, color.NRGBA{A: 0})
			} else {
				piece.SetNRGBA(x, y, color.NRGBA{R: value, G: uint8(255 - value), B: uint8((gap + x + y) % 256), A: 255})
			}
		}
	}
	var bgBuf, pieceBuf bytes.Buffer
	if err := png.Encode(&bgBuf, background); err != nil || png.Encode(&pieceBuf, piece) != nil {
		t.Fatal(err)
	}
	offset, err := NewLocal().Solve(nil, bgBuf.Bytes(), pieceBuf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if offset < gap-4 || offset > gap+4 {
		t.Fatalf("offset=%d want=%d", offset, gap)
	}
}
