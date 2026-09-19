package jerry

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUploadsAndCampaignsEndToEnd exercises the same HTTP routes used by the
// browser: login, unlock, upload, rescan, campaign management, and playback
// status. It intentionally uses tiny fake media files because the catalog only
// needs the extensions during this boundary test.
func TestUploadsAndCampaignsEndToEnd(t *testing.T) {
	mediaRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(mediaRoot, "s01e01_test.mp4"), []byte("episode"), 0644); err != nil {
		t.Fatal(err)
	}
	app, err := NewApp(context.Background(), Config{
		MediaRoot:        mediaRoot,
		StatePath:        filepath.Join(t.TempDir(), "state.json"),
		UsePlayerStub:    true,
		UseGPIOSimulated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	srv := httptest.NewServer(NewServer(app))
	defer srv.Close()
	client, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	hc := srv.Client()
	hc.Jar = client
	hc.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	// Mutations must not be available anonymously.
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/soundboard/upload", strings.NewReader("nope"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/html")
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		resp.Body.Close()
		t.Fatalf("anonymous upload status = %d, want redirect to login", resp.StatusCode)
	}
	resp.Body.Close()

	form := url.Values{"user": {"admin"}, "password": {"admin"}}
	resp, err = hc.PostForm(srv.URL+"/login", form)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		resp.Body.Close()
		t.Fatalf("login status = %d, want redirect", resp.StatusCode)
	}
	resp.Body.Close()

	// Fresh installs require the password to be changed before mutations.
	resp, err = hc.PostForm(srv.URL+"/change-password", url.Values{
		"password": {"end-to-end-test-password"},
		"confirm":  {"end-to-end-test-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		resp.Body.Close()
		t.Fatalf("password change status = %d, want redirect", resp.StatusCode)
	}
	resp.Body.Close()

	resp, err = postMultipart(hc, srv.URL+"/soundboard/upload", "sound_file", "blue-test-tone.mp3", []byte("audio"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		resp.Body.Close()
		t.Fatalf("sound upload status = %d, want redirect", resp.StatusCode)
	}
	resp.Body.Close()
	if _, err := os.Stat(filepath.Join(mediaRoot, "sounds", "blue-test-tone.mp3")); err != nil {
		t.Fatalf("uploaded sound not persisted: %v", err)
	}

	resp, err = postMultipart(hc, srv.URL+"/commercials/upload", "commercial_file", "jerry-cola.mp4", []byte("commercial"), map[string]string{"campaign": "Jerry Cola"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		resp.Body.Close()
		t.Fatalf("commercial upload status = %d, want redirect", resp.StatusCode)
	}
	resp.Body.Close()
	commercialPath := filepath.Join(mediaRoot, "commercials", "jerry-cola.mp4")
	if _, err := os.Stat(commercialPath); err != nil {
		t.Fatalf("uploaded commercial not persisted: %v", err)
	}

	st := app.Store.Get()
	if len(st.CommercialCampaigns) != 1 || len(st.CommercialCampaigns[0].Files) != 1 || st.CommercialCampaigns[0].Files[0] != commercialPath {
		t.Fatalf("campaign state = %#v, want one campaign containing uploaded spot", st.CommercialCampaigns)
	}
	if got := app.Playlist.CommercialCatalog(); len(got) != 1 || got[0] != commercialPath {
		t.Fatalf("commercial catalog = %#v, want uploaded spot", got)
	}

	// Save campaign controls through the browser route and verify the
	// campaign state changes are persisted.
	id := st.CommercialCampaigns[0].ID
	resp, err = hc.PostForm(srv.URL+"/commercials/campaigns/save", url.Values{
		"ce:" + id:   {"1"},
		"cwmp:" + id: {"3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := app.Store.Get().CommercialCampaigns[0].Weight; got != 3 {
		t.Fatalf("campaign weight = %d, want 3", got)
	}

	resp, err = hc.PostForm(srv.URL+"/commercials/campaigns/save", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if app.Store.Get().CommercialCampaigns[0].Enabled {
		t.Fatal("campaign should be disabled when its checkbox is omitted")
	}

	// The playback API remains available after the same login flow.
	resp, err = hc.Post(srv.URL+"/api/play", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		resp.Body.Close()
		t.Fatalf("play API status = %d, want 204", resp.StatusCode)
	}
	resp.Body.Close()
	resp, err = hc.PostForm(srv.URL+"/api/volume", url.Values{"level": {"7"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		resp.Body.Close()
		t.Fatalf("volume API status = %d, want 204", resp.StatusCode)
	}
	resp.Body.Close()
	if got := app.Store.Get().VolumeLevel; got != 7 {
		t.Fatalf("saved volume level = %d, want 7", got)
	}
	resp, err = hc.Get(srv.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("status API = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
}

func postMultipart(client *http.Client, endpoint, field, filename string, data []byte, values map[string]string) (*http.Response, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range values {
		if err := writer.WriteField(key, value); err != nil {
			return nil, err
		}
	}
	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, bytes.NewReader(data)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return client.Post(endpoint, writer.FormDataContentType(), &body)
}
