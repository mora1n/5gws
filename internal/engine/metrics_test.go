package engine

import (
	"strings"
	"testing"
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
	metric := CollectMetrics(nil, "invalid-address", "missing0")
	if metric.DNSOK || metric.DNSLatencyMS != 0 {
		t.Fatalf("DNS result = ok:%v latency:%v", metric.DNSOK, metric.DNSLatencyMS)
	}
}
