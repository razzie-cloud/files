package rclone

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Policy is also used by the private egress proxy, not just form validation.
// Every new connection resolves once and dials a validated numeric IP.
type Policy struct {
	Private   map[string]bool
	AllowHTTP bool
	Resolver  *net.Resolver
}

func NewPolicy(exceptions string, allowHTTP bool) (*Policy, error) {
	p := &Policy{Private: map[string]bool{}, AllowHTTP: allowHTTP, Resolver: net.DefaultResolver}
	for _, host := range strings.Split(exceptions, ",") {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" {
			continue
		}
		h, port, e := net.SplitHostPort(host)
		if e != nil || h == "" || port == "" {
			return nil, errors.New("private S3 exceptions must be exact host:port values")
		}
		p.Private[host] = true
	}
	return p, nil
}
func (p *Policy) Endpoint(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("use an http(s) S3 endpoint without credentials, query, or path")
	}
	if u.Scheme == "http" && (!p.AllowHTTP || !p.Private[address(u)]) {
		return errors.New("HTTP requires an explicit operator endpoint exception")
	}
	return nil
}
func address(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}
func public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	for _, prefix := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16"} {
		if netip.MustParsePrefix(prefix).Contains(ip) {
			return false
		}
	}
	return true
}
func (p *Policy) dial(ctx context.Context, hostport string) (net.Conn, error) {
	host, port, e := net.SplitHostPort(hostport)
	if e != nil {
		return nil, errors.New("invalid S3 destination")
	}
	allowed := p.Private[strings.ToLower(hostport)]
	if port != "443" && !allowed {
		return nil, errors.New("S3 destination port blocked")
	}
	ips, e := p.Resolver.LookupNetIP(ctx, "ip", host)
	if e != nil || len(ips) == 0 {
		return nil, errors.New("S3 DNS lookup failed")
	}
	for _, ip := range ips {
		if !allowed && !public(ip) {
			return nil, errors.New("private S3 destination blocked")
		}
	}
	d := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	for _, ip := range ips {
		conn, e := d.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
		if e == nil {
			return conn, nil
		}
	}
	return nil, errors.New("cannot connect to S3 destination")
}

type Egress struct {
	URL       string
	server    *http.Server
	listener  net.Listener
	transport *http.Transport
}

func StartEgress(policy *Policy) (*Egress, error) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return nil, e
	}
	var token [32]byte
	if _, e = rand.Read(token[:]); e != nil {
		listener.Close()
		return nil, e
	}
	password := hex.EncodeToString(token[:])
	proxyURL := &url.URL{Scheme: "http", Host: listener.Addr().String(), User: url.UserPassword("files", password)}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) { return policy.dial(ctx, addr) }, ResponseHeaderTimeout: 30 * time.Second, MaxIdleConns: 32, IdleConnTimeout: 30 * time.Second}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Authenticate local proxy use; never expose this listener publicly.
		clone := r.Clone(r.Context())
		clone.Header = r.Header.Clone()
		clone.Header.Set("Authorization", r.Header.Get("Proxy-Authorization"))
		user, pass, ok := clone.BasicAuth()
		if !ok || user != "files" || subtle.ConstantTimeCompare([]byte(pass), []byte(password)) != 1 {
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		if r.Method == http.MethodConnect {
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			upstream, err := policy.dial(ctx, r.Host)
			cancel()
			if err != nil {
				http.Error(w, "destination blocked or unavailable", 502)
				return
			}
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				upstream.Close()
				http.Error(w, "tunnel unavailable", 500)
				return
			}
			client, buffer, err := hijacker.Hijack()
			if err != nil {
				upstream.Close()
				return
			}
			client.SetDeadline(time.Now().Add(2 * time.Hour))
			upstream.SetDeadline(time.Now().Add(2 * time.Hour))
			buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
			if err = buffer.Flush(); err != nil {
				client.Close()
				upstream.Close()
				return
			}
			go func() { io.Copy(upstream, buffer); upstream.Close(); client.Close() }()
			io.Copy(client, upstream)
			client.Close()
			upstream.Close()
			return
		}
		if r.URL.Scheme != "http" || !policy.AllowHTTP || !policy.Private[address(r.URL)] {
			http.Error(w, "HTTP destination blocked", 403)
			return
		}
		out := r.Clone(r.Context())
		out.RequestURI = ""
		out.Header = r.Header.Clone()
		out.Header.Del("Proxy-Authorization")
		out.Header.Del("Proxy-Connection")
		response, err := transport.RoundTrip(out)
		if err != nil {
			http.Error(w, "S3 unavailable", 502)
			return
		}
		defer response.Body.Close()
		for name, values := range response.Header {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10}
	proxy := &Egress{URL: proxyURL.String(), server: server, listener: listener, transport: transport}
	go server.Serve(listener)
	return proxy, nil
}
func (p *Egress) Close() { p.server.Close(); p.transport.CloseIdleConnections() }
