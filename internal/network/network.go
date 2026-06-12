// Package network drives NetworkManager via nmcli to switch between client
// and AP (captive-portal) modes. On boot, if no known WiFi network is
// available, the Pi spawns its own SSID `LittleJerrys_Config` so the staff
// can connect their phone and configure WiFi credentials from the admin UI.
//
// All nmcli interactions are wrapped here so the rest of the app deals with
// pure Go types. On non-Pi dev builds, Available() returns false and methods
// return descriptive errors instead of shelling out.
package network

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"
)

const (
	APConnectionName = "LittleJerrysConfig"

	// APaddress is on an uncommon subnet (192.168.42.0/24) so it doesn't
	// collide with the typical home / restaurant 192.168.{0,1,4}.0/24
	// ranges. The Pi takes .1 and DHCP serves clients on .100+ via
	// NetworkManager's `ipv4.method=shared`.
	APaddress = "192.168.42.1"
)

// APssid is the captive-portal WiFi network name. Settable at boot from
// jerry.conf (AP_SSID=...) so a tenant rebrand doesn't require recompile.
var APssid = "Channel14_Setup"

// SetAPssid overrides the broadcast SSID. Must be called before EnsureAPProfile.
func SetAPssid(s string) {
	if strings.TrimSpace(s) != "" {
		APssid = s
	}
}

// Manager wraps nmcli interactions.
type Manager struct {
	nmcliPath string // resolved at construction; empty means unavailable
}

// New constructs a Manager. nmcli must be on PATH for full functionality.
// On non-Pi machines without NetworkManager, the Manager still constructs
// but Available() will be false.
func New() *Manager {
	path, _ := exec.LookPath("nmcli")
	return &Manager{nmcliPath: path}
}

// Available reports whether nmcli is usable on this host.
func (m *Manager) Available() bool { return m.nmcliPath != "" }

// SSID is one row from `nmcli device wifi list`.
type SSID struct {
	Name   string
	Signal int
	InUse  bool
}

// Scan returns visible WiFi networks. Stub on non-NM hosts.
func (m *Manager) Scan(ctx context.Context) ([]SSID, error) {
	if !m.Available() {
		return nil, fmt.Errorf("nmcli not available on this host")
	}
	out, err := m.run(ctx, "-t", "-f", "IN-USE,SSID,SIGNAL", "device", "wifi", "list", "--rescan", "yes")
	if err != nil {
		return nil, err
	}
	var ssids []SSID
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) < 3 {
			continue
		}
		ssids = append(ssids, SSID{
			InUse:  parts[0] == "*",
			Name:   parts[1],
			Signal: atoi(parts[2]),
		})
	}
	return ssids, nil
}

// JoinNetwork connects to ssid with the given password and disables AP mode.
func (m *Manager) JoinNetwork(ctx context.Context, ssid, password string) error {
	if !m.Available() {
		return fmt.Errorf("nmcli not available")
	}
	if ssid == "" {
		return fmt.Errorf("ssid required")
	}
	args := []string{"device", "wifi", "connect", ssid}
	if password != "" {
		args = append(args, "password", password)
	}
	if _, err := m.run(ctx, args...); err != nil {
		return fmt.Errorf("join %q: %w", ssid, err)
	}
	// Bring AP down on success — the Pi just joined a real network.
	_, _ = m.run(ctx, "connection", "down", APConnectionName)
	return nil
}

// EnsureAPProfile creates the LittleJerrys_Config AP connection profile if
// it doesn't already exist. Idempotent.
func (m *Manager) EnsureAPProfile(ctx context.Context) error {
	if !m.Available() {
		return fmt.Errorf("nmcli not available")
	}
	out, _ := m.run(ctx, "-t", "-f", "NAME", "connection", "show")
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == APConnectionName {
			return nil
		}
	}
	// Create AP profile. The wifi-sec config is left open (no password) so
	// staff can join from a phone — captive-portal config is single-use.
	if _, err := m.run(ctx,
		"connection", "add",
		"type", "wifi",
		"ifname", "wlan0",
		"con-name", APConnectionName,
		"autoconnect", "no",
		"ssid", APssid,
		"802-11-wireless.mode", "ap",
		"802-11-wireless.band", "bg",
		"ipv4.method", "shared",
		"ipv4.addresses", APaddress+"/24",
	); err != nil {
		return fmt.Errorf("create AP profile: %w", err)
	}
	return nil
}

// StartAP brings up the AP profile.
func (m *Manager) StartAP(ctx context.Context) error {
	if !m.Available() {
		return fmt.Errorf("nmcli not available")
	}
	_, err := m.run(ctx, "connection", "up", APConnectionName)
	return err
}

// StopAP brings down the AP profile.
func (m *Manager) StopAP(ctx context.Context) error {
	if !m.Available() {
		return nil
	}
	_, err := m.run(ctx, "connection", "down", APConnectionName)
	return err
}

// APActive reports whether the LittleJerrys_Config AP profile is currently up.
func (m *Manager) APActive(ctx context.Context) bool {
	if !m.Available() {
		return false
	}
	out, err := m.run(ctx, "-t", "-f", "NAME,STATE", "connection", "show", "--active")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Split(line, ":")
		if len(parts) < 2 {
			continue
		}
		if parts[0] == APConnectionName && strings.HasPrefix(parts[1], "activated") {
			return true
		}
	}
	return false
}

// LANAddresses returns all non-loopback IPv4 addresses currently bound on
// this host. This is what staff need to point their phone at when they're
// already on the restaurant WiFi — no nmcli needed, just stdlib net.
func (m *Manager) LANAddresses() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, iface := range ifaces {
		// Skip down, loopback, and tailscale-style virtual interfaces.
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}
			out = append(out, ip4.String())
		}
	}
	return out
}

// HasInternet does a 2s connectivity check by hitting NM's connectivity URL.
func (m *Manager) HasInternet(ctx context.Context) bool {
	if !m.Available() {
		return false
	}
	out, err := m.run(ctx, "-t", "-f", "STATE", "general")
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "connected") &&
		!strings.Contains(string(out), "limited")
}

// Watcher periodically checks connectivity and toggles AP mode as needed.
// Boot flow: if no WiFi after 30s grace period → start AP. If client mode
// reconnects later → stop AP.
func (m *Manager) Watcher(ctx context.Context, gracePeriod time.Duration) {
	if !m.Available() {
		fmt.Printf("[network] nmcli unavailable — captive portal disabled\n")
		return
	}
	if err := m.EnsureAPProfile(ctx); err != nil {
		fmt.Printf("[network] ensure AP profile: %v\n", err)
	}
	timer := time.NewTimer(gracePeriod)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	for {
		if m.HasInternet(ctx) {
			_ = m.StopAP(ctx)
		} else {
			fmt.Printf("[network] no client connectivity — starting AP %s\n", APssid)
			if err := m.StartAP(ctx); err != nil {
				fmt.Printf("[network] start AP: %v\n", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(60 * time.Second):
		}
	}
}

func (m *Manager) run(ctx context.Context, args ...string) ([]byte, error) {
	if !m.Available() {
		return nil, fmt.Errorf("nmcli not available")
	}
	cmd := exec.CommandContext(ctx, m.nmcliPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("nmcli %s: %w (output: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
