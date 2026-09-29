package shellinstaller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const gentleShellStableRegistryHost = "registry.npmjs.org"
const gentleShellStableRequestTimeout = 45 * time.Second

// FetchGentleShellStableArchiveEvidence measures bytes returned by the fixed,
// verified npm registry endpoint. The result is DATA only: it is not publisher,
// human-approval, graph, TLS, installation, or Ready authorization.
func FetchGentleShellStableArchiveEvidence(selector GentleShellSourceSelection,
	claim GentleShellStableSourceClaim) (GentleShellArchiveComparison, error) {
	result := GentleShellArchiveComparison{Kind: "not-authorized"}
	if _, err := CompareGentleShellStableClaim(selector, claim); err != nil {
		return result, err
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return result, fmt.Errorf("STOP: load system TLS roots: %w", err)
	}
	if roots == nil {
		return result, errors.New("STOP: system TLS roots unavailable")
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DisableCompression:    true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: gentleShellStableRequestTimeout,
		TLSClientConfig: &tls.Config{
			RootCAs:    roots,
			MinVersion: tls.VersionTLS12,
			ServerName: gentleShellStableRegistryHost,
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   gentleShellStableRequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return fetchGentleShellStableArchiveEvidenceWithClient(selector, claim,
		client, gentleShellStableRegistryURL)
}

// fetchGentleShellStableArchiveEvidenceWithClient is an unexported test seam.
// Injected clients do not establish production trust; callers must still prove
// the response has a verified chain for the exact registry host.
func fetchGentleShellStableArchiveEvidenceWithClient(selector GentleShellSourceSelection,
	claim GentleShellStableSourceClaim, client *http.Client,
	registryRoot string) (GentleShellArchiveComparison, error) {
	result := GentleShellArchiveComparison{Kind: "not-authorized"}
	if _, err := CompareGentleShellStableClaim(selector, claim); err != nil {
		return result, err
	}
	if client == nil {
		return result, errors.New("STOP: no Stable archive HTTP client")
	}
	root, err := gentleShellStableRegistryOrigin(registryRoot)
	if err != nil {
		return result, err
	}
	archivePath := fmt.Sprintf("/gentle-pi/-/gentle-pi-%s.tgz", selector.version)
	requestURL := *root
	requestURL.Path = archivePath
	requestURL.RawPath = ""
	ctx, cancel := context.WithTimeout(context.Background(), gentleShellStableRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return result, fmt.Errorf("STOP: Stable archive request: %w", err)
	}
	// Redirect policy belongs to this boundary, not the injected client's policy.
	boundedClient := *client
	boundedClient.Jar = nil
	boundedClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := boundedClient.Do(request)
	if err != nil {
		return result, fmt.Errorf("STOP: Stable archive HTTPS GET: %w", err)
	}
	if response == nil || response.Body == nil {
		return result, errors.New("STOP: Stable archive response has no body")
	}
	defer response.Body.Close()
	if !gentleShellStableResponseMatches(response, root, archivePath) {
		return result, errors.New("STOP: Stable archive response origin, path or verified TLS state differs")
	}
	if response.StatusCode != http.StatusOK {
		return result, fmt.Errorf("STOP: Stable archive HTTP status %d", response.StatusCode)
	}
	if response.Uncompressed || (response.Header.Get("Content-Encoding") != "" &&
		!strings.EqualFold(response.Header.Get("Content-Encoding"), "identity")) {
		return result, errors.New("STOP: Stable archive response used content encoding")
	}
	if response.ContentLength > gentleShellMaxArchiveBytes ||
		(response.ContentLength >= 0 && response.ContentLength != selector.byteLength) {
		return result, errors.New("STOP: Stable archive declared length differs from selector or maximum")
	}
	return CompareGentleShellStableArchive(selector, response.Body)
}

func gentleShellStableRegistryOrigin(value string) (*url.URL, error) {
	root, err := url.Parse(value)
	if err != nil || root.Scheme != "https" || root.Opaque != "" || root.Host == "" ||
		root.User != nil || root.Path != "/" || root.RawPath != "" || root.RawQuery != "" ||
		root.ForceQuery || root.Fragment != "" || strings.ToLower(root.Host) != root.Host ||
		root.Hostname() == "" {
		return nil, errors.New("STOP: Stable archive registry must be one HTTPS origin")
	}
	return root, nil
}

func gentleShellStableResponseMatches(response *http.Response, root *url.URL,
	archivePath string) bool {
	if response == nil || response.Request == nil || response.Request.URL == nil || response.TLS == nil {
		return false
	}
	requestURL := response.Request.URL
	if response.Request.Method != http.MethodGet || requestURL.Scheme != "https" || requestURL.Host != root.Host ||
		requestURL.User != nil || requestURL.Path != archivePath ||
		requestURL.RawPath != "" || requestURL.RawQuery != "" || requestURL.ForceQuery ||
		requestURL.Fragment != "" || (response.Request.Host != "" && response.Request.Host != root.Host) {
		return false
	}
	state := response.TLS
	if state.Version < tls.VersionTLS12 || state.ServerName != root.Hostname() ||
		len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 ||
		len(state.PeerCertificates) == 0 ||
		!state.VerifiedChains[0][0].Equal(state.PeerCertificates[0]) {
		return false
	}
	return state.PeerCertificates[0].VerifyHostname(root.Hostname()) == nil
}
