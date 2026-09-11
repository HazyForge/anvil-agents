package desktop

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ParseAPIOrigin validates an anvil-agents OIDC API origin.
// It must be http(s), include a host, and omit credentials, path, query, and fragment.
func ParseAPIOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("api origin is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("api origin is not a valid URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("api origin must be http or https")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("api origin must not include credentials")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("api origin must include a host")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("api origin must not include a query or fragment")
	}
	if parsed.Opaque != "" {
		return "", fmt.Errorf("api origin must not be opaque")
	}
	path := strings.TrimSuffix(parsed.EscapedPath(), "/")
	if path != "" {
		return "", fmt.Errorf("api origin must not include a path")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func originOf(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func hasTokenQuery(request *http.Request) bool {
	if request == nil || request.URL == nil {
		return false
	}
	query := request.URL.Query()
	for key, values := range query {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "access_token", "accesstoken", "id_token", "refresh_token":
			for _, value := range values {
				if strings.TrimSpace(value) != "" {
					return true
				}
			}
		}
	}
	return false
}
