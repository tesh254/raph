package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/minio/selfupdate"
	"golang.org/x/mod/semver"
)

const (
	repository      = "tesh254/raph"
	checkInterval   = 24 * time.Hour
	maxDownloadSize = 250 << 20
)

type Result struct {
	Current string
	Latest  string
	Updated bool
}

type release struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

func Update(ctx context.Context, current string) (Result, error) {
	result := Result{Current: current}
	if !semver.IsValid(current) {
		return result, fmt.Errorf("current version %q is not a semantic version", current)
	}

	latest, err := latestRelease(ctx)
	if err != nil {
		return result, err
	}
	result.Latest = latest.TagName
	if semver.Compare(latest.TagName, current) <= 0 {
		return result, nil
	}

	archiveName := assetName()
	archiveURL, err := findAsset(latest.Assets, archiveName)
	if err != nil {
		return result, err
	}
	checksumsURL, err := findAsset(latest.Assets, "checksums.txt")
	if err != nil {
		return result, err
	}

	checksums, err := download(ctx, checksumsURL)
	if err != nil {
		return result, fmt.Errorf("download checksums: %w", err)
	}
	want, err := checksumFor(checksums, archiveName)
	if err != nil {
		return result, err
	}
	archive, err := download(ctx, archiveURL)
	if err != nil {
		return result, fmt.Errorf("download %s: %w", archiveName, err)
	}
	got := sha256.Sum256(archive)
	if !bytes.Equal(got[:], want) {
		return result, fmt.Errorf("checksum mismatch for %s", archiveName)
	}

	binary, err := extractBinary(archive)
	if err != nil {
		return result, err
	}
	if err := selfupdate.Apply(bytes.NewReader(binary), selfupdate.Options{}); err != nil {
		if rollbackErr := selfupdate.RollbackError(err); rollbackErr != nil {
			return result, fmt.Errorf("apply update: %w (rollback failed: %v)", err, rollbackErr)
		}
		return result, fmt.Errorf("apply update: %w", err)
	}
	result.Updated = true
	return result, nil
}

func ShouldAutoCheck() bool {
	if os.Getenv("RAPH_NO_AUTO_UPDATE") != "" {
		return false
	}
	path, err := checkPath()
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	if err == nil && time.Since(info.ModTime()) < checkInterval {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false
	}
	now := time.Now()
	if err := os.WriteFile(path, []byte(now.UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		return false
	}
	return true
}

func checkPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".raph", "update-check"), nil
}

func latestRelease(ctx context.Context) (release, error) {
	var value release
	url := "https://api.github.com/repos/" + repository + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return value, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "raph-updater")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return value, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return value, fmt.Errorf("GitHub releases returned %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&value); err != nil {
		return value, err
	}
	if !semver.IsValid(value.TagName) {
		return value, fmt.Errorf("latest release tag %q is not semantic versioning", value.TagName)
	}
	return value, nil
}

func findAsset(assets []asset, name string) (string, error) {
	for _, item := range assets {
		if item.Name == name {
			return item.URL, nil
		}
	}
	return "", fmt.Errorf("release asset %s not found", name)
}

func assetName() string {
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("raph_%s_%s%s", runtime.GOOS, runtime.GOARCH, ext)
}

func download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDownloadSize))
}

func checksumFor(data []byte, name string) ([]byte, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			sum, err := hex.DecodeString(fields[0])
			if err != nil {
				return nil, fmt.Errorf("invalid checksum for %s: %w", name, err)
			}
			return sum, nil
		}
	}
	return nil, fmt.Errorf("checksum for %s not found", name)
}

func extractBinary(archive []byte) ([]byte, error) {
	if runtime.GOOS == "windows" {
		return extractZip(archive)
	}
	return extractTarGzip(archive)
}

func extractTarGzip(archive []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(header.Name) == "raph" {
			return io.ReadAll(io.LimitReader(tarReader, maxDownloadSize))
		}
	}
	return nil, errors.New("raph binary not found in release archive")
}

func extractZip(archive []byte) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, err
	}
	for _, file := range reader.File {
		if filepath.Base(file.Name) != "raph.exe" {
			continue
		}
		body, err := file.Open()
		if err != nil {
			return nil, err
		}
		defer body.Close()
		return io.ReadAll(io.LimitReader(body, maxDownloadSize))
	}
	return nil, errors.New("raph.exe binary not found in release archive")
}
