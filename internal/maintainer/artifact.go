// Package maintainer is the separately installed, single-target release actuator.
// It never downloads code, builds source, runs hooks or accepts shell commands.
package maintainer

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

var revisionRE = regexp.MustCompile(`^[a-f0-9]{40}$`)
var digestRE = regexp.MustCompile(`^[a-f0-9]{64}$`)

const maxBinary = 128 * 1024 * 1024

type Manifest struct {
	Revision string `json:"revision"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

func secureFile(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0022 != 0 || info.IsDir() != directory || (!directory && !info.Mode().IsRegular()) {
		return fmt.Errorf("unsafe ownership/type/mode: %s", filepath.Base(path))
	}
	return nil
}
func verifyBuild(path, revision string) error {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return fmt.Errorf("artifact has no Go build identity")
	}
	values := map[string]string{}
	for _, s := range info.Settings {
		values[s.Key] = s.Value
	}
	if values["vcs.revision"] != revision || values["vcs.modified"] != "false" || values["GOOS"] != "linux" || values["GOARCH"] != "arm64" || info.Path != "github.com/niski84/little-jerrys/cmd/little-jerrys" {
		return fmt.Errorf("artifact is not the exact clean Channel Eleven linux/arm64 build")
	}
	return nil
}
func fileManifest(path, revision string) (Manifest, error) {
	var m Manifest
	info, err := os.Stat(path)
	if err != nil {
		return m, err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxBinary {
		return m, fmt.Errorf("invalid binary size/type")
	}
	f, err := os.Open(path)
	if err != nil {
		return m, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maxBinary+1))
	if err != nil || n != info.Size() {
		return m, fmt.Errorf("binary changed during hashing")
	}
	return Manifest{Revision: revision, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}, nil
}
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func atomicJSON(path string, value any) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".intent-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	if err := json.NewEncoder(f).Encode(value); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}
func copyVerified(source, destination string, m Manifest) error {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.CreateTemp(filepath.Dir(destination), ".binary-*")
	if err != nil {
		return err
	}
	name := out.Name()
	defer os.Remove(name)
	defer out.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(src, maxBinary+1))
	if err != nil || n != m.Size || hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
		return fmt.Errorf("artifact changed while copying")
	}
	if err := out.Chmod(0755); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, destination); err != nil {
		return err
	}
	return syncDir(filepath.Dir(destination))
}

// StageArtifact is an operator CLI action, not an HTTP endpoint. Production must
// run as root; it seals a reviewed binary's embedded clean revision and checksum.
// Existing revision slots are immutable; mismatches fail instead of overwriting.
func StageArtifact(root, source, revision string) error {
	if !revisionRE.MatchString(revision) {
		return fmt.Errorf("invalid revision")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	if err := secureFile(root, true); err != nil {
		return err
	}
	if err := secureFile(source, false); err != nil {
		return err
	}
	if err := verifyBuild(source, revision); err != nil {
		return err
	}
	manifest, err := fileManifest(source, revision)
	if err != nil {
		return err
	}
	directory := filepath.Join(root, "artifacts")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	if err := secureFile(directory, true); err != nil {
		return err
	}
	destination := filepath.Join(directory, revision)
	if err := os.Mkdir(destination, 0700); err != nil {
		return fmt.Errorf("revision slot already exists or cannot be created; inspect it, never overwrite")
	}
	// An interrupted stage leaves an unusable slot for explicit operator recovery.
	if err := copyVerified(source, filepath.Join(destination, "jerry"), manifest); err != nil {
		return err
	}
	return atomicJSON(filepath.Join(destination, "manifest.json"), manifest)
}
