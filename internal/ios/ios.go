package ios

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/morain/5gws/internal/config"
	qrcode "github.com/skip2/go-qrcode"
)

type Links struct {
	ProfileURL string
	ProfileQR  string
}

func Generate(dir string, cfg config.Config) (Links, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Links{}, err
	}
	profile := mobileConfig(cfg)
	if err := writeFile(filepath.Join(dir, "5gws-dot.mobileconfig"), []byte(profile), 0o644); err != nil {
		return Links{}, err
	}
	links := BuildLinks(cfg)
	if err := qrcode.WriteFile(links.ProfileURL, qrcode.Medium, 256, filepath.Join(dir, "5gws-dot.png")); err != nil {
		return Links{}, err
	}
	return links, nil
}

func BuildLinks(cfg config.Config) Links {
	base := trimSlash(cfg.IOS.BaseURL)
	return Links{
		ProfileURL: base + "/5gws-dot.mobileconfig",
		ProfileQR:  base + "/5gws-dot.png",
	}
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

func Serve(dir, listen, internalCIDR string) error {
	if listen == "" {
		return fmt.Errorf("ios.listen is required")
	}
	_, network, err := net.ParseCIDR(internalCIDR)
	if err != nil {
		return err
	}
	server := http.Server{
		Addr:              listen,
		Handler:           cidrOnly(network, http.FileServer(http.Dir(dir))),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return server.ListenAndServe()
}

func cidrOnly(network *net.IPNet, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			http.Error(w, "invalid remote address", http.StatusForbidden)
			return
		}
		ip := net.ParseIP(host)
		if ip == nil || (!ip.IsLoopback() && !network.Contains(ip)) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
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
