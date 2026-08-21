package thumbnail

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/sign"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

func loopbackBaseURL() (string, error) {
	if conf.Conf.Scheme.HttpPort == -1 {
		return "", fmt.Errorf("HTTP listener is disabled; HTTPS-only and Unix-socket deployments are not supported")
	}
	if conf.Conf.Scheme.ForceHttps && conf.Conf.Scheme.HttpsPort != -1 {
		return "", fmt.Errorf("ForceHTTPS redirects the internal HTTP endpoint")
	}
	host := strings.TrimSpace(conf.Conf.Scheme.Address)
	switch host {
	case "", "0.0.0.0", "::", "[::]", "localhost", "::1", "[::1]":
		host = "127.0.0.1"
	default:
		if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil && ip.IsUnspecified() {
			host = "127.0.0.1"
		}
	}
	basePath := ""
	if conf.URL != nil && conf.URL.Path != "/" {
		basePath = strings.TrimSuffix(conf.URL.Path, "/")
	}
	return "http://" + net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(conf.Conf.Scheme.HttpPort)) + basePath, nil
}

func SourceURL(visiblePath string) (string, error) {
	base, err := loopbackBaseURL()
	if err != nil {
		return "", err
	}
	query := url.Values{}
	query.Set("d", "1")
	query.Set("sign", sign.Sign(visiblePath))
	return base + "/p" + utils.EncodePath(visiblePath, true) + "?" + query.Encode(), nil
}

func checkLoopback(ctx context.Context) CapabilityItem {
	base, err := loopbackBaseURL()
	if err != nil {
		return CapabilityItem{Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/ping", nil)
	if err != nil {
		return CapabilityItem{Error: err.Error()}
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Do(req)
	if err != nil {
		return CapabilityItem{Error: fmt.Sprintf("loopback listener is unreachable: %v", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return CapabilityItem{Error: fmt.Sprintf("loopback health check returned %s", resp.Status)}
	}
	return CapabilityItem{Available: true, Version: base}
}
