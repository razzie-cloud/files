// Package gatewayidentity verifies the access-gateway v4.public identity contract.
// Keep this implementation and Notes' independent copy in sync.
package gatewayidentity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"aidanwoods.dev/go-paseto"
)

const Header = "X-Auth-Request-Token"

var errIdentity = errors.New("verified gateway identity required")

type publicKey struct {
	ID        string    `json:"kid"`
	PublicKey string    `json:"public_key"`
	NotBefore time.Time `json:"not_before"`
	ExpiresAt time.Time `json:"expires_at"`
}
type keySet struct {
	Issuer    string      `json:"issuer"`
	ExpiresAt time.Time   `json:"expires_at"`
	Keys      []publicKey `json:"keys"`
}
type verificationKey struct {
	key        paseto.V4AsymmetricPublicKey
	begin, end time.Time
}

type Verifier struct {
	mu                  sync.Mutex
	endpoint, audience  string
	client              *http.Client
	now                 func() time.Time
	issuer              string
	keys                map[string]verificationKey
	expires, retryAfter time.Time
}

// ValidateURL accepts exact operator-configured origins. HTTP permits discovery
// on the private container network; use HTTPS across untrusted networks.
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return errors.New("ACCESS_GATEWAY_URL must be an exact http(s) origin")
	}
	return nil
}

// New fetches keys before accepting traffic. An empty URL preserves the existing
// trusted-proxy mode. A configured but unavailable gateway is a startup error.
func New(ctx context.Context, gatewayURL, audience string) (*Verifier, error) {
	if gatewayURL == "" {
		return nil, nil
	}
	if err := ValidateURL(gatewayURL); err != nil {
		return nil, err
	}
	if err := ValidateURL(audience); err != nil {
		return nil, errors.New("invalid identity audience")
	}
	v := &Verifier{endpoint: gatewayURL + "/.well-known/paseto-keys", audience: audience, now: time.Now,
		client: &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if err := v.refresh(ctx, v.now()); err != nil {
		return nil, err
	}
	return v, nil
}

// refresh runs under mu after initialization. Never replace usable keys with an
// invalid document or extend their lifetime when discovery fails.
func (v *Verifier) refresh(ctx context.Context, now time.Time) error {
	v.retryAfter = now.Add(5 * time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.endpoint, nil)
	if err != nil {
		return errIdentity
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return errIdentity
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errIdentity
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (16<<10)+1))
	if err != nil || len(b) > 16<<10 {
		return errIdentity
	}
	var doc keySet
	if json.Unmarshal(b, &doc) != nil || ValidateURL(doc.Issuer) != nil || !now.Before(doc.ExpiresAt) || len(doc.Keys) < 1 || len(doc.Keys) > 4 {
		return errIdentity
	}
	// The configured discovery origin is the trust anchor. Its issuer may be
	// the public auth origin while the fetch uses a private service address.
	if v.issuer != "" && doc.Issuer != v.issuer {
		return errIdentity
	}
	keys := make(map[string]verificationKey, len(doc.Keys))
	for _, item := range doc.Keys {
		key, err := paseto.NewV4AsymmetricPublicKeyFromHex(item.PublicKey)
		if err != nil {
			return errIdentity
		}
		sum := sha256.Sum256(key.ExportBytes())
		if item.ID != hex.EncodeToString(sum[:16]) || !item.NotBefore.Before(item.ExpiresAt) || item.ExpiresAt.Sub(item.NotBefore) > 24*time.Hour+2*time.Minute {
			return errIdentity
		}
		if _, duplicate := keys[item.ID]; duplicate {
			return errIdentity
		}
		keys[item.ID] = verificationKey{key, item.NotBefore, item.ExpiresAt}
	}
	v.issuer, v.keys = doc.Issuer, keys
	v.expires = minTime(doc.ExpiresAt, now.Add(5*time.Minute))
	return nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// Verify checks both the signature and its binding to this request and the
// separately forwarded identity headers. Tokens are never accepted as cookies.
func (v *Verifier) Verify(r *http.Request, requireAdmin bool) error {
	if v == nil {
		return nil
	}
	values := r.Header.Values(Header)
	if len(values) != 1 || len(values[0]) > 4096 || !strings.HasPrefix(values[0], "v4.public.") {
		return errIdentity
	}
	parser := paseto.NewParserWithoutExpiryCheck()
	footer, err := parser.UnsafeParseFooter(paseto.V4Public, values[0])
	if err != nil || len(footer) != 32 {
		return errIdentity
	}
	v.mu.Lock()
	now := v.now()
	key, ok := v.keys[string(footer)]
	if !now.Before(v.expires) || !ok {
		if !now.Before(v.retryAfter) {
			_ = v.refresh(r.Context(), now)
		}
		key, ok = v.keys[string(footer)]
	}
	now = v.now() // Discovery may have taken long enough for the assertion to expire.
	issuer, expires := v.issuer, v.expires
	v.mu.Unlock()
	if !ok || !now.Before(expires) || now.Before(key.begin) || !now.Before(key.end) {
		return errIdentity
	}
	parser.AddRule(paseto.IssuedBy(issuer), paseto.ForAudience(v.audience), paseto.ValidAt(now))
	token, err := parser.ParseV4Public(key.key, values[0], nil)
	if err != nil {
		return errIdentity
	}
	iat, e1 := token.GetIssuedAt()
	nbf, e2 := token.GetNotBefore()
	exp, e3 := token.GetExpiration()
	if e1 != nil || e2 != nil || e3 != nil || now.Before(iat) || nbf.Before(iat) || !iat.Before(exp) || !now.Before(exp) || exp.Sub(iat) > time.Minute || iat.Before(key.begin) || exp.After(key.end) {
		return errIdentity
	}
	for claim, header := range map[string]string{"sub": "X-Auth-Request-User", "name": "X-Auth-Request-Name"} {
		value, err := token.GetString(claim)
		if err != nil || len(r.Header.Values(header)) != 1 || r.Header.Get(header) != value {
			return errIdentity
		}
	}
	method, e1 := token.GetString("method")
	uri, e2 := token.GetString("uri")
	var admin bool
	if e1 != nil || e2 != nil || token.Get("admin", &admin) != nil || method != r.Method || uri != r.URL.RequestURI() || requireAdmin && !admin {
		return errIdentity
	}
	return nil
}
