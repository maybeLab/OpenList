package _189pc

import "testing"

func TestCloud189DisplayNameUnescapesApostrophes(t *testing.T) {
	const want = "O'Brien's file"
	for _, object := range []interface{ GetDisplayName() string }{
		&Cloud189File{Name: "O\\'Brien\\'s file"},
		&Cloud189Folder{Name: "O\\'Brien\\'s file"},
	} {
		if got := object.GetDisplayName(); got != want {
			t.Fatalf("GetDisplayName() = %q, want %q", got, want)
		}
	}
}
