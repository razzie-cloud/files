package rclone

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestProxyBlocksRedirectAndRequiresAuthentication(t *testing.T) {
	var reached atomic.Int32
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1) }))
	defer blocked.Close()
	allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, blocked.URL, 302) }))
	defer allowed.Close()
	u, _ := url.Parse(allowed.URL)
	p, _ := NewPolicy(u.Host, true)
	proxy, e := StartEgress(p)
	if e != nil {
		t.Fatal(e)
	}
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: time.Second}
	response, e := client.Get(allowed.URL)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 403 || reached.Load() != 0 {
		t.Fatal("redirect escaped endpoint policy")
	}
	proxyURL.User = nil
	unauthorized := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: time.Second}
	response, e = unauthorized.Get(allowed.URL)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 407 {
		t.Fatal("unauthenticated internal proxy request accepted")
	}
}

func TestPolicyChecksDNSAnswersAtDialTime(t *testing.T) {
	listener, e := net.ListenPacket("udp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	go func() {
		for {
			buffer := make([]byte, 1024)
			n, addr, e := listener.ReadFrom(buffer)
			if e != nil {
				return
			}
			var request dnsmessage.Message
			if request.Unpack(buffer[:n]) != nil {
				continue
			}
			response := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, RecursionAvailable: true}, Questions: request.Questions}
			for _, question := range request.Questions {
				if question.Type == dnsmessage.TypeA {
					response.Answers = append(response.Answers, dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 0}, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}})
				}
			}
			packet, e := response.Pack()
			if e == nil {
				listener.WriteTo(packet, addr)
			}
		}
	}()
	p, _ := NewPolicy("", false)
	p.Resolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", listener.LocalAddr().String())
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if conn, e := p.dial(ctx, "storage.example:443"); e == nil {
		conn.Close()
		t.Fatal("private DNS answer escaped validation")
	}
}
