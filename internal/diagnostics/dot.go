package diagnostics

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/miekg/dns"

	"github.com/morain/5gws/internal/config"
)

func probeDOT(ctx context.Context, cfg config.Config, roots *x509.CertPool) DOTResult {
	result := DOTResult{Domain: cfg.DNS.DOTDomain, Listen: cfg.DNS.ListenPublicDOT, Status: "error", CertificateStatus: "error"}
	if cfg.DNS.ListenPublicDOT == "" {
		result.Status = "disabled"
		result.CertificateStatus = "disabled"
		return result
	}
	certificate, err := readCertificate(cfg.DNS.CertFile)
	if err != nil {
		result.Error = "certificate: " + err.Error()
		return result
	}
	result.ExpiresAt = certificate.NotAfter.UTC()
	result.DaysRemaining = int(time.Until(certificate.NotAfter).Hours() / 24)
	if time.Now().After(certificate.NotAfter) {
		result.Error = "certificate has expired"
		return result
	}
	if err := certificate.VerifyHostname(cfg.DNS.DOTDomain); err != nil {
		result.Error = "certificate domain: " + err.Error()
		return result
	}
	result.DomainMatch = true
	if result.DaysRemaining <= 30 {
		result.CertificateStatus = "warning"
	} else {
		result.CertificateStatus = "ok"
	}
	_, port, err := net.SplitHostPort(cfg.DNS.ListenPublicDOT)
	if err != nil {
		result.Error = "DoT listen: " + err.Error()
		return result
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 3 * time.Second},
		Config:    &tls.Config{ServerName: cfg.DNS.DOTDomain, MinVersion: tls.VersionTLS12, RootCAs: roots},
	}
	started := time.Now()
	connection, err := dialer.DialContext(probeCtx, "tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		result.Error = "DoT TLS: " + err.Error()
		return result
	}
	defer connection.Close()
	message := new(dns.Msg)
	message.SetQuestion("example.com.", dns.TypeA)
	dnsConnection := &dns.Conn{Conn: connection}
	if err := dnsConnection.WriteMsg(message); err != nil {
		result.Error = "DoT query: " + err.Error()
		return result
	}
	response, err := dnsConnection.ReadMsg()
	result.LatencyMS = elapsedMS(started)
	if err != nil {
		result.Error = "DoT response: " + err.Error()
		return result
	}
	if response.Rcode != dns.RcodeSuccess {
		result.Error = fmt.Sprintf("DoT response code: %s", dns.RcodeToString[response.Rcode])
		return result
	}
	result.Status = "ok"
	return result
}

func readCertificate(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("no PEM certificate found in %s", path)
	}
	return x509.ParseCertificate(block.Bytes)
}
