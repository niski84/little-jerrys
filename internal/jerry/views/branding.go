package views

// Branding pulls per-tenant identity out of the templates so the same
// binary can ship under different names. Set once by main.go / app
// initialization (after reading the USB jerry.conf file) and read by
// every layout / nav / title.
//
// Defaults live here so a stock build still renders something coherent
// even if no jerry.conf is on the USB.
var (
	// BrandTenantName is the display name shown in nav, page titles, and
	// log lines. Default is the generic project name; per-venue operators
	// override via TENANT_NAME in jerry.conf on the USB drive.
	BrandTenantName = "Channel 14"
	BrandLogoURL    = "/static/img/logo.png"
)

// BrandTitle rewrites a hardcoded view title to use the configured tenant
// name. Templates pass `Layout("Little Jerry's — Now Playing", …)` and
// this helper swaps in whatever the operator put in jerry.conf, so we
// don't have to thread the brand string through every view.
func BrandTitle(title string) string {
	if BrandTenantName == "Little Jerry's" {
		return title
	}
	// Replace anywhere — covers the page title in <title>, the meta
	// description, and any inline copy that uses the same prefix.
	return replaceFirst(title, "Little Jerry's", BrandTenantName)
}

func replaceFirst(s, from, to string) string {
	idx := indexOf(s, from)
	if idx < 0 {
		return s
	}
	return s[:idx] + to + s[idx+len(from):]
}

func indexOf(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
