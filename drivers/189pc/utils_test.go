package _189pc

import "testing"

func TestHasMoreFiles(t *testing.T) {
	for _, test := range []struct {
		name      string
		pageCount int
		want      bool
	}{
		{"empty page", 0, false},
		{"partial page", 999, false},
		{"full page", 1000, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := hasMoreFiles(test.pageCount, 1000); got != test.want {
				t.Fatalf("hasMoreFiles(%d, 1000) = %t, want %t", test.pageCount, got, test.want)
			}
		})
	}
}
