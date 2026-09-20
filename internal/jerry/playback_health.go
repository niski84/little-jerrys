package jerry

import "github.com/niski84/little-jerrys/internal/player"

// Readiness uses cached player observations, never synchronous IPC in HTTP.
// Liveness remains available independently so an external watchdog does not
// fight the player's bounded recovery policy by repeatedly restarting the app.
func playbackReadiness(app *App) (player.PlaybackHealth, bool) {
	observed, ok := app.Player.(interface{ PlaybackHealth() player.PlaybackHealth })
	if !ok {
		return player.PlaybackHealth{State: "unverified", Reason: "no real playback progress observations"}, false
	}
	health := observed.PlaybackHealth()
	switch health.State {
	case "playing", "paused", "slideshow", "static":
		return health, true
	default:
		return health, false
	}
}
