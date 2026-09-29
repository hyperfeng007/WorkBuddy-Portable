package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	xproxy "golang.org/x/net/proxy"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Resolver func(*http.Request) (*url.URL, error)
type Bridge struct {
	observer  func(string, string, string)
	server    *http.Server
	ln        net.Listener
	transport *http.Transport
	mu        sync.Mutex
	tunnels   map[net.Conn]bool
	closed    bool
	resolve   Resolver
	token     string
}

func newTransport(r Resolver) *http.Transport {
	return &http.Transport{Proxy: r, DialContext: (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 20 * time.Second, ResponseHeaderTimeout: 90 * time.Second, IdleConnTimeout: 60 * time.Second, MaxIdleConns: 32, ForceAttemptHTTP2: false}
}
func startBridge(r Resolver, token string) (*Bridge, error) {
	return startBridgeObserved(r, token, nil)
}
func startBridgeObserved(r Resolver, token string, observe func(string, string, string)) (*Bridge, error) {
	ln, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		return nil, e
	}
	b := &Bridge{observer: observe, ln: ln, transport: newTransport(r), resolve: r, token: token, tunnels: map[net.Conn]bool{}}
	b.server = &http.Server{Handler: b, ReadHeaderTimeout: 15 * time.Second, MaxHeaderBytes: 1 << 20}
	go b.server.Serve(ln)
	return b, nil
}
func (b *Bridge) URL() string { return "http://" + b.ln.Addr().String() }
func (b *Bridge) Close() {
	b.mu.Lock()
	b.closed = true
	for c := range b.tunnels {
		c.Close()
	}
	b.mu.Unlock()
	b.server.Close()
	b.transport.CloseIdleConnections()
}
func (b *Bridge) track(c net.Conn) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		c.Close()
		return false
	}
	b.tunnels[c] = true
	return true
}
func (b *Bridge) drop(c net.Conn) { c.Close(); b.mu.Lock(); delete(b.tunnels, c); b.mu.Unlock() }
func stripHop(h http.Header) {
	for _, k := range strings.Split(h.Get("Connection"), ",") {
		h.Del(strings.TrimSpace(k))
	}
	for _, k := range []string{"Connection", "Proxy-Connection", "Proxy-Authorization", "Proxy-Authenticate", "Keep-Alive", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		h.Del(k)
	}
}
func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("wbp:"+b.token))
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Proxy-Authorization")), []byte(expected)) != 1 {
		b.event("authentication-required", r, "407")
		w.Header().Set("Proxy-Authenticate", `Basic realm="WorkBuddy Portable"`)
		http.Error(w, "Proxy authentication required", 407)
		return
	}
	if r.Method == "CONNECT" {
		b.connect(w, r)
		return
	}
	if r.URL.Scheme != "http" && r.URL.Scheme != "https" {
		http.Error(w, "Absolute HTTP URL required", 400)
		return
	}
	if r.URL.Host == b.ln.Addr().String() {
		http.Error(w, "Proxy loop rejected", 400)
		return
	}
	out := r.Clone(r.Context())
	out.RequestURI = ""
	stripHop(out.Header)
	b.event("http-request", r, "")
	resp, e := b.transport.RoundTrip(out)
	if e != nil {
		b.event("upstream-error", r, fmt.Sprintf("%T", e))
		http.Error(w, "Upstream connection failed; check system proxy / portable.json", 502)
		return
	}
	defer resp.Body.Close()
	stripHop(resp.Header)
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(flushingWriter{w}, resp.Body)
}

// Flush streaming model output rather than buffering small SSE chunks.
type flushingWriter struct{ http.ResponseWriter }

