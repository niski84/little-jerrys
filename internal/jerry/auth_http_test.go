package jerry

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// TestRescuePasswordAtHTTPBoundary verifies the operator recovery path all
// the way through the login handler. This is intentionally isolated from the
// live appliance: a rescue password must never be installed there by tests.
func TestRescuePasswordAtHTTPBoundary(t *testing.T) {
	mediaRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(mediaRoot, "s01e01_test.mp4"), []byte("episode"), 0644); err != nil {
		t.Fatal(err)
	}

	app, err := NewApp(context.Background(), Config{
		MediaRoot:        mediaRoot,
		StatePath:        filepath.Join(t.TempDir(), "state.json"),
		RescuePassword:   "rescue-test-password",
		UsePlayerStub:    true,
		UseGPIOSimulated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	// Make the normal credential known and non-default. Rescue must still work
	// when the operator has forgotten that credential.
	if err := app.Auth.ChangePassword("normal-test-password"); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewServer(app))
	defer srv.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := srv.Client()
	client.Jar = jar
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := client.PostForm(srv.URL+"/login", url.Values{
		"user":     {"admin"},
		"password": {"not-the-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		resp.Body.Close()
		t.Fatalf("wrong password status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	resp, err = client.PostForm(srv.URL+"/login", url.Values{
		"user":     {"admin"},
		"password": {"rescue-test-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		resp.Body.Close()
		t.Fatalf("rescue login status = %d, want redirect", resp.StatusCode)
	}
	resp.Body.Close()

	resp, err = client.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("dashboard after rescue login status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
}
