package splash

import (
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestWaitingCardDoesNotAdvertiseSetupCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "waiting.png")
	if err := RenderWaiting("Custom Venue", "", path); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 1920 || img.Bounds().Dy() != 1080 {
		t.Fatalf("unexpected dimensions: %v", img.Bounds())
	}
	// The former setup-instructions region must remain blank, regardless of
	// the venue's network settings. The optional logo is outside this region.
	background := color.RGBA{0, 35, 102, 255}
	for y := 600; y < 800; y++ {
		for x := 120; x < 1400; x++ {
			if got := color.RGBAModel.Convert(img.At(x, y)); got != background {
				t.Fatalf("unexpected setup content at (%d,%d): %v", x, y, got)
			}
		}
	}
}
