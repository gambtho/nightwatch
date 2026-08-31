package httpapi

import (
	"fmt"
	"net/url"
)

// ParsePublicBaseURL validates NIGHTSHIFT_PUBLIC_BASE_URL: scheme + host,
// nothing else. It carries magic-link tokens, defines the trusted Origin,
// and pairs with a Secure cookie, so HTTPS is required — with an explicit
// exception only for localhost development (the secure-context carve-out).
func ParsePublicBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("public base URL: %w", err)
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && isLocalhost(u.Hostname()):
	default:
		return nil, fmt.Errorf("public base URL %q must be https (http is allowed for localhost only)", raw)
	}
	if u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || u.Opaque != "" {
		return nil, fmt.Errorf("public base URL %q must be scheme + host only, no trailing slash", raw)
	}
	return u, nil
}

func isLocalhost(hostname string) bool {
	return hostname == "localhost" || hostname == "127.0.0.1" || hostname == "::1"
}
