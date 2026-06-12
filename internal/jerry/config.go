package jerry

import "os"

// Config bundles all runtime knobs sourced from env vars at boot.
type Config struct {
	Port            string // 8089
	MediaRoot       string // USB mount, e.g. /media/usb
	StatePath       string // JSON state on USB, e.g. /media/usb/.jerry/state.json
	MpvSocket       string // /tmp/mpv-jerry.sock
	MpvAO           string // audio output driver, e.g. "null" in containers
	MpvVO           string // video output driver, e.g. "x11" in containers
	UsePlayerStub   bool   // dev mode — log instead of driving mpv
	UseGPIOSimulated bool  // dev mode — bailout button is /api/button/press
	GPIOPin         int    // BCM pin number (default 17)
	BootSplashSecs  int    // captive-portal OSD splash duration
	RescuePassword  string // backdoor for the admin user; empty disables
}

// LoadConfig reads env vars and applies sensible defaults.
func LoadConfig() Config {
	c := Config{
		Port:           env("PORT", "8089"),
		MediaRoot:      env("JERRY_MEDIA_ROOT", "/media/usb"),
		StatePath:      env("JERRY_STATE_PATH", "/media/usb/.jerry/state.json"),
		MpvSocket:      env("JERRY_MPV_SOCKET", "/tmp/mpv-jerry.sock"),
		MpvAO:          env("JERRY_MPV_AO", ""),
		MpvVO:          env("JERRY_MPV_VO", ""),
		BootSplashSecs: 120,
		GPIOPin:        17,
	}
	if env("JERRY_PLAYER_STUB", "") != "" {
		c.UsePlayerStub = true
	}
	if env("JERRY_GPIO_SIMULATED", "") != "" {
		c.UseGPIOSimulated = true
	}
	c.RescuePassword = os.Getenv("JERRY_RESCUE_PASSWORD")
	return c
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
