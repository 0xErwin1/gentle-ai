package shellinstaller

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func gentleShellStableTestInputs(t *testing.T, body []byte) (GentleShellSourceSelection, GentleShellStableSourceClaim) {
	t.Helper()
	sha256Sum := sha256.Sum256(body)
	sha512Sum := sha512.Sum512(body)
	selector, err := NewGentleShellStableSource(gentleShellStablePackageName, "1.2.3",
		"sha512-"+base64.StdEncoding.EncodeToString(sha512Sum[:]),
		fmt.Sprintf("%x", sha256Sum), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	return selector, GentleShellStableSourceClaim{PackageName: gentleShellStablePackageName,
		Version: "1.2.3", IntegritySRI: selector.integritySRI, ByteLength: int64(len(body)),
		RegistryURL: gentleShellStableRegistryURL}
}

const gentleShellStableTestRoot = "https://example.com/"

func gentleShellStableTestClient(t *testing.T, server *httptest.Server, trustTestCertificate bool) *http.Client {
	t.Helper()
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "example.com:443" {
			return nil, fmt.Errorf("refusing non-local test dial to %q", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	if !trustTestCertificate {
		transport.TLSClientConfig.RootCAs = nil
	}
	return &http.Client{Transport: transport}
}

func TestFetchGentleShellStableArchiveEvidenceVerifiedTLSAndBoundedBytes(t *testing.T) {
	body := []byte("synthetic stable archive")
	selector, claim := gentleShellStableTestInputs(t, body)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/gentle-pi/-/gentle-pi-1.2.3.tgz" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()

	evidence, err := fetchGentleShellStableArchiveEvidenceWithClient(selector, claim,
		gentleShellStableTestClient(t, server, true), gentleShellStableTestRoot)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Kind != "not-authorized" || !evidence.Matched || evidence.ByteLength != int64(len(body)) {
		t.Fatalf("unexpected DATA evidence: %+v", evidence)
	}
}

func TestFetchGentleShellStableArchiveEvidenceChecksClaimBeforeRequest(t *testing.T) {
	selector, claim := gentleShellStableTestInputs(t, []byte("bytes"))
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	for _, test := range []struct {
		name   string
		mutate func(*GentleShellStableSourceClaim)
	}{
		{name: "package", mutate: func(c *GentleShellStableSourceClaim) { c.PackageName = "other" }},
		{name: "version", mutate: func(c *GentleShellStableSourceClaim) { c.Version = "1.2.4" }},
		{name: "integrity", mutate: func(c *GentleShellStableSourceClaim) { c.IntegritySRI = "sha512-mismatch" }},
		{name: "length", mutate: func(c *GentleShellStableSourceClaim) { c.ByteLength++ }},
		{name: "registry", mutate: func(c *GentleShellStableSourceClaim) { c.RegistryURL = "https://other.example/" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			mismatchedClaim := claim
			test.mutate(&mismatchedClaim)
			if _, err := fetchGentleShellStableArchiveEvidenceWithClient(selector, mismatchedClaim,
				gentleShellStableTestClient(t, server, true), gentleShellStableTestRoot); err == nil {
				t.Fatal("mismatched claim was accepted")
			}
			if requests.Load() != 0 {
				t.Fatal("request issued before exact claim comparison")
			}
		})
	}
}

func TestFetchGentleShellStableArchiveEvidenceRejectsUnverifiedTLS(t *testing.T) {
	selector, claim := gentleShellStableTestInputs(t, []byte("bytes"))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("bytes"))
	}))
	defer server.Close()
	client := gentleShellStableTestClient(t, server, false)
	if _, err := fetchGentleShellStableArchiveEvidenceWithClient(selector, claim,
		client, gentleShellStableTestRoot); err == nil {
		t.Fatal("untrusted test certificate was accepted")
	}
}

func TestFetchGentleShellStableArchiveEvidenceRejectsHTTPAndRedirect(t *testing.T) {
	selector, claim := gentleShellStableTestInputs(t, []byte("bytes"))
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/gentle-pi/-/gentle-pi-1.2.3.tgz" {
			w.Header().Set("Location", "https://"+r.Host+"/redirected")
			w.WriteHeader(http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("bytes"))
	}))
	defer server.Close()
	root := gentleShellStableTestRoot
	client := gentleShellStableTestClient(t, server, true)
	if _, err := fetchGentleShellStableArchiveEvidenceWithClient(selector, claim,
		client, strings.Replace(root, "https://", "http://", 1)); err == nil {
		t.Fatal("HTTP registry root was accepted")
	}
	if _, err := fetchGentleShellStableArchiveEvidenceWithClient(selector, claim,
		client, root); err == nil {
		t.Fatal("redirect to a changed path was accepted")
	}
	if requests.Load() != 1 {
		t.Fatalf("redirect was followed; server received %d requests", requests.Load())
	}
}

type gentleShellResponseURLMutator struct {
	base   http.RoundTripper
	mutate func(*url.URL)
}

func (m gentleShellResponseURLMutator) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := m.base.RoundTrip(request)
	if err != nil || response == nil {
		return response, err
	}
	copiedRequest := request.Clone(request.Context())
	copiedURL := *request.URL
	m.mutate(&copiedURL)
	copiedRequest.URL = &copiedURL
	response.Request = copiedRequest
	return response, nil
}

func TestFetchGentleShellStableArchiveEvidenceRejectsChangedHostOrPath(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*url.URL)
	}{
		{name: "host", mutate: func(u *url.URL) { u.Host = "other.example" }},
		{name: "path", mutate: func(u *url.URL) { u.Path = "/other/archive.tgz" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte("bytes")
			selector, claim := gentleShellStableTestInputs(t, body)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(body)
			}))
			defer server.Close()
			client := gentleShellStableTestClient(t, server, true)
			client.Transport = gentleShellResponseURLMutator{base: client.Transport, mutate: test.mutate}
			if _, err := fetchGentleShellStableArchiveEvidenceWithClient(selector, claim,
				client, gentleShellStableTestRoot); err == nil {
				t.Fatal("response with changed host or path was accepted")
			}
		})
	}
}

func TestFetchGentleShellStableArchiveEvidenceRejectsMismatchEarlyEOFAndOversize(t *testing.T) {
	for _, test := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{name: "mismatched body", handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("BYTES")) }},
		{name: "early EOF", handler: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "5")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("byte"))
		}},
		{name: "oversize declaration", handler: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprint(gentleShellMaxArchiveBytes+1))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("bytes"))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte("bytes")
			selector, claim := gentleShellStableTestInputs(t, body)
			server := httptest.NewTLSServer(test.handler)
			defer server.Close()
			if _, err := fetchGentleShellStableArchiveEvidenceWithClient(selector, claim,
				gentleShellStableTestClient(t, server, true), gentleShellStableTestRoot); err == nil {
				t.Fatal("invalid archive response was accepted")
			}
		})
	}
}
