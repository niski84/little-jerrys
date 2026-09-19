package jerry

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type memoryUpload struct{ *bytes.Reader }

func (memoryUpload) Close() error { return nil }

func TestConcurrentUploadsNeverOverwrite(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	paths := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			data := []byte(fmt.Sprintf("fixture-%d", i))
			path, err := saveUpload(memoryUpload{bytes.NewReader(data)}, &multipart.FileHeader{Filename: "spot.mp4"}, dir, 100, commercialUploadTypes)
			if err != nil {
				t.Error(err)
				return
			}
			actual, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(actual, data) {
				t.Error("upload overwritten")
			}
			paths <- path
		}(i)
	}
	wg.Wait()
	close(paths)
	seen := map[string]bool{}
	for path := range paths {
		if seen[path] {
			t.Error("duplicate path")
		}
		seen[path] = true
	}
	if len(seen) != 20 {
		t.Fatalf("uploads: %d", len(seen))
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".upload-*"))
	if len(leftovers) > 0 {
		t.Fatal("staging leak")
	}
}

func TestUploadRejectsTraversalOversizeAndSymlinkCollision(t *testing.T) {
	dir := t.TempDir()
	for _, test := range []struct{ name, data string }{{"../escape.mp4", "x"}, {"bad.txt", "x"}, {"large.mp4", "123456"}, {"empty.mp4", ""}} {
		if _, err := saveUpload(memoryUpload{bytes.NewReader([]byte(test.data))}, &multipart.FileHeader{Filename: test.name}, dir, 5, commercialUploadTypes); err == nil {
			t.Fatalf("accepted %s", test.name)
		}
	}
	target := filepath.Join(t.TempDir(), "original")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "spot.mp4")); err != nil {
		t.Fatal(err)
	}
	path, err := saveUpload(memoryUpload{bytes.NewReader([]byte("new"))}, &multipart.FileHeader{Filename: "spot.mp4"}, dir, 5, commercialUploadTypes)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "spot-2.mp4" {
		t.Fatal(path)
	}
	f, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil || string(data) != "keep" {
		t.Fatal("symlink target modified")
	}
}
