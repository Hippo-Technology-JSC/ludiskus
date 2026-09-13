package storage

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestOptimizeImageLosslesslyPreservesPNGPixels(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			source.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 120, A: 255})
		}
	}
	var input bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.NoCompression}
	if err := encoder.Encode(&input, source); err != nil {
		t.Fatal(err)
	}

	output, err := optimizeImageLosslessly(input.Bytes(), "image/png", 40_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) >= input.Len() {
		t.Fatalf("expected smaller output: input=%d output=%d", input.Len(), len(output))
	}
	decoded, err := png.Decode(bytes.NewReader(output))
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			sourceR, sourceG, sourceB, sourceA := source.At(x, y).RGBA()
			outputR, outputG, outputB, outputA := decoded.At(x, y).RGBA()
			if sourceR != outputR || sourceG != outputG || sourceB != outputB || sourceA != outputA {
				t.Fatalf("pixel changed at %d,%d", x, y)
			}
		}
	}
}

func TestOptimizeImageLosslesslyPassesThroughUnsupportedFormat(t *testing.T) {
	input := []byte("jpeg bytes")
	output, err := optimizeImageLosslessly(input, "image/jpeg", 40_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(input, output) {
		t.Fatal("unsupported format must pass through unchanged")
	}
}

func TestOptimizeImageLosslesslyRejectsOversizedPNG(t *testing.T) {
	var input bytes.Buffer
	if err := png.Encode(&input, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if _, err := optimizeImageLosslessly(input.Bytes(), "image/png", 3); err == nil {
		t.Fatal("expected max-pixel error")
	}
}
