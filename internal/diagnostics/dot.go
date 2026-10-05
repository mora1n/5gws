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
	return probeDOTListener(ctx, dotProbe{DNS: cfg.DNS, Listen: cfg.DNS.ListenPublicDOT, Roots: roots})
}

type dotProbe struct {
	DNS    config.DNSConfig
	Listen string
	Roots  *x509.CertPool
}

func probeDOTListener(ctx context.Context, probe dotProbe) DOTResult {
	result := DOTResult{Domain: probe.DNS.DOTDomain, Listen: probe.Listen, Status: "error", CertificateStatus: "error"}
	if probe.Listen == "" {
		result.Status = "disabled"
		result.CertificateStatus = "disabled"
		return result
	}
	certificate, err := readCertificate(probe.DNS.CertFile)
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
	if err := certificate.VerifyHostname(probe.DNS.DOTDomain); err != nil {
		result.Error = "certificate domain: " + err.Error()
		return result
	}
	result.DomainMatch = true
	if result.DaysRemaining <= 30 {
		result.CertificateStatus = "warning"
	} else {
		result.CertificateStatus = "ok"
	}
	result.LatencyMS, err = exchangeDOT(ctx, probe)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Status = "ok"
	return result
}

func exchangeDOT(ctx context.Context, probe dotProbe) (float64, error) {
	_, port, err := net.SplitHostPort(probe.Listen)
	if err != nil {
		return 0, fmt.Errorf("DoT listen: %w", err)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 3 * time.Second},
		Config:    &tls.Config{ServerName: probe.DNS.DOTDomain, MinVersion: tls.VersionTLS12, RootCAs: probe.Roots},
	}
	started := time.Now()
	connection, err := dialer.DialContext(probeCtx, "tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		return elapsedMS(started), fmt.Errorf("DoT TLS: %w", err)
	}
	defer connection.Close()
	deadline, _ := probeCtx.Deadline()
	if err := connection.SetDeadline(deadline); err != nil {
		return elapsedMS(started), fmt.Errorf("DoT deadline: %w", err)
	}
	stop := context.AfterFunc(probeCtx, func() { connection.Close() })
	defer stop()
	message := new(dns.Msg)
	message.SetQuestion("example.com.", dns.TypeA)
	dnsConnection := &dns.Conn{Conn: connection}
	if err := dnsConnection.WriteMsg(message); err != nil {
		return elapsedMS(started), fmt.Errorf("DoT query: %w", err)
	}
	response, err := dnsConnection.ReadMsg()
	if err != nil {
		return elapsedMS(started), fmt.Errorf("DoT response: %w", err)
	}
	if err := ValidateDNSResponse(message, response); err != nil {
		return elapsedMS(started), err
	}
	return elapsedMS(started), nil
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
