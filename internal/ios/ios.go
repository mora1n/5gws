package ios

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/morain/5gws/internal/config"
	qrcode "github.com/skip2/go-qrcode"
)

type Links struct {
	CertURL    string
	ProfileURL string
	CertQR     string
	ProfileQR  string
}

func Generate(dir string, cfg config.Config) (Links, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Links{}, err
	}
	caCert, caKey, err := certificate(cfg)
	if err != nil {
		return Links{}, err
	}
	serverCert, serverKey, err := serverCertificate(cfg, caCert, caKey)
	if err != nil {
		return Links{}, err
	}
	if err := writeFile(filepath.Join(dir, "5gws-ca.crt"), caCert, 0o644); err != nil {
		return Links{}, err
	}
	if err := writeFile(filepath.Join(dir, "5gws-ca.key"), caKey, 0o600); err != nil {
		return Links{}, err
	}
	if err := writeFile(filepath.Join(dir, "fullchain.pem"), append(serverCert, caCert...), 0o644); err != nil {
		return Links{}, err
	}
	if err := writeFile(filepath.Join(dir, "privkey.pem"), serverKey, 0o644); err != nil {
		return Links{}, err
	}
	profile := mobileConfig(cfg)
	if err := writeFile(filepath.Join(dir, "5gws-dot.mobileconfig"), []byte(profile), 0o644); err != nil {
		return Links{}, err
	}
	links := BuildLinks(cfg)
	if err := qrcode.WriteFile(links.CertURL, qrcode.Medium, 256, filepath.Join(dir, "5gws-ca.png")); err != nil {
		return Links{}, err
	}
	if err := qrcode.WriteFile(links.ProfileURL, qrcode.Medium, 256, filepath.Join(dir, "5gws-dot.png")); err != nil {
		return Links{}, err
	}
	return links, nil
}

func BuildLinks(cfg config.Config) Links {
	base := trimSlash(cfg.IOS.BaseURL)
	return Links{
		CertURL:    base + "/5gws-ca.crt",
		ProfileURL: base + "/5gws-dot.mobileconfig",
		CertQR:     base + "/5gws-ca.png",
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

func certificate(cfg config.Config) ([]byte, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{cfg.IOS.Organization},
			CommonName:   "5gws local CA",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM, nil
}

func serverCertificate(cfg config.Config, caCertPEM, caKeyPEM []byte) ([]byte, []byte, error) {
	caBlock, _ := pem.Decode(caCertPEM)
	if caBlock == nil {
		return nil, nil, fmt.Errorf("invalid CA certificate")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	keyBlock, _ := pem.Decode(caKeyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("invalid CA private key")
	}
	caKey, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	ip := net.ParseIP(cfg.Network.GatewayIP)
	if ip == nil {
		return nil, nil, fmt.Errorf("invalid gateway IP %q", cfg.Network.GatewayIP)
	}
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{cfg.IOS.Organization},
			CommonName:   cfg.Network.GatewayIP,
		},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().AddDate(2, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{ip},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM, nil
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
`, cfg.IOS.ProfileIdentifier, cfg.IOS.ProfileIdentifier, cfg.Network.GatewayIP, cfg.Network.GatewayIP)
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
