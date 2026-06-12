//go:build pi

// Real Raspberry Pi GPIO implementation. Build with: go build -tags pi
//
// Wiring: connect one terminal of the arcade button to a Ground pin and the
// other to GPIO 17 (default). The stub listener pulls the pin high; pressing
// the button shorts it to ground (falling edge).

package gpio

import (
	"context"
	"fmt"
	"sync"
	"time"

	"periph.io/x/conn/v3/gpio"
	"periph.io/x/conn/v3/gpio/gpioreg"
	"periph.io/x/host/v3"
)

type piListener struct {
	cfg     Config
	pin     gpio.PinIO
	mu      sync.Mutex
	cancel  context.CancelFunc
	onPress func()
	last    time.Time
}

func newPlatformListener(cfg Config) Listener {
	return &piListener{cfg: cfg}
}

func (p *piListener) Start(ctx context.Context, onPress func()) error {
	if _, err := host.Init(); err != nil {
		return fmt.Errorf("init periph host: %w", err)
	}
	pinName := fmt.Sprintf("GPIO%d", p.cfg.Pin)
	pin := gpioreg.ByName(pinName)
	if pin == nil {
		return fmt.Errorf("gpio pin %s not found", pinName)
	}
	pull := gpio.PullUp
	if !p.cfg.PullUp {
		pull = gpio.PullDown
	}
	if err := pin.In(pull, gpio.FallingEdge); err != nil {
		return fmt.Errorf("configure pin: %w", err)
	}
	p.pin = pin
	p.onPress = onPress

	ctx, cancel := context.WithCancel(ctx)
	p.cancel = cancel

	go func() {
		for {
			if ctx.Err() != nil {
				return
			}
			if !pin.WaitForEdge(time.Second) {
				continue
			}
			p.mu.Lock()
			if time.Since(p.last) < p.cfg.Debounce {
				p.mu.Unlock()
				continue
			}
			p.last = time.Now()
			cb := p.onPress
			p.mu.Unlock()
			if cb != nil {
				cb()
			}
		}
	}()
	return nil
}

func (p *piListener) Close() error {
	if p.cancel != nil {
		p.cancel()
	}
	return nil
}
