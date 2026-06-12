package views

import "strconv"

// weightStr returns the current weight as a string, defaulting to "1".
func weightStr(weights map[string]int, path string) string {
	if w, ok := weights[path]; ok && w > 1 {
		return strconv.Itoa(w)
	}
	return "1"
}

func intStr(n int) string { return strconv.Itoa(n) }

// fmtPosition returns mm:ss for a float seconds value.
func fmtPosition(secs float64) string {
	if secs < 0 {
		secs = 0
	}
	total := int(secs)
	mins := total / 60
	s := total % 60
	return strconv.Itoa(mins) + ":" + pad2(s)
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// pausedStyle hides the "Paused" indicator on initial render when not
// paused; the polling script unhides it whenever the player reports paused.
func pausedStyle(paused bool) string {
	if paused {
		return ""
	}
	return "display:none"
}
