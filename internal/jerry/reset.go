package jerry

import "fmt"

// ResetPassword wipes the stored admin password hash and re-arms the
// must-change-on-first-login flag. Run from the CLI when an operator gets
// locked out — typically a Pi shipped to a new restaurant where the previous
// admin's password is unknown. The next login round-trips through the
// forced-password-change flow.
func ResetPassword(statePath string) error {
	store, err := NewStore(statePath)
	if err != nil {
		return fmt.Errorf("open state: %w", err)
	}
	return store.Update(func(s *State) {
		s.AdminPasswordHash = ""
		s.PasswordIsDefault = true
	})
}
