package auth

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

const testKey = "test-signing-key-do-not-use-in-production"

// memStore is an in-memory PasswordStore for tests.
type memStore struct {
	mu         sync.Mutex
	hash       string
	mustChange bool
}

func (m *memStore) GetHash() (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hash, m.mustChange
}
func (m *memStore) SetHash(h string, must bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hash = h
	m.mustChange = must
	return nil
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := New(&memStore{}, testKey)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSeedDefaultPasswordOnFirstBoot(t *testing.T) {
	m := newTestManager(t)
	if !m.Verify(DefaultUser, DefaultPassword) {
		t.Fatal("default admin/admin should verify after seeding")
	}
	if !m.MustChange() {
		t.Fatal("must-change flag should be set on fresh install")
	}
}

func TestChangePasswordRejectsShortAndDefault(t *testing.T) {
	m := newTestManager(t)
	if err := m.ChangePassword("short"); err == nil {
		t.Error("expected error for short password")
	}
	if err := m.ChangePassword(DefaultPassword); err == nil {
		t.Error("expected error for reusing default password")
	}
	if err := m.ChangePassword("a-real-password"); err != nil {
		t.Fatalf("valid password should succeed: %v", err)
	}
	if m.MustChange() {
		t.Error("must-change should clear after successful change")
	}
}

func TestLoginCookieRoundTrip(t *testing.T) {
	m := newTestManager(t)
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	m.Login(w, r)

	// Replay the cookie on a fresh request.
	res := w.Result()
	if len(res.Cookies()) == 0 {
		t.Fatal("expected session cookie after login")
	}
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.AddCookie(res.Cookies()[0])
	if !m.IsAuthed(r2) {
		t.Fatal("session should be valid right after login")
	}

	// Logout clears the cookie; IsAuthed still returns false for the old request
	// because the cookie is gone from r2 (it had the old cookie value).
	w2 := httptest.NewRecorder()
	m.Logout(w2, r2)
	// A new request without a cookie must not be authed.
	r3 := httptest.NewRequest("GET", "/", nil)
	if m.IsAuthed(r3) {
		t.Fatal("unauthenticated request should not be authed")
	}
}

func TestTamperedCookieRejected(t *testing.T) {
	m := newTestManager(t)
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	m.Login(w, r)

	cookie := w.Result().Cookies()[0]
	// Flip a character in the signature.
	v := cookie.Value
	tampered := v[:len(v)-1] + "X"
	cookie.Value = tampered

	r2 := httptest.NewRequest("GET", "/", nil)
	r2.AddCookie(cookie)
	if m.IsAuthed(r2) {
		t.Fatal("tampered cookie must be rejected")
	}
}

func TestRequireRedirectsHTMLAnd401sJSON(t *testing.T) {
	m := newTestManager(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := m.Require(next)

	htmlReq := httptest.NewRequest("GET", "/", nil)
	htmlReq.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, htmlReq)
	if w.Code != http.StatusSeeOther {
		t.Errorf("HTML unauth: want 303, got %d", w.Code)
	}

	apiReq := httptest.NewRequest("GET", "/", nil)
	apiReq.Header.Set("Accept", "application/json")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, apiReq)
	if w2.Code != http.StatusUnauthorized {
		t.Errorf("API unauth: want 401, got %d", w2.Code)
	}
}

func TestRescuePasswordVerifiesEvenWhenWrongHashStored(t *testing.T) {
	m := newTestManager(t)
	_ = m.ChangePassword("a-real-password")

	if m.Verify(DefaultUser, "rescue-me") {
		t.Fatal("rescue should not work before SetRescuePassword")
	}
	m.SetRescuePassword("rescue-me")
	if !m.Verify(DefaultUser, "rescue-me") {
		t.Fatal("rescue password should verify after SetRescuePassword")
	}
	if !m.Verify(DefaultUser, "a-real-password") {
		t.Fatal("regular password should still verify with rescue installed")
	}
	if m.Verify(DefaultUser, "definitely-wrong") {
		t.Fatal("unrelated wrong password should not verify")
	}
	m.SetRescuePassword("")
	if m.Verify(DefaultUser, "rescue-me") {
		t.Fatal("rescue should disable when password set to empty")
	}
}

func TestIsRescueDifferentiatesLogPath(t *testing.T) {
	m := newTestManager(t)
	m.SetRescuePassword("rescue-me")

	if !m.IsRescue("rescue-me") {
		t.Error("IsRescue should report true for rescue password")
	}
	if m.IsRescue("not-the-rescue") {
		t.Error("IsRescue should report false for any other password")
	}
}

func TestLockedDuringDefaultBlocksUntilChanged(t *testing.T) {
	m := newTestManager(t)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	h := m.LockedDuringDefault(next)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/skip", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("blocked path: want 403, got %d", w.Code)
	}
	if called {
		t.Error("downstream handler should not run while default")
	}

	_ = m.ChangePassword("a-real-password")
	w2 := httptest.NewRecorder()
	called = false
	h.ServeHTTP(w2, httptest.NewRequest("POST", "/api/skip", nil))
	if !called {
		t.Error("downstream should run after password change")
	}
}
