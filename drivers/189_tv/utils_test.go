package _189_tv

import "testing"

func TestHasMoreFiles(t *testing.T) {
	for _, test := range []struct {
		name     string
		pageCount int
		want      bool
	}{
		{"empty page", 0, false},
		{"partial page", 129, false},
		{"full page", 130, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := hasMoreFiles(test.pageCount, 130); got != test.want {
				t.Fatalf("hasMoreFiles(%d, 130) = %t, want %t", test.pageCount, got, test.want)
			}
		})
	}
}
