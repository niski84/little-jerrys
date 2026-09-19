//go:build !linux

package jerry

import "os"

func publishUpload(staging, destination string) error {
	return os.Link(staging, destination)
}
