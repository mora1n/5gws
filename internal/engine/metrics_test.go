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
