package jerry

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

const (
	maxSoundUpload      = 100 << 20 // 100 MiB
	maxCommercialUpload = 1 << 30   // 1 GiB
)

var safeUploadName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

type uploadError struct {
	invalid bool
	err     error
}

func (e *uploadError) Error() string { return e.err.Error() }
func (e *uploadError) Unwrap() error { return e.err }

func invalidUpload(err error) error { return &uploadError{invalid: true, err: err} }
func storageUpload(err error) error { return &uploadError{err: err} }

func requestUploadError(err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return storageUpload(err)
	}
	return invalidUpload(err)
}

func isInvalidUpload(err error) bool {
	var uploadErr *uploadError
	return errors.As(err, &uploadErr) && uploadErr.invalid
}

func saveUpload(file multipart.File, header *multipart.FileHeader, dir string, maxBytes int64, allowed map[string]bool) (string, error) {
	defer file.Close()
	name := filepath.Base(header.Filename)
	if name == "." || name == "" || name != header.Filename {
		return "", invalidUpload(fmt.Errorf("invalid filename"))
	}
	name = safeUploadName.ReplaceAllString(name, "-")
	name = strings.Trim(name, ".-")
	ext := strings.ToLower(filepath.Ext(name))
	if name == "" || !allowed[ext] {
		return "", invalidUpload(fmt.Errorf("unsupported file type %q", ext))
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", storageUpload(fmt.Errorf("create upload directory: %w", err))
	}
	// Publish with an atomic no-replace operation below: a preflight Stat plus
	// Rename would overwrite a concurrent upload with the same name.
	base := strings.TrimSuffix(name, filepath.Ext(name))

	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return "", storageUpload(fmt.Errorf("create upload staging file: %w", err))
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	n, copyErr := io.Copy(tmp, io.LimitReader(file, maxBytes+1))
	if copyErr != nil {
		tmp.Close()
		return "", storageUpload(fmt.Errorf("write upload: %w", copyErr))
	}
	if n == 0 || n > maxBytes {
		tmp.Close()
		return "", invalidUpload(fmt.Errorf("file is empty or exceeds the upload limit"))
	}
	if err := tmp.Chmod(0644); err != nil {
		tmp.Close()
		return "", storageUpload(fmt.Errorf("set upload permissions: %w", err))
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", storageUpload(fmt.Errorf("sync upload: %w", err))
	}
	if err := tmp.Close(); err != nil {
		return "", storageUpload(err)
	}
	for n := 1; n <= 10000; n++ {
		candidate := name
		if n > 1 {
			candidate = fmt.Sprintf("%s-%d%s", base, n, ext)
		}
		path := filepath.Join(dir, candidate)
		if err := publishUpload(tmpPath, path); err == nil {
			return path, nil
		} else if !os.IsExist(err) {
			return "", storageUpload(fmt.Errorf("finish upload: %w", err))
		}
	}
	return "", storageUpload(fmt.Errorf("too many uploads with the same name"))
}

func uploadFile(r *http.Request, field, dir string, maxBytes int64, allowed map[string]bool) (string, error) {
	// ParseMultipartForm's argument limits in-memory buffering, not the total
	// request body. Cap the body too, so a bad client cannot fill the Pi disk.
	r.Body = http.MaxBytesReader(nil, r.Body, maxBytes+(1<<20))
	defer func() {
		if r.MultipartForm != nil {
			if err := r.MultipartForm.RemoveAll(); err != nil {
				fmt.Printf("[upload] temporary file cleanup: %v\n", err)
			}
		}
	}()
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		return "", requestUploadError(fmt.Errorf("read upload: %w", err))
	}
	if err := validation.Validate(strings.TrimSpace(r.FormValue("campaign")), validation.Length(0, 120)); err != nil {
		return "", invalidUpload(fmt.Errorf("campaign name: %w", err))
	}
	file, header, err := r.FormFile(field)
	if err != nil {
		return "", requestUploadError(fmt.Errorf("choose a file: %w", err))
	}
	return saveUpload(file, header, dir, maxBytes, allowed)
}

func handleSoundboardUpload(w http.ResponseWriter, r *http.Request, dir string) {
	_, err := uploadFile(r, "sound_file", dir, maxSoundUpload, soundUploadTypes)
	if err != nil {
		if isInvalidUpload(err) {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		fmt.Printf("[upload] soundboard storage failure: %v\n", err)
		http.Error(w, "upload could not be saved", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/soundboard", http.StatusSeeOther)
}

var soundUploadTypes = map[string]bool{".mp3": true, ".ogg": true, ".wav": true, ".m4a": true, ".flac": true}
var commercialUploadTypes = map[string]bool{".mp4": true, ".mkv": true, ".mov": true, ".avi": true, ".m4v": true, ".webm": true}
