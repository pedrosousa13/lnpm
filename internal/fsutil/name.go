package fsutil

// IsLowerHex reports whether s is a non-empty run of lowercase hex digits.
//
// It lives here because both the store and the linker recognise the temporary
// directories they created by the shape of the name, and both names carry a
// lowercase hex run: fmt's %x verb over a uint64 in the linker, and pack's
// content hash in the store. One definition means the two sweeps cannot drift
// into disagreeing about what counts as one of their own names.
func IsLowerHex(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
