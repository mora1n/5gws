package ios

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/morain/5gws/internal/config"
	qrcode "github.com/skip2/go-qrcode"
)

type Links struct {
	Enabled    bool   `json:"enabled"`
	ProfileURL string `json:"profile_url,omitempty"`
	ProfileQR  string `json:"profile_qr,omitempty"`
}

func Generate(dir string, cfg config.Config) (Links, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Links{}, err
	}
	if err := writeFile(filepath.Join(dir, "5gws-dot.mobileconfig"), Profile(cfg), 0o644); err != nil {
		return Links{}, err
	}
	links := BuildLinks(cfg)
	if err := qrcode.WriteFile(links.ProfileURL, qrcode.Medium, 256, filepath.Join(dir, "5gws-dot.png")); err != nil {
		return Links{}, err
	}
	return links, nil
}

func BuildLinks(cfg config.Config) Links {
	if !cfg.IOS.Enabled {
		return Links{Enabled: false}
	}
	base := trimSlash(cfg.IOS.BaseURL)
	return Links{
		Enabled:    cfg.IOS.Enabled,
		ProfileURL: base + "/ios/5gws-dot.mobileconfig",
		ProfileQR:  base + "/ios/5gws-dot.png",
	}
}

func Profile(cfg config.Config) []byte {
	return []byte(mobileConfig(cfg))
}

func QRCode(cfg config.Config) ([]byte, error) {
	return qrcode.Encode(BuildLinks(cfg).ProfileURL, qrcode.Medium, 256)
}

func TerminalQRCode(value string) (string, error) {
	code, err := qrcode.New(value, qrcode.Medium)
	if err != nil {
		return "", err
	}
	return code.ToSmallString(false), nil
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, data, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func mobileConfig(cfg config.Config) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>PayloadType</key><string>Configuration</string>
<key>PayloadVersion</key><integer>1</integer>
<key>PayloadIdentifier</key><string>%s.profile</string>
<key>PayloadUUID</key><string>00000000-0000-5000-8000-000000000001</string>
<key>PayloadDisplayName</key><string>5gws DNS over TLS</string>
<key>PayloadContent</key><array><dict>
<key>PayloadType</key><string>com.apple.dnsSettings.managed</string>
<key>PayloadVersion</key><integer>1</integer>
<key>PayloadIdentifier</key><string>%s.dns</string>
<key>PayloadUUID</key><string>00000000-0000-5000-8000-000000000002</string>
<key>PayloadDisplayName</key><string>5gws DoT</string>
<key>DNSSettings</key><dict>
<key>DNSProtocol</key><string>TLS</string>
<key>ServerName</key><string>%s</string>
<key>ServerAddresses</key><array><string>%s</string></array>
</dict></dict></array>
</dict></plist>
`, cfg.IOS.ProfileIdentifier, cfg.IOS.ProfileIdentifier, cfg.DNS.DOTDomain, cfg.Network.GatewayIP)
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
