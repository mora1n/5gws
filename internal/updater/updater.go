package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Info struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	AssetURL  string `json:"-"`
	SumURL    string `json:"-"`
}

type pending struct {
	Binary string `json:"binary"`
	Backup string `json:"backup"`
}

type Client struct {
	HTTP       *http.Client
	Repository string
	APIBase    string
}

func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}, Repository: "mora1n/5gws", APIBase: "https://api.github.com"}
}

func (c *Client) Check(ctx context.Context, current string) (Info, error) {
	url := strings.TrimRight(c.APIBase, "/") + "/repos/" + c.Repository + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Info{}, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("release check failed: %s", resp.Status)
	}
	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&release); err != nil {
		return Info{}, err
	}
	latest := strings.TrimPrefix(release.TagName, "v")
	assetName := fmt.Sprintf("5gws-linux-%s", runtime.GOARCH)
	info := Info{Current: current, Latest: latest, Available: latest != "" && latest != current}
	for _, asset := range release.Assets {
		switch asset.Name {
		case assetName:
			info.AssetURL = asset.BrowserDownloadURL
		case assetName + ".sha256":
			info.SumURL = asset.BrowserDownloadURL
		}
	}
	if info.AssetURL == "" || info.SumURL == "" {
		return Info{}, fmt.Errorf("release %s lacks %s and checksum", release.TagName, assetName)
	}
	return info, nil
}

func (c *Client) Apply(ctx context.Context, current, binary, stateDir string) (Info, error) {
	info, err := c.Check(ctx, current)
	if err != nil || !info.Available {
		return info, err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(binary), ".5gws-update-")
	if err != nil {
		return Info{}, err
	}
	defer os.RemoveAll(tmp)
	candidate := filepath.Join(tmp, "5gws")
	if err := c.download(ctx, info.AssetURL, candidate, 128<<20); err != nil {
		return Info{}, err
	}
	sumFile := filepath.Join(tmp, "5gws.sha256")
	if err := c.download(ctx, info.SumURL, sumFile, 1<<20); err != nil {
		return Info{}, err
	}
	if err := verify(candidate, sumFile); err != nil {
		return Info{}, err
	}
	if err := os.Chmod(candidate, 0o755); err != nil {
		return Info{}, err
	}
	output, err := exec.CommandContext(ctx, candidate, "version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != info.Latest {
		return Info{}, fmt.Errorf("candidate version check failed: %w (%s)", err, output)
	}
	backupDir := filepath.Join(stateDir, "updates")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return Info{}, err
	}
	backup := filepath.Join(backupDir, "5gws-"+current)
	if err := copyFile(binary, backup, 0o755); err != nil {
		return Info{}, err
	}
	if err := writePending(stateDir, pending{Binary: binary, Backup: backup}); err != nil {
		return Info{}, err
	}
	if err := copyFile(candidate, binary, 0o755); err != nil {
		_ = os.Remove(pendingPath(stateDir))
		return Info{}, err
	}
	return info, nil
}

func Confirm(stateDir string) error {
	err := os.Remove(pendingPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func RollbackPending(stateDir string) error {
	data, err := os.ReadFile(pendingPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var item pending
	if err := json.Unmarshal(data, &item); err != nil {
		return err
	}
	if err := copyFile(item.Backup, item.Binary, 0o755); err != nil {
		return err
	}
	return os.Remove(pendingPath(stateDir))
}

func (c *Client) download(ctx context.Context, url, path string, limit int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", resp.Status)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, io.LimitReader(resp.Body, limit))
	return err
}

func verify(binary, sumFile string) error {
	wantFields, err := os.ReadFile(sumFile)
	if err != nil {
		return err
	}
	want := strings.Fields(string(wantFields))
	if len(want) == 0 || len(want[0]) != 64 {
		return errors.New("invalid checksum file")
	}
	file, err := os.Open(binary)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if !strings.EqualFold(want[0], hex.EncodeToString(hash.Sum(nil))) {
		return errors.New("release checksum mismatch")
	}
	return nil
}

func writePending(stateDir string, item pending) error {
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	return os.WriteFile(pendingPath(stateDir), data, 0o600)
}

func pendingPath(stateDir string) string { return filepath.Join(stateDir, "update-pending.json") }

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
