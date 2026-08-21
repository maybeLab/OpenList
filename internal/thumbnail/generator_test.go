package thumbnail

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const onePixelWebP = "UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEAAUAmJaQAA3AA/vuUAAA="

func TestValidateWebP(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "valid.webp")
	data, err := base64.StdEncoding.DecodeString(onePixelWebP)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(valid, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWebP(valid); err != nil {
		t.Fatalf("ValidateWebP(valid) error = %v", err)
	}

	tests := map[string][]byte{
		"empty.webp":     nil,
		"not-webp.webp":  []byte("not a webp file"),
		"truncated.webp": data[:12],
	}
	for name, content := range tests {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := ValidateWebP(path); err == nil {
			t.Errorf("ValidateWebP(%s) unexpectedly succeeded", name)
		}
	}
}

func TestCommandEnvironmentForcesLoopbackNoProxy(t *testing.T) {
	t.Setenv("NO_PROXY", "example.com")
	t.Setenv("no_proxy", "example.net")
	env := commandEnvironment()
	var upper, lower []string
	for _, entry := range env {
		switch {
		case strings.HasPrefix(entry, "NO_PROXY="):
			upper = append(upper, entry)
		case strings.HasPrefix(entry, "no_proxy="):
			lower = append(lower, entry)
		}
	}
	const expected = "127.0.0.1,localhost,::1,openlist-thumbnail-dev"
	if len(upper) != 1 || upper[0] != "NO_PROXY="+expected {
		t.Fatalf("NO_PROXY entries = %q", upper)
	}
	if len(lower) != 1 || lower[0] != "no_proxy="+expected {
		t.Fatalf("no_proxy entries = %q", lower)
	}
}
