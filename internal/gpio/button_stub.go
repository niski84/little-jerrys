//go:build !pi

package gpio

import "fmt"

// newPlatformListener returns the stub on non-Pi builds. Build with `-tags pi`
// on the actual Raspberry Pi to enable the real periph.io-backed listener.
func newPlatformListener(cfg Config) Listener {
	fmt.Printf("[gpio] non-pi build — bailout button is simulated\n")
	return &stubListener{}
}
