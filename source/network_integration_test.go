package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeFetchUsesLauncherProxy(t *testing.T) {
	runNativeFetchUsesLauncherProxy(t, false, false, []string{"1", "0"})
}

func TestNativeFetchCompressedHTTP2Endpoint(t *testing.T) {
	runNativeFetchUsesLauncherProxy(t, true, true, []string{"0"})
}

func TestNativeTLSVerificationIsNotDisabled(t *testing.T) {
	runNativeFetchUsesLauncherProxy(t, false, false, []string{"reject"})
}

func TestCLIProxyAuthenticationRegression(t *testing.T) {
	app := os.Getenv("WBP_TEST_APP")
	if app == "" || os.Getenv("WBP_TEST_NODE") == "" || os.Getenv("WBP_TEST_RUNTIME") == "" {
		t.Skip("set the real app, Node and ASAR fixture paths")
	}
	f, e := os.Open(filepath.Join(app, "resources", "app.asar"))
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	tree, _, e := readAsarHeader(f)
	if e != nil {
		t.Fatal(e)
	}
	stage := filepath.Join(t.TempDir(), "app.asar")
	for _, p := range cliPatches() {
		copyFixture(t, filepath.Join(app, "resources", "app.asar.unpacked", filepath.FromSlash(p.name)), filepath.Join(stage+".unpacked", filepath.FromSlash(p.name)))
	}
	if e = patchUnpackedCLI(stage, tree); e != nil {
		t.Fatal(e)
	}
	if e = patchUnpackedCLI(stage, tree); e == nil {
		t.Fatal("modified CLI accepted as original")
	}
	t.Setenv("WBP_TEST_CLI_PATCHED", stage+".unpacked")
	runNativeFetchUsesLauncherProxy(t, false, false, []string{"cli-headless", "cli-lite"})
}

