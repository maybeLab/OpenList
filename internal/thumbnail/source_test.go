package thumbnail

import (
	"net/url"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
)

func withTestConfig(t *testing.T, mutate func(*conf.Config)) {
	t.Helper()
	oldConf, oldURL := conf.Conf, conf.URL
	conf.Conf = conf.DefaultConfig(t.TempDir())
	conf.URL = &url.URL{Path: "/"}
	mutate(conf.Conf)
	t.Cleanup(func() {
		conf.Conf, conf.URL = oldConf, oldURL
	})
}

func TestLoopbackBaseURL(t *testing.T) {
	withTestConfig(t, func(config *conf.Config) {
		config.Scheme.Address = "0.0.0.0"
		config.Scheme.HttpPort = 15244
		conf.URL = &url.URL{Path: "/base/"}
	})
	got, err := loopbackBaseURL()
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://127.0.0.1:15244/base" {
		t.Fatalf("loopbackBaseURL() = %q", got)
	}
}

func TestLoopbackBaseURLRejectsUnsupportedSchemes(t *testing.T) {
	t.Run("http disabled", func(t *testing.T) {
		withTestConfig(t, func(config *conf.Config) { config.Scheme.HttpPort = -1 })
		if _, err := loopbackBaseURL(); err == nil || !strings.Contains(err.Error(), "HTTP listener is disabled") {
			t.Fatalf("loopbackBaseURL() error = %v", err)
		}
	})
	t.Run("force https", func(t *testing.T) {
		withTestConfig(t, func(config *conf.Config) {
			config.Scheme.HttpPort = 15244
			config.Scheme.HttpsPort = 15245
			config.Scheme.ForceHttps = true
		})
		if _, err := loopbackBaseURL(); err == nil || !strings.Contains(err.Error(), "ForceHTTPS") {
			t.Fatalf("loopbackBaseURL() error = %v", err)
		}
	})
}

func TestSourceURLUsesSignedProxyPath(t *testing.T) {
	withTestConfig(t, func(config *conf.Config) { config.Scheme.HttpPort = 15244 })
	got, err := SourceURL("/crypt/a b#c?.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "http://127.0.0.1:15244/p/crypt/a%20b%23c%3F.mp4?") {
		t.Fatalf("SourceURL() = %q", got)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("d") != "1" || parsed.Query().Get("sign") == "" {
		t.Fatalf("missing required query values in %q", got)
	}
}
