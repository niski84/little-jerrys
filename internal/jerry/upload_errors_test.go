package jerry

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func soundboardUploadRequest(t *testing.T, filename string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("sound_file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("audio")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/soundboard/upload", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	return request
}

func TestSoundboardUploadErrorClassification(t *testing.T) {
	t.Run("invalid input is unprocessable", func(t *testing.T) {
		response := httptest.NewRecorder()
		handleSoundboardUpload(response, soundboardUploadRequest(t, "not-audio.txt"), filepath.Join(t.TempDir(), "sounds"))
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusUnprocessableEntity, response.Body.String())
		}
	})

	t.Run("storage failure is internal and hides filesystem details", func(t *testing.T) {
		root := t.TempDir()
		blocker := filepath.Join(root, "not-a-directory")
		if err := os.WriteFile(blocker, []byte("block"), 0600); err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		handleSoundboardUpload(response, soundboardUploadRequest(t, "valid.mp3"), filepath.Join(blocker, "sounds"))
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusInternalServerError, response.Body.String())
		}
		if strings.Contains(response.Body.String(), root) || strings.Contains(response.Body.String(), "not a directory") {
			t.Fatalf("response leaked storage details: %q", response.Body.String())
		}
		if got := strings.TrimSpace(response.Body.String()); got != "upload could not be saved" {
			t.Fatalf("body = %q, want generic storage error", got)
		}
	})
}

func TestSoundboardUploadMultipartSpillFailureIsStorageError(t *testing.T) {
	root := t.TempDir()
	// Multipart parsing spills files larger than its 8 MiB memory budget to
	// TMPDIR. Point it at a missing directory to deterministically fail that
	// private temporary-file creation without changing process state afterward.
	invalidTempDir := filepath.Join(root, "missing-temp-dir")
	t.Setenv("TMPDIR", invalidTempDir)

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("sound_file", "large.mp3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("x"), (8<<20)+1)); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/soundboard/upload", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	handleSoundboardUpload(response, request, filepath.Join(root, "sounds"))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if strings.Contains(response.Body.String(), root) || strings.Contains(response.Body.String(), "missing-temp-dir") {
		t.Fatalf("response leaked temporary storage details: %q", response.Body.String())
	}
	if got := strings.TrimSpace(response.Body.String()); got != "upload could not be saved" {
		t.Fatalf("body = %q, want generic storage error", got)
	}
}
