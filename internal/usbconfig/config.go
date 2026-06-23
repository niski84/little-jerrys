// Package usbconfig parses an operator-editable config file from the USB
// drive (default <MediaRoot>/jerry.conf). Format is dotenv-style KEY=VALUE
// — chosen because operators can edit it from any text editor on any OS
// without needing to know YAML or JSON syntax.
//
// Detected keys:
//
//	TENANT_NAME=Little Jerry's      # shown in nav + page titles
//	AP_SSID=LittleJerrys_Config     # captive-portal WiFi name
//	LOGO_FILE=branding/logo.png     # path relative to <MediaRoot>; empty → use bundled default
//	CAPTIVE_PORTAL=true             # opt-in: manage WiFi / spawn setup AP. OFF by default.
//
// CAPTIVE_PORTAL is OFF unless explicitly set true. The WiFi management it
// enables (forced nmcli rescans, AP-mode switching) is only safe on a
// dedicated Pi appliance — on any normal multi-use machine it disrupts the
// host's own networking. Leave it unset everywhere but real units.
//
// Missing file is fine — the appliance falls back to compiled-in defaults.
package usbconfig

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Config is what the file expresses. Empty fields mean "use the default".
type Config struct {
	TenantName string
	APssid     string
	LogoFile   string // relative to MediaRoot
	// CaptivePortal gates all nmcli-based WiFi management (rescans + AP-mode
	// switching). Defaults to false so a fresh install never touches the
	// host's networking; only real Pi appliances set CAPTIVE_PORTAL=true.
	CaptivePortal bool
}

// Load reads jerry.conf from the media root. Returns a zero-value Config
// (all fields empty) when the file is absent or unreadable. Caller decides
// what defaults to substitute for empty fields.
func Load(mediaRoot string) Config {
	if mediaRoot == "" {
		return Config{}
	}
	path := filepath.Join(mediaRoot, "jerry.conf")
	f, err := os.Open(path)
	if err != nil {
		return Config{}
	}
	defer f.Close()

	cfg := Config{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip an inline `# comment` but preserve `#` inside quoted values.
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		// Strip surrounding quotes if present.
		val = strings.Trim(val, `"'`)
		switch strings.ToUpper(key) {
		case "TENANT_NAME":
			cfg.TenantName = val
		case "AP_SSID":
			cfg.APssid = val
		case "LOGO_FILE":
			cfg.LogoFile = val
		case "CAPTIVE_PORTAL":
			switch strings.ToLower(val) {
			case "1", "true", "yes", "on":
				cfg.CaptivePortal = true
			}
		}
	}
	return cfg
}
