//go:build windows

package panel

import (
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// System mode supports environment variables and Windows manual proxy settings.
// PAC scripts are intentionally not evaluated by the native downloader.
func systemProxy(req *http.Request) (*url.URL, error) {
	if p, err := http.ProxyFromEnvironment(req); p != nil || err != nil {
		return p, err
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if os.Getenv(key) != "" {
			return nil, nil
		}
	}
	host := req.URL.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil, nil
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return nil, nil
	}
	defer key.Close()
	enabled, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil || enabled == 0 {
		return nil, nil
	}
	raw, _, err := key.GetStringValue("ProxyServer")
	if err != nil || raw == "" {
		return nil, nil
	}
	bypass, _, _ := key.GetStringValue("ProxyOverride")
	for _, item := range strings.Split(bypass, ";") {
		item = strings.TrimSpace(item)
		if item == host || item == "<local>" && !strings.Contains(host, ".") || strings.HasPrefix(item, "*.") && strings.HasSuffix(host, item[1:]) {
			return nil, nil
		}
	}
	selected := raw
	if strings.Contains(raw, "=") {
		selected = ""
		for _, entry := range strings.Split(raw, ";") {
			parts := strings.SplitN(entry, "=", 2)
			if len(parts) == 2 && parts[0] == req.URL.Scheme {
				selected = parts[1]
				break
			}
		}
	}
	if selected == "" {
		return nil, nil
	}
	if !strings.Contains(selected, "://") {
		selected = "http://" + selected
	}
	return url.Parse(selected)
}
