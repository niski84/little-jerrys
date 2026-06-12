// Package web embeds all static assets (CSS, fan art, fonts) into the Go
// binary so the deployed Pi has zero external file dependencies.
package web

import (
	"embed"
	"io/fs"
)

//go:embed jerry
var FS embed.FS

// StaticFS returns the embedded /static directory rooted at the inner folder
// so paths like /static/app.css resolve correctly.
func StaticFS() (fs.FS, error) {
	return fs.Sub(FS, "jerry/static")
}
