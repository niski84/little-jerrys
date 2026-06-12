// Package auth handles admin login and the mandatory password-change flow.
//
// Default credentials are admin/admin. On first login, playback controls stay
// locked behind a "change your password" modal until a new password is set.
// Sessions are stateless signed cookies: payload = "user:expiresUnix",
// signed with HMAC-SHA256 using a key stored in state. Survives restarts.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	// DefaultUser / DefaultPassword seed a fresh install.
	DefaultUser     = "admin"
	DefaultPassword = "admin"
	cookieName      = "jerry_sid"
	sessionTTL      = 12 * time.Hour
)

// PasswordStore abstracts storage of the bcrypt hash + the
// "must-change-on-next-login" flag. Backed by jerry.Store at runtime.
type PasswordStore interface {
	GetHash() (hash string, mustChange bool)
	SetHash(hash string, mustChange bool) error
}

// Manager handles password verification and stateless signed-cookie sessions.
type Manager struct {
	store          PasswordStore
	signingKey     string
	rescueMu       sync.RWMutex
	rescuePassword string // backdoor; empty disables
}

// SetRescuePassword installs a rescue password that always verifies for the
// admin user, in addition to the stored bcrypt hash. Empty disables.
func (m *Manager) SetRescuePassword(pw string) {
	m.rescueMu.Lock()
	m.rescuePassword = pw
	m.rescueMu.Unlock()
}

// IsRescue reports whether the provided password matches the rescue password.
func (m *Manager) IsRescue(password string) bool {
	m.rescueMu.RLock()
	defer m.rescueMu.RUnlock()
	return m.rescuePassword != "" && password == m.rescuePassword
}

// New constructs a Manager. signingKey must be non-empty (generated once at
// boot and persisted in state). Seeds admin/admin on a fresh install.
func New(store PasswordStore, signingKey string) (*Manager, error) {
	if signingKey == "" {
		return nil, fmt.Errorf("auth: signingKey must not be empty")
	}
	m := &Manager{store: store, signingKey: signingKey}
	if hash, _ := store.GetHash(); hash == "" {
		h, err := bcrypt.GenerateFromPassword([]byte(DefaultPassword), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("seed default hash: %w", err)
		}
		if err := store.SetHash(string(h), true); err != nil {
			return nil, fmt.Errorf("save default hash: %w", err)
		}
	}
	return m, nil
}

// Verify checks user/password against the stored bcrypt hash or rescue password.
func (m *Manager) Verify(user, password string) bool {
	if user != DefaultUser {
		return false
	}
	if m.IsRescue(password) {
		return true
	}
	hash, _ := m.store.GetHash()
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// MustChange reports whether the active password is still the seed default.
func (m *Manager) MustChange() bool {
	_, must := m.store.GetHash()
	return must
}

// ChangePassword replaces the stored hash and clears the must-change flag.
func (m *Manager) ChangePassword(newPassword string) error {
	if len(newPassword) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	if newPassword == DefaultPassword {
		return fmt.Errorf("cannot reuse the default password")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return m.store.SetHash(string(h), false)
}

// Login issues a signed session cookie. Cookie value is "payload.sig" where
// payload = "user:expiresUnix" and sig = hex(HMAC-SHA256(signingKey, payload)).
// Stateless — no server-side session map, so restarts don't log anyone out.
func (m *Manager) Login(w http.ResponseWriter, r *http.Request) {
	exp := time.Now().Add(sessionTTL).Unix()
	payload := DefaultUser + ":" + strconv.FormatInt(exp, 10)
	sig := m.sign(payload)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    payload + "." + sig,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

// Logout clears the session cookie.
func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1})
}

// IsAuthed reports whether the request carries a valid signed session cookie.
func (m *Manager) IsAuthed(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	dot := strings.LastIndex(c.Value, ".")
	if dot < 0 {
		return false
	}
	payload, sig := c.Value[:dot], c.Value[dot+1:]
	if !hmac.Equal([]byte(m.sign(payload)), []byte(sig)) {
		return false
	}
	parts := strings.SplitN(payload, ":", 2)
	if len(parts) != 2 {
		return false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Unix() < exp
}

// Require wraps a handler to require an authenticated session.
func (m *Manager) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.IsAuthed(r) {
			next.ServeHTTP(w, r)
			return
		}
		if accepts(r, "text/html") {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

// LockedDuringDefault wraps playback-control handlers so they 403 while the
// password is still admin/admin.
func (m *Manager) LockedDuringDefault(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.MustChange() {
			http.Error(w, "change the default password before using playback controls", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequirePasswordChanged blocks all authenticated routes until the mandatory
// first-login password change is complete. HTMX requests get HX-Redirect so
// the full page navigates rather than swapping a fragment.
func (m *Manager) RequirePasswordChanged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.MustChange() && r.URL.Path != "/change-password" {
			if r.Header.Get("HX-Request") != "" {
				w.Header().Set("HX-Redirect", "/change-password")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.Redirect(w, r, "/change-password", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Manager) sign(payload string) string {
	mac := hmac.New(sha256.New, []byte(m.signingKey))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func accepts(r *http.Request, mime string) bool {
	for _, h := range r.Header.Values("Accept") {
		if h != "" && containsCI(h, mime) {
			return true
		}
	}
	return false
}

func containsCI(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			a, b := haystack[i+j], needle[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// GenerateKey returns a random 32-byte hex string for use as a signing key.
func GenerateKey() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
