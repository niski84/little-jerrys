package jerry

import (
	"os"
	"strconv"
)

// Config bundles all runtime knobs sourced from env vars at boot.
type Config struct {
	Port             string // 8089
	MediaRoot        string // USB mount, e.g. /media/usb
	StatePath        string // JSON state on USB, e.g. /media/usb/.jerry/state.json
	MpvSocket        string // /tmp/mpv-jerry.sock
	MpvAO            string // audio output driver, e.g. "null" in containers
	MpvVO            string // video output driver, e.g. "x11" in containers
	UsePlayerStub    bool   // dev mode — log instead of driving mpv
	UseGPIOSimulated bool   // dev mode — bailout button is /api/button/press
	GPIOPin          int    // BCM pin number (default 17)
	BootSplashSecs   int    // captive-portal OSD splash duration
	RescuePassword   string // backdoor for the admin user; empty disables
	// Auto-update
	AutoUpdate        bool // default true; set JERRY_AUTO_UPDATE=false to disable
	UpdateWindowFrom  int  // local hour the safe restart window opens (default 2)
	UpdateWindowTo    int  // local hour the safe restart window closes (default 4)
}

// LoadConfig reads env vars and applies sensible defaults.
func LoadConfig() Config {
	c := Config{
		Port:             env("PORT", "8089"),
		MediaRoot:        env("JERRY_MEDIA_ROOT", "/media/usb"),
		StatePath:        env("JERRY_STATE_PATH", "/media/usb/.jerry/state.json"),
		MpvSocket:        env("JERRY_MPV_SOCKET", "/tmp/mpv-jerry.sock"),
		MpvAO:            env("JERRY_MPV_AO", ""),
		MpvVO:            env("JERRY_MPV_VO", ""),
		BootSplashSecs:   120,
		GPIOPin:          17,
		AutoUpdate:       os.Getenv("JERRY_AUTO_UPDATE") != "false",
		UpdateWindowFrom: envInt("JERRY_UPDATE_WINDOW_FROM", 2),
		UpdateWindowTo:   envInt("JERRY_UPDATE_WINDOW_TO", 4),
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

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
