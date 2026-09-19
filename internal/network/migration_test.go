package network

import "testing"

func TestRecoveryRouteClassification(t *testing.T) {
	for _, test := range []struct {
		route string
		want  bool
	}{
		{"1.1.1.1 via 192.168.4.1 dev wlan0 src 192.168.4.40", true},
		{"unreachable 1.1.1.1 dev lo", false}, {"", false},
		{"local 1.1.1.1 dev lo", false}, {"blackhole 1.1.1.1", false},
	} {
		if got := usableRoute(test.route); got != test.want {
			t.Fatalf("route %q = %v", test.route, got)
		}
	}
}
