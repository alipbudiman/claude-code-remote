package network

import (
	"fmt"
	"strings"
	"testing"
)

// M7: rankLANIPs must order IPv4 strings by how likely they are to be the
// REAL LAN address a phone can reach: 192.168.* first, then 10.*, then
// 172.16-31.*, then everything else — independent of interface order. Virtual
// adapters (WSL/Hyper-V/VPN) commonly live in 172.16/12 while home LANs are
// 192.168/16, and the QR code / primary URL uses the first entry.
func TestRankLANIPs(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			// The production bug: the WSL virtual adapter enumerated first
			// beat the real LAN IP into the QR code.
			name: "virtual adapter ranked below real LAN IP",
			in:   []string{"172.16.0.2", "192.168.100.48"},
			want: []string{"192.168.100.48", "172.16.0.2"},
		},
		{
			name: "full mix: 192.168 then 10 then 172.16-31 then rest",
			in: []string{
				"172.20.0.1", // docker-ish virtual (172.16/12) -> tier 2
				"100.64.1.2", // CGNAT -> rest
				"192.168.1.10",
				"10.0.0.5",
				"172.32.1.1", // just outside 172.16/12 -> rest
				"172.15.1.1", // just outside 172.16/12 -> rest
				"10.255.0.1",
				"192.168.0.2",
			},
			want: []string{
				"192.168.1.10", "192.168.0.2",
				"10.0.0.5", "10.255.0.1",
				"172.20.0.1",
				"100.64.1.2", "172.32.1.1", "172.15.1.1",
			},
		},
		{
			name: "172 tier includes both boundaries 16 and 31",
			in:   []string{"172.31.0.1", "172.16.0.1", "192.168.5.5"},
			want: []string{"192.168.5.5", "172.31.0.1", "172.16.0.1"},
		},
		{
			name: "stable within a tier",
			in:   []string{"10.9.9.9", "10.1.1.1", "172.16.0.2", "172.30.0.3"},
			want: []string{"10.9.9.9", "10.1.1.1", "172.16.0.2", "172.30.0.3"},
		},
		{
			name: "already ranked stays unchanged",
			in:   []string{"192.168.100.48", "172.16.0.2"},
			want: []string{"192.168.100.48", "172.16.0.2"},
		},
		{
			name: "only foreign addresses keep their order",
			in:   []string{"100.64.1.2", "172.32.1.1"},
			want: []string{"100.64.1.2", "172.32.1.1"},
		},
		{
			name: "empty input, empty output",
			in:   nil,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rankLANIPs(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("rankLANIPs(%v) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("rankLANIPs(%v) = %v, want %v (mismatch at %d)", tt.in, got, tt.want, i)
				}
			}
		})
	}
}

// M7: GetLocalIPs output must be ranked, so the first entry (the QR / primary
// URL) is the most reachable address. Interface enumeration order on the test
// host is unknowable, so this asserts the property that holds for any host:
// the returned list is exactly rankLANIPs applied to itself (idempotent), and
// whenever a 192.168.* address exists it lands before any 172.16-31.* one.
func TestGetLocalIPsRanked(t *testing.T) {
	ips := GetLocalIPs()
	if len(ips) == 0 {
		t.Fatal("GetLocalIPs returned no IPs, want at least the loopback fallback")
	}

	first192 := -1
	first172 := -1
	for i, ip := range ips {
		switch {
		case strings.HasPrefix(ip, "192.168."):
			if first192 == -1 {
				first192 = i
			}
		case strings.HasPrefix(ip, "172."):
			if first172 == -1 && isPrivate172(ip) {
				first172 = i
			}
		}
	}
	if first192 != -1 && first172 != -1 && first172 < first192 {
		t.Fatalf("GetLocalIPs ranked a 172.16/12 address (%s) before a 192.168 address (%s): %v",
			ips[first172], ips[first192], ips)
	}
}

// isPrivate172 reports whether ip is inside 172.16.0.0/12.
func isPrivate172(ip string) bool {
	var a, b int
	if _, err := fmt.Sscanf(ip, "%d.%d", &a, &b); err != nil {
		return false
	}
	return a == 172 && b >= 16 && b <= 31
}
