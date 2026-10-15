package service

import (
	"fmt"
	"net"
	"strings"
)

// NormalizeMAC returns mac in upper case with colons, the form the firmware sends.
func NormalizeMAC(mac string) (string, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) != 6 {
		return "", fmt.Errorf("invalid mac %q", mac)
	}
	return strings.ToUpper(hw.String()), nil
}