func (w flushingWriter) Write(p []byte) (int, error) {
	n, e := w.ResponseWriter.Write(p)
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
	return n, e
}
func (b *Bridge) connect(w http.ResponseWriter, r *http.Request) {
	host, port, e := net.SplitHostPort(r.Host)
	if e != nil || host == "" || port == "" || r.Host == b.ln.Addr().String() {
		http.Error(w, "Invalid tunnel target", 400)
		return
	}
	probe := r.Clone(r.Context())
	probe.URL = &url.URL{Scheme: "https", Host: r.Host}
	p, e := b.resolve(probe)
	if e != nil {
		b.event("resolution-error", r, fmt.Sprintf("%T", e))
		http.Error(w, "Proxy resolution failed", 502)
		return
	}
	mode := "upstream-proxy"
	if p == nil {
		mode = "resolved-direct"
	}
	b.event("connect-route", r, mode)
	remote, e := dialTunnel(r.Context(), r.Host, p)
	if e != nil {
		b.event("tunnel-error", r, fmt.Sprintf("%T", e))
		http.Error(w, "Proxy tunnel failed", 502)
		return
	}
	b.event("tunnel-connected", r, mode)
	if !b.track(remote) {
		return
	}
	defer b.drop(remote)
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "No hijack", 500)
		return
	}
	local, rw, e := hj.Hijack()
	if e != nil {
		return
	}
	if !b.track(local) {
		return
	}
	defer b.drop(local)
	rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
	if e = rw.Flush(); e != nil {
		return
	}
	done := make(chan struct{})
	go func() { io.Copy(remote, rw); remote.Close(); close(done) }()
	io.Copy(local, remote)
	local.Close()
	<-done
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
func dialTunnel(ctx context.Context, target string, p *url.URL) (net.Conn, error) {
	d := &net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}
	if p == nil {
		return d.DialContext(ctx, "tcp", target)
	}
	if p.Scheme == "socks5" || p.Scheme == "socks5h" {
		var auth *xproxy.Auth
		if p.User != nil {
			pw, _ := p.User.Password()
			auth = &xproxy.Auth{User: p.User.Username(), Password: pw}
		}
		addr := p.Host
		if p.Port() == "" {
			addr = net.JoinHostPort(p.Hostname(), "1080")
		}
		sd, e := xproxy.SOCKS5("tcp", addr, auth, d)
		if e != nil {
			return nil, e
		}
		return sd.(xproxy.ContextDialer).DialContext(ctx, "tcp", target)
	}
	addr := p.Host
	if p.Port() == "" {
		port := "80"
		if p.Scheme == "https" {
			port = "443"
		}
		addr = net.JoinHostPort(p.Hostname(), port)
	}
	c, e := d.DialContext(ctx, "tcp", addr)
	if e != nil {
		return nil, e
	}
	success := false
	defer func() {
		if !success {
			c.Close()
		}
	}()
	c.SetDeadline(time.Now().Add(25 * time.Second))
	if p.Scheme == "https" {
		t := tls.Client(c, &tls.Config{ServerName: p.Hostname(), MinVersion: tls.VersionTLS12})
		if e = t.HandshakeContext(ctx); e != nil {
			return nil, e
		}
		c = t
	}
	q := &http.Request{Method: "CONNECT", URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
	if p.User != nil {
		pw, _ := p.User.Password()
		q.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(p.User.Username()+":"+pw)))
	}
	if e = q.Write(c); e != nil {
		return nil, e
	}
	reader := bufio.NewReader(c)
	resp, e := http.ReadResponse(reader, q)
	if e != nil {
		return nil, e
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("upstream CONNECT status %d", resp.StatusCode)
	}
	c.SetDeadline(time.Time{})
	success = true
	return &bufferedConn{c, reader}, nil
}

func (b *Bridge) event(kind string, r *http.Request, detail string) {
	if b.observer == nil {
		return
	}
	host := r.URL.Host
	if r.Method == "CONNECT" {
		host = r.Host
	}
	// Remove any user-info even from malformed absolute proxy requests.
	u, e := url.Parse("http://" + host)
	if e == nil {
		host = u.Host
	} else {
		host = "invalid"
	}
	b.observer(kind, host, detail)
}
