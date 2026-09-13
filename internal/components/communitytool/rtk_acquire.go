package communitytool

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const (
	rtkMaxArchiveSize    int64 = 16 << 20
	rtkMaxExecutableSize       = 64 << 20
)

type rtkHTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

func acquireRTKCandidate(client rtkHTTPClient, stagingDir string, asset rtkCandidateAsset) (string, error) {
	if client == nil || asset.SizeBytes <= 0 || asset.SizeBytes > rtkMaxArchiveSize || filepath.Base(asset.ExecutableMember) != asset.ExecutableMember {
		return "", fmt.Errorf("invalid RTK acquisition contract")
	}
	entries, err := os.ReadDir(stagingDir)
	if err != nil || len(entries) != 0 {
		return "", fmt.Errorf("RTK staging directory must exist and be empty")
	}
	req, err := http.NewRequest(http.MethodGet, asset.URL, nil)
	if err != nil {
		return "", fmt.Errorf("build RTK download request: %w", err)
	}
	response, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download RTK candidate: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("download RTK candidate: HTTP %d", response.StatusCode)
	}
	archive, err := io.ReadAll(io.LimitReader(response.Body, asset.SizeBytes+1))
	if err != nil || int64(len(archive)) != asset.SizeBytes {
		return "", fmt.Errorf("RTK candidate size verification failed")
	}
	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) != asset.SHA256 {
		return "", fmt.Errorf("RTK candidate checksum verification failed")
	}
	file, err := rtkArchiveExecutable(archive, asset.ExecutableMember)
	if err != nil {
		return "", err
	}
	return extractRTKExecutable(stagingDir, asset.ExecutableMember, file)
}

func rtkArchiveExecutable(archive []byte, expected string) (*zip.File, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("read RTK candidate ZIP: %w", err)
	}
	if len(reader.File) != 1 {
		return nil, fmt.Errorf("RTK candidate ZIP must contain exactly one member")
	}
	file := reader.File[0]
	if file.Name != expected || !file.Mode().IsRegular() || file.UncompressedSize64 > rtkMaxExecutableSize {
		return nil, fmt.Errorf("RTK candidate ZIP member is not the expected regular executable")
	}
	return file, nil
}

func extractRTKExecutable(stagingDir, name string, file *zip.File) (string, error) {
	source, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("open RTK executable: %w", err)
	}
	defer source.Close()
	path := filepath.Join(stagingDir, name)
	output, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return "", fmt.Errorf("create RTK executable: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := io.Copy(output, source); err != nil {
		_ = output.Close()
		return "", fmt.Errorf("extract RTK executable: %w", err)
	}
	if err := output.Close(); err != nil {
		return "", fmt.Errorf("close RTK executable: %w", err)
	}
	ok = true
	return path, nil
}
