package usbconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConf writes a jerry.conf into a temp media root and returns the root.
func writeConf(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "jerry.conf"), []byte(body), 0o644); err != nil {
		t.Fatalf("write jerry.conf: %v", err)
	}
	return root
}

// TestCaptivePortalDefaultsOff is the safety-critical guarantee: the WiFi
// management (forced rescans / AP-mode switching) must NEVER engage unless an
// operator explicitly opts in. A regression here disrupts the host's network.
func TestCaptivePortalDefaultsOff(t *testing.T) {
	// Absent file → zero-value config → off.
	if Load(t.TempDir()).CaptivePortal {
		t.Fatal("CaptivePortal must be false when jerry.conf is absent")
	}
	// Present file, key omitted → still off.
	if Load(writeConf(t, "TENANT_NAME=Little Jerry's\n")).CaptivePortal {
		t.Fatal("CaptivePortal must be false when CAPTIVE_PORTAL is unset")
	}
	// Empty media root → off.
	if Load("").CaptivePortal {
		t.Fatal("CaptivePortal must be false for empty media root")
	}
}

func TestCaptivePortalParsing(t *testing.T) {
	on := []string{"true", "TRUE", "True", "1", "yes", "on"}
	for _, v := range on {
		if got := Load(writeConf(t, "CAPTIVE_PORTAL="+v+"\n")).CaptivePortal; !got {
			t.Errorf("CAPTIVE_PORTAL=%q: want true, got false", v)
		}
	}
	off := []string{"false", "0", "no", "off", "", "garbage"}
	for _, v := range off {
		if got := Load(writeConf(t, "CAPTIVE_PORTAL="+v+"\n")).CaptivePortal; got {
			t.Errorf("CAPTIVE_PORTAL=%q: want false, got true", v)
		}
	}
}

// TestLoadOtherKeys guards the existing keys keep parsing alongside the new one.
func TestLoadOtherKeys(t *testing.T) {
	cfg := Load(writeConf(t, "TENANT_NAME=Acme\nAP_SSID=Acme_Setup\nLOGO_FILE=branding/acme.png\nCAPTIVE_PORTAL=true\n"))
	if cfg.TenantName != "Acme" || cfg.APssid != "Acme_Setup" || cfg.LogoFile != "branding/acme.png" || !cfg.CaptivePortal {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}
