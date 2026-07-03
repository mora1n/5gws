package app

import (
	"archive/tar"
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
	"path"
	"path/filepath"
	"strings"
	"time"

	checksumutil "github.com/morain/5gws/internal/checksum"
)

const defaultUpdateAPIBase = "https://api.github.com"

var defaultUpdateHTTPClient = &http.Client{Timeout: 60 * time.Second}

type updateRelease struct {
	Tag          string
	Version      string
	AssetName    string
	AssetURL     string
	ChecksumName string
	ChecksumURL  string
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func resolve5gwsRelease(ctx context.Context, client *http.Client, apiBase, repo, version string) (updateRelease, error) {
	if version == "" {
		rel, err := fetchGithubRelease(ctx, client, apiBase, repo, "latest")
		if err != nil {
			return updateRelease{}, err
		}
		return releaseFromGithub(rel)
	}
	for _, tag := range releaseTagCandidates(version) {
		rel, err := fetchGithubRelease(ctx, client, apiBase, repo, "tags/"+tag)
		if err == nil {
			return releaseFromGithub(rel)
		}
		var statusErr httpStatusError
		if !errors.As(err, &statusErr) || statusErr.Code != http.StatusNotFound {
			return updateRelease{}, err
		}
	}
	return updateRelease{}, fmt.Errorf("release not found for version %s", version)
}

func releaseTagCandidates(version string) []string {
	candidates := []string{version}
	if !strings.HasPrefix(version, "v") {
		candidates = append(candidates, "v"+version)
	}
	return candidates
}

func fetchGithubRelease(ctx context.Context, client *http.Client, apiBase, repo, suffix string) (githubRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/%s", strings.TrimRight(apiBase, "/"), repo, suffix)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "5gws-update")
	resp, err := client.Do(req)
	if err != nil {
		return githubRelease{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return githubRelease{}, httpStatusError{URL: url, Code: resp.StatusCode, Status: resp.Status}
	}
	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return githubRelease{}, err
	}
	return rel, nil
}

func releaseFromGithub(rel githubRelease) (updateRelease, error) {
	version := strings.TrimPrefix(rel.TagName, "v")
	if version == "" {
		return updateRelease{}, errors.New("release tag_name is empty")
	}
	assetName := fmt.Sprintf("5gws-linux-amd64-%s.tar.gz", version)
	checksumName := assetName + ".sha256"
	assetURL := findGithubAsset(rel.Assets, assetName)
	if assetURL == "" {
		return updateRelease{}, fmt.Errorf("release %s has no asset %s", rel.TagName, assetName)
	}
	checksumURL := findGithubAsset(rel.Assets, checksumName)
	if checksumURL == "" {
		return updateRelease{}, fmt.Errorf("release %s has no checksum asset %s", rel.TagName, checksumName)
	}
	return updateRelease{
		Tag:          rel.TagName,
		Version:      version,
		AssetName:    assetName,
		AssetURL:     assetURL,
		ChecksumName: checksumName,
		ChecksumURL:  checksumURL,
	}, nil
}

func findGithubAsset(assets []githubAsset, name string) string {
	for _, asset := range assets {
		if asset.Name == name {
			return asset.BrowserDownloadURL
		}
	}
	return ""
}

func downloadVerifiedCandidate(ctx context.Context, client *http.Client, rel updateRelease, dir string, out io.Writer) (string, error) {
	archivePath := filepath.Join(dir, rel.AssetName)
	checksumPath := filepath.Join(dir, rel.ChecksumName)
	fmt.Fprintf(out, "download: %s\n", rel.AssetURL)
	if err := downloadReleaseFile(ctx, client, rel.AssetURL, archivePath); err != nil {
		return "", err
	}
	fmt.Fprintf(out, "checksum: %s\n", rel.ChecksumURL)
	if err := downloadReleaseFile(ctx, client, rel.ChecksumURL, checksumPath); err != nil {
		return "", err
	}
	if err := verifyArchiveChecksum(archivePath, checksumPath, rel.AssetName); err != nil {
		return "", err
	}
	return extractReleaseBinary(archivePath, dir)
}

func downloadReleaseFile(ctx context.Context, client *http.Client, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "5gws-update")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return httpStatusError{URL: url, Code: resp.StatusCode, Status: resp.Status}
	}
	file, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, resp.Body); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func verifyArchiveChecksum(archivePath, checksumPath, asset string) error {
	data, err := os.ReadFile(checksumPath)
	if err != nil {
		return err
	}
	want, err := checksumutil.ParseSHA256Text(string(data), asset)
	if err != nil {
		return err
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return err
	}
	got := hex.EncodeToString(sum.Sum(nil))
	if got != want {
		return fmt.Errorf("sha256 mismatch for %s: got %s want %s", asset, got, want)
	}
	return nil
}

func extractReleaseBinary(archivePath, dir string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	dst := filepath.Join(dir, "5gws")
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		name, err := safeTarName(hdr.Name)
		if err != nil {
			return "", err
		}
		if name != "5gws" {
			continue
		}
		if found {
			return "", errors.New("archive contains duplicate 5gws entries")
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			return "", errors.New("archive 5gws entry is not a regular file")
		}
		mode := os.FileMode(hdr.Mode) & 0o777
		if mode&0o111 == 0 {
			return "", errors.New("archive 5gws entry is not executable")
		}
		if err := writeTarFile(dst, tr, mode); err != nil {
			return "", err
		}
		found = true
	}
	if !found {
		return "", errors.New("archive did not contain executable 5gws")
	}
	return dst, nil
}

func safeTarName(name string) (string, error) {
	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("archive contains absolute path %q", name)
	}
	clean := path.Clean(name)
	if clean == "." {
		return clean, nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("archive contains unsafe path %q", name)
	}
	return clean, nil
}

func writeTarFile(dst string, r io.Reader, mode os.FileMode) error {
	file, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, r); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

type httpStatusError struct {
	URL    string
	Code   int
	Status string
}

func (e httpStatusError) Error() string {
	return fmt.Sprintf("%s returned %s", e.URL, e.Status)
}
