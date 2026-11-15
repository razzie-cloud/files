package gatewayidentity

import (
	"net"
	"net/url"
)

// AccountURL returns a browser destination, never the private discovery endpoint.
// The public issuer is pinned during startup discovery. It remains a navigation
// address when keys expire; verification still enforces its separate deadlines.
// Legacy proxy mode follows the cloud-home auth.<domain> convention.
func (v *Verifier) AccountURL(landingOrigin string) string {
	if v != nil {
		v.mu.Lock()
		issuer := v.issuer
		v.mu.Unlock()
		if issuer != "" {
			return issuer + "/"
		}
	}
	u, err := url.Parse(landingOrigin)
	if err == nil && ValidateURL(landingOrigin) == nil && u.Hostname() != "" && net.ParseIP(u.Hostname()) == nil {
		u.Host = "auth." + u.Host
		return u.String() + "/"
	}
	return "https://auth.razzie.cloud/"
}