func runNativeFetchUsesLauncherProxy(t *testing.T, h2, compressed bool, baselines []string) {
	node := os.Getenv("WBP_TEST_NODE")
	rt := os.Getenv("WBP_TEST_RUNTIME")
	if node == "" || rt == "" {
		t.Skip("set WBP_TEST_NODE and WBP_TEST_RUNTIME to test the published adapter")
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Portable test CA"}, DNSNames: []string{"models.portable.invalid"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, _ := x509.MarshalECPrivateKey(key)
	pair, e := tls.X509KeyPair(pemCert, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}))
	if e != nil {
		t.Fatal(e)
	}
	var modelHits, tunnelHits atomic.Int32
	model := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelHits.Add(1)
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer fixture-model-key" {
			t.Errorf("bad model request %s", r.URL.Path)
		}
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy secret reached model server")
		}
		w.Header().Set("Content-Type", "application/json")
		if compressed {
			if r.ProtoMajor != 1 {
				t.Errorf("portable fetch negotiated HTTP/%d; expected HTTP/1.1", r.ProtoMajor)
			}
			w.Header().Set("Content-Encoding", "gzip")
			z := gzip.NewWriter(w)
			io.WriteString(z, `{"data":[{"id":"fixture-model","label":"中文内容 𠀀😀"}]}`)
			z.Close()
		} else {
			io.WriteString(w, `{"data":[{"id":"fixture-model","label":"中文内容 𠀀😀"}]}`)
		}
	}))
	model.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	model.EnableHTTP2 = h2
	model.StartTLS()
	defer model.Close()
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "local-ok") }))
	defer loop.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != "models.portable.invalid:443" {
			t.Errorf("unexpected upstream request %s %s", r.Method, r.Host)
			http.Error(w, "bad", 400)
			return
		}
		tunnelHits.Add(1)
		remote, e := net.Dial("tcp", model.Listener.Addr().String())
		if e != nil {
			t.Error(e)
			return
		}
		defer remote.Close()
		c, rw, e := w.(http.Hijacker).Hijack()
		if e != nil {
			t.Error(e)
			return
		}
		defer c.Close()
		rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		rw.Flush()
		done := make(chan struct{})
		go func() { io.Copy(remote, rw); remote.Close(); close(done) }()
		io.Copy(c, remote)
		c.Close()
		<-done
	}))
	defer upstream.Close()
	proxyURL, _ := url.Parse(upstream.URL)
	var bridgeHits atomic.Int32
	bridge, e := startBridgeObserved(func(r *http.Request) (*url.URL, error) { return proxyURL, nil }, "bridge-secret", func(event, host, detail string) {
		if event == "tunnel-connected" {
			bridgeHits.Add(1)
		}
	})
	if e != nil {
		t.Fatal(e)
	}
	defer bridge.Close()
	for _, baseline := range baselines {
		t.Run("baseline-"+baseline, func(t *testing.T) {
			root := t.TempDir()
			ca := filepath.Join(root, "fixture-ca.pem")
			os.WriteFile(ca, pemCert, 0600)
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			script := "tests/network-probe.mjs"
			if strings.HasPrefix(baseline, "cli-") {
				script = "tests/cli-network-probe.mjs"
			}
			cmd := exec.CommandContext(ctx, node, script)
			cmd.Env = append(os.Environ(), "WBP_TEST_BASELINE="+baseline, "WBP_PORTABLE_ROOT="+root, "WBP_PORTABLE_PROXY="+bridge.URL(), "WBP_PORTABLE_PROXY_TOKEN=bridge-secret", "WBP_TEST_ORIGIN=https://models.portable.invalid", "WBP_TEST_LOOPBACK="+loop.URL, "NODE_EXTRA_CA_CERTS="+ca, "NODE_TLS_REJECT_UNAUTHORIZED=1")
			if baseline == "reject" {
				cmd.Env = append(cmd.Env, "NODE_EXTRA_CA_CERTS=")
			}
			out, e := cmd.CombinedOutput()
			t.Log(string(out))
			if e != nil {
				t.Fatal(e)
			}
			log, _ := os.ReadFile(filepath.Join(root, "Data", "Logs", "network.log"))
			if strings.Contains(string(log), "fixture-model-key") || strings.Contains(string(log), "bridge-secret") {
				t.Fatal("network log contains credentials")
			}
		})
	}
	wanted := int32(len(baselines))
	for _, baseline := range baselines {
		if baseline == "reject" {
			wanted--
		}
	}
	if modelHits.Load() != wanted || bridgeHits.Load() != int32(len(baselines)) || tunnelHits.Load() != int32(len(baselines)) {
		t.Fatalf("model %d bridge %d upstream %d", modelHits.Load(), bridgeHits.Load(), tunnelHits.Load())
	}
}

// A fake transport proves the desktop check uses the provided launcher client,
// accepts only the fixed official feed, and binds responses to this launch nonce.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDesktopUpdateCheckUsesLauncherClient(t *testing.T) {
	dir := t.TempDir()
	nonce := "test-nonce"
	id := strings.Repeat("a", 24)
	raw, _ := json.Marshal(updateCheckRequest{nonce, id})
	os.WriteFile(filepath.Join(dir, "update-check.request.json"), raw, 0600)
	called := false
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		if r.URL.String() != officialFeed {
			t.Fatal("unexpected update URL")
		}
		body := `{"version":"` + supportedVersion + `","productVersion":"` + supportedVersion + `","url":"` + supportedURL + `","sha256hash":""}`
		return http.ReadResponse(bufio.NewReader(strings.NewReader("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nConnection: close\r\n\r\n"+body)), r)
	})}
	if e := serviceUpdateCheck(context.Background(), client, dir, nonce); e != nil {
		t.Fatal(e)
	}
	if !called {
		t.Fatal("launcher client not used")
	}
	b, e := os.ReadFile(filepath.Join(dir, "update-check.response.json"))
	if e != nil {
		t.Fatal(e)
	}
	var response updateCheckResponse
	if e = json.Unmarshal(b, &response); e != nil {
		t.Fatal(e)
	}
	if response.Nonce != nonce || response.ID != id || response.Version != supportedVersion || response.Error != "" {
		t.Fatalf("bad response %#v", response)
	}
}
