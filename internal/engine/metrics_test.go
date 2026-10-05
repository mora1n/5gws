package engine

import (
	"context"
	"github.com/miekg/dns"
	"net"
	"strings"
	"testing"

	"github.com/morain/5gws/internal/config"
)

func TestNetworkBytesUsesConfiguredInterface(t *testing.T) {
	input := `Inter-|   Receive                                                |  Transmit
 face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed
    lo: 100 1 0 0 0 0 0 0 200 2 0 0 0 0 0 0
  eth0: 300 3 0 0 0 0 0 0 400 4 0 0 0 0 0 0
  eth1: 500 5 0 0 0 0 0 0 600 6 0 0 0 0 0 0
`
	rx, tx := networkBytesFrom(strings.NewReader(input), "eth1")
	if rx != 500 || tx != 600 {
		t.Fatalf("network bytes = %d/%d, want 500/600", rx, tx)
	}
}

func TestCollectMetricsMarksDNSFailure(t *testing.T) {
	metric := CollectMetrics(context.Background(), nil, config.Config{DNS: config.DNSConfig{ListenUDP: "invalid-address"}, Network: config.NetworkConfig{IngressIface: "missing0"}})
	if metric.DNSOK || metric.DNSLatencyMS != 0 {
		t.Fatalf("DNS result = ok:%v latency:%v", metric.DNSOK, metric.DNSLatencyMS)
	}
	if metric.DNSError == "" || metric.DOTOK || metric.DOTError == "" {
		t.Fatalf("missing explicit failures: %+v", metric)
	}
}

func TestUDPHealthProbeRejectsSERVFAILAndEmptyAnswers(t *testing.T) {
	for _, mode := range []string{"ok", "servfail", "empty"} {
		t.Run(mode, func(t *testing.T) {
			packet, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := &dns.Server{PacketConn: packet, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, request *dns.Msg) {
				response := new(dns.Msg)
				response.SetReply(request)
				if mode == "servfail" {
					response.Rcode = dns.RcodeServerFailure
				}
				if mode == "ok" {
					response.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: request.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP("192.0.2.10")}}
				}
				_ = w.WriteMsg(response)
			})}
			go func() { _ = server.ActivateAndServe() }()
			t.Cleanup(func() { server.Shutdown() })
			err = probeDNS(context.Background(), packet.LocalAddr().String())
			if (err == nil) != (mode == "ok") {
				t.Fatalf("mode %s, error %v", mode, err)
			}
		})
	}
}

func TestSwapBytesFromProcStatus(t *testing.T) {
	input := "Name:\tsmartdns\nVmRSS:\t2048 kB\nVmSwap:\t1536 kB\n"
	got, err := swapBytesFrom(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if got != 1536*1024 {
		t.Fatalf("swap bytes = %d, want %d", got, 1536*1024)
	}
}

func TestSwapBytesFromProcStatusRejectsMalformedValue(t *testing.T) {
	for _, input := range []string{"Name:\tsmartdns\n", "VmSwap:\tinvalid kB\n", "VmSwap:\t10 MB\n"} {
		if _, err := swapBytesFrom(strings.NewReader(input)); err == nil {
			t.Fatalf("expected %q to fail", input)
		}
	}
}
