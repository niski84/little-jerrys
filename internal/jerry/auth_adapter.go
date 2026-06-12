package jerry

// passwordAdapter bridges Store to auth.PasswordStore. Keeps the auth package
// free of jerry types so it can be reused by other Smash Deck apps.
type passwordAdapter struct{ s *Store }

func (p passwordAdapter) GetHash() (string, bool) {
	st := p.s.Get()
	return st.AdminPasswordHash, st.PasswordIsDefault
}

func (p passwordAdapter) SetHash(hash string, mustChange bool) error {
	return p.s.Update(func(st *State) {
		st.AdminPasswordHash = hash
		st.PasswordIsDefault = mustChange
	})
}
