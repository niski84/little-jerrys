// Package splash renders the boot-time on-screen-display shown on the TV
// before mpv switches to playback. The image carries a QR code that
// auto-joins the LittleJerrys_Config WiFi network when scanned by a phone,
// plus instructions for the manual flow.
//
// The splash is regenerated each boot (and on demand via /api/splash.png) so
// the displayed IP / SSID always reflect current state. Output is a 1920x1080
// PNG written to a path the player loads.
package splash

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"os"

	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// Config carries everything the splash needs to render. SSID is shown in
// human-readable form and encoded in the WiFi QR. AdminURL is the local
// address to visit after joining (typically http://192.168.4.1).
type Config struct {
	SSID     string // e.g. "LittleJerrys_Config"
	AdminURL string // e.g. "http://192.168.4.1"
	Width    int    // default 1920
	Height   int    // default 1080
}

// Render writes a PNG splash to w.
func Render(cfg Config, w io.Writer) error {
	if cfg.Width == 0 {
		cfg.Width = 1920
	}
	if cfg.Height == 0 {
		cfg.Height = 1080
	}
	if cfg.SSID == "" {
		cfg.SSID = "LittleJerrys_Config"
	}
	if cfg.AdminURL == "" {
		cfg.AdminURL = "http://192.168.4.1"
	}

	canvas := image.NewRGBA(image.Rect(0, 0, cfg.Width, cfg.Height))

	// Black background — looks correct on any TV regardless of color mode.
	bg := color.RGBA{8, 8, 12, 255}
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)

	// Header: amber "Smash Deck" tag and big "Little Jerry's" title.
	amber := color.RGBA{250, 204, 21, 255}
	white := color.RGBA{240, 240, 245, 255}
	muted := color.RGBA{161, 161, 170, 255}

	drawText(canvas, "SMASH DECK", 80, 100, amber, 4)
	drawText(canvas, "Little Jerry's", 80, 180, white, 8)
	drawText(canvas, "First-time setup", 80, 260, muted, 3)

	// QR code: WIFI: format auto-joins on iOS/Android.
	wifiPayload := fmt.Sprintf("WIFI:S:%s;T:nopass;;", cfg.SSID)
	qr, err := qrcode.New(wifiPayload, qrcode.Medium)
	if err != nil {
		return fmt.Errorf("qr: %w", err)
	}
	qrImg := qr.Image(560)
	qrX := cfg.Width - qrImg.Bounds().Dx() - 80
	qrY := 100
	// White card behind the QR so it scans reliably on dark TV calibration.
	cardPad := 24
	cardRect := image.Rect(qrX-cardPad, qrY-cardPad,
		qrX+qrImg.Bounds().Dx()+cardPad, qrY+qrImg.Bounds().Dy()+cardPad)
	draw.Draw(canvas, cardRect, &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(qrX, qrY, qrX+qrImg.Bounds().Dx(), qrY+qrImg.Bounds().Dy()),
		qrImg, image.Point{}, draw.Src)

	// Instructions.
	y := 380
	step := 64
	drawText(canvas, "1.  Scan the QR code with your phone camera", 80, y, white, 3)
	y += step
	drawText(canvas, "    OR connect to WiFi:  "+cfg.SSID, 80, y, muted, 3)
	y += step + 20
	drawText(canvas, "2.  Open a browser and visit:", 80, y, white, 3)
	y += step
	drawText(canvas, "    "+cfg.AdminURL, 80, y, amber, 4)
	y += step + 20
	drawText(canvas, "3.  Sign in with admin / admin", 80, y, white, 3)
	y += step
	drawText(canvas, "    (you'll be asked to set a new password)", 80, y, muted, 2)

	// Footer.
	footerY := cfg.Height - 80
	drawText(canvas, "This screen disappears once Tony sets the WiFi or after 2 minutes.",
		80, footerY, muted, 2)

	return png.Encode(w, canvas)
}

// RenderToFile is a convenience for the boot path — write to disk so mpv can
// loadfile the result.
func RenderToFile(cfg Config, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()
	return Render(cfg, f)
}

// drawText is a minimal text renderer that scales basicfont (7x13) by an
// integer factor. Cheap, no external font files, looks fine at TV viewing
// distance for splash-level information density.
func drawText(canvas *image.RGBA, text string, x, y int, col color.RGBA, scale int) {
	if scale < 1 {
		scale = 1
	}
	face := basicfont.Face7x13

	// Render to a small temp image at 1x, then nearest-neighbor scale up.
	advance := face.Advance // int (pixels)
	w := len(text) * advance
	h := face.Height
	if w == 0 || h == 0 {
		return
	}
	small := image.NewRGBA(image.Rect(0, 0, w+8, h+8))
	d := &font.Drawer{
		Dst:  small,
		Src:  &image.Uniform{C: col},
		Face: face,
		Dot:  fixed.Point26_6{X: fixed.I(2), Y: fixed.I(face.Ascent + 2)},
	}
	d.DrawString(text)

	for sy := 0; sy < small.Bounds().Dy(); sy++ {
		for sx := 0; sx < small.Bounds().Dx(); sx++ {
			c := small.RGBAAt(sx, sy)
			if c.A == 0 {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					px := x + sx*scale + dx
					py := y + sy*scale + dy
					if px < 0 || py < 0 || px >= canvas.Bounds().Dx() || py >= canvas.Bounds().Dy() {
						continue
					}
					canvas.SetRGBA(px, py, c)
				}
			}
		}
	}
}
