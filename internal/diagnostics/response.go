package diagnostics

import (
	"fmt"
	"net"

	"github.com/miekg/dns"
)

// ValidateDNSResponse checks an A probe's reply, including SERVFAIL and empty answers.
func ValidateDNSResponse(request, response *dns.Msg) error {
	if !response.Response || response.Id != request.Id || response.Opcode != request.Opcode {
		return fmt.Errorf("DNS response does not match request")
	}
	if len(response.Question) != 1 || response.Question[0] != request.Question[0] {
		return fmt.Errorf("DNS response question does not match request")
	}
	if response.Rcode != dns.RcodeSuccess {
		return fmt.Errorf("DNS response code: %s", dns.RcodeToString[response.Rcode])
	}
	for _, answer := range response.Answer {
		if record, ok := answer.(*dns.A); ok && record.Hdr.Class == dns.ClassINET && net.IP(record.A).To4() != nil {
			return nil
		}
	}
	return fmt.Errorf("DNS response contains no A answer")
}
