package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestProxyValidation(t *testing.T) {
	for _, s := range []string{"http://127.0.0.1:7890", "socks5://localhost:1080", "https://u:p@proxy.example:443"} {
		if _, e := validateProxy(s); e != nil {
			t.Fatal(e)
		}
	}
	for _, s := range []string{"file:///etc/passwd", "ftp://localhost:1", "http://", "http://a/b", "http://a?x=1"} {
		if _, e := validateProxy(s); e == nil {
			t.Fatal("accepted", s)
		}
	}
}
func TestSafePaths(t *testing.T) {
	for _, s := range []string{"../bad", "a/../../b", "a/../b", `C:\bad`, `\bad`, `a\..\bad`} {
		if _, e := safeChild("root", s); e == nil {
			t.Fatal("accepted", s)
		}
	}
	if _, e := safeChild("root", "lib/main.js"); e != nil {
		t.Fatal(e)
	}
}
func TestBridgeHTTPAndAuth(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("credential leak")
		}
		w.Write([]byte("hello portable"))
	}))
	defer origin.Close()
	b, e := startBridge(func(r *http.Request) (*url.URL, error) { return nil, nil }, "test-token")
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	p, _ := url.Parse(b.URL())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(p)}}
	res, e := client.Get(origin.URL)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 407 {
		t.Fatal(res.StatusCode)
	}
	p.User = url.UserPassword("wbp", "test-token")
	tr := &http.Transport{Proxy: http.ProxyURL(p)}
	defer tr.CloseIdleConnections()
	client.Transport = tr
	res, e = client.Get(origin.URL)
	if e != nil {
		t.Fatal(e)
	}
	buf, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(buf) != "hello portable" {
		t.Fatal(string(buf))
	}
}
func TestBridgeHTTPSConnect(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("secure response")) }))
	defer origin.Close()
	b, e := startBridge(func(r *http.Request) (*url.URL, error) { return nil, nil }, "secret")
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	p, _ := url.Parse(b.URL())
	p.User = url.UserPassword("wbp", "secret")
	tr := origin.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(p)
	defer tr.CloseIdleConnections()
	c := &http.Client{Transport: tr}
	res, e := c.Get(origin.URL)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(data) != "secure response" {
		t.Fatal(string(data))
	}
}
func TestBridgeChainedHTTPProxy(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("chained")) }))
	defer origin.Close()
	up, e := startBridge(func(r *http.Request) (*url.URL, error) { return nil, nil }, "up-token")
	if e != nil {
		t.Fatal(e)
	}
	defer up.Close()
	upURL, _ := url.Parse(up.URL())
	upURL.User = url.UserPassword("wbp", "up-token")
	b, e := startBridge(func(r *http.Request) (*url.URL, error) { return upURL, nil }, "local-token")
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	p, _ := url.Parse(b.URL())
	p.User = url.UserPassword("wbp", "local-token")
	tr := origin.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(p)
	defer tr.CloseIdleConnections()
	res, e := (&http.Client{Transport: tr}).Get(origin.URL)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(raw) != "chained" {
		t.Fatal(string(raw))
	}
}
func TestAsarEmptyEntries(t *testing.T) {
	for _, e := range []asarEntry{{Files: map[string]*asarEntry{}}, {Size: 0, Offset: "0"}, {Size: 0, Unpacked: true}} {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if e.Files != nil && !bytes.Contains(b, []byte(`"files":{}`)) {
			t.Fatal(string(b))
		}
		if e.Files == nil && !bytes.Contains(b, []byte(`"size":0`)) {
			t.Fatal(string(b))
		}
	}
}
func TestProxyFailureDoesNotGoDirect(t *testing.T) {
	called := false
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.Write([]byte("must not connect")) }))
	defer origin.Close()
	b, e := startBridge(func(r *http.Request) (*url.URL, error) { return nil, io.ErrUnexpectedEOF }, "test-token")
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	p, _ := url.Parse(b.URL())
	p.User = url.UserPassword("wbp", "test-token")
	tr := &http.Transport{Proxy: http.ProxyURL(p)}
	defer tr.CloseIdleConnections()
	res, e := (&http.Client{Transport: tr}).Get(origin.URL)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 502 || called {
		t.Fatal("proxy failure fell back to direct")
	}
}
