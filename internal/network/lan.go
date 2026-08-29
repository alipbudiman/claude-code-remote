package network

import (
	"fmt"
	"net"
	"strings"

	"github.com/skip2/go-qrcode"
)

// GetLocalIPs returns all non-loopback IPv4 addresses on the host machine
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
	return ips
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
