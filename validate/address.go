package validate

import "net/netip"

func canonicalAddress(address string) string {
	if ip, err := netip.ParseAddr(address); err == nil {
		return ip.Unmap().String()
	}
	return address
}
