package network

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/skip2/go-qrcode"
)

// GetLocalIPs returns all non-loopback IPv4 addresses on the host machine,
// ranked so the most reachable address comes first (see rankLANIPs).
func GetLocalIPs() []string {
	var ips []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return []string{"127.0.0.1"}
	}

	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				ipStr := ipNet.IP.String()
				// Filter out autoconfiguration 169.254.x.x
				if !strings.HasPrefix(ipStr, "169.254.") {
					ips = append(ips, ipStr)
				}
			}
		}
	}

	if len(ips) == 0 {
		ips = append(ips, "127.0.0.1")
	}
	return rankLANIPs(ips)
}

// rankLANIPs returns the input IPv4 strings reordered by likelihood of being
// the REAL LAN address a phone can reach: 192.168.* first, then 10.*, then
// 172.16-31.*, then everything else (stable within each tier). Rationale:
// interface enumeration order is arbitrary, and virtual adapters
// (WSL/Hyper-V/VPN) commonly live in 172.16/12 while home LANs are 192.168/16
// — without ranking, a virtual adapter like 172.16.0.2 won the first slot and
// ended up in the QR code / primary URL instead of the real LAN IP.
func rankLANIPs(ips []string) []string {
	ranked := make([]string, len(ips))
	copy(ranked, ips)
	sort.SliceStable(ranked, func(i, j int) bool {
		return lanIPRank(ranked[i]) < lanIPRank(ranked[j])
	})
	return ranked
}

// lanIPRank maps one IPv4 string to its rankLANIPs tier.
func lanIPRank(ip string) int {
	octets := strings.Split(ip, ".")
	if len(octets) != 4 {
		return 3
	}
	switch {
	case strings.HasPrefix(ip, "192.168."):
		return 0
	case strings.HasPrefix(ip, "10."):
		return 1
	}
	if octets[0] == "172" {
		if second, err := strconv.Atoi(octets[1]); err == nil && second >= 16 && second <= 31 {
			return 2
		}
	}
	return 3
}

// GenerateTerminalQRCode prints an ASCII QR code to standard output for instant scanning from phone
func GenerateTerminalQRCode(content string) string {
	qr, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return ""
	}
	return qr.ToSmallString(false)
}

// GenerateQRCodePNG generates PNG bytes for the QR code
func GenerateQRCodePNG(content string, size int) ([]byte, error) {
	return qrcode.Encode(content, qrcode.Medium, size)
}

// FormatServerURLs returns formatted HTTP & WebSocket URLs for all local IPs
func FormatServerURLs(ips []string, port int) []string {
	var urls []string
	for _, ip := range ips {
		urls = append(urls, fmt.Sprintf("http://%s:%d", ip, port))
	}
	return urls
}
