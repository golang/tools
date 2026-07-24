package conv

import (
	"net"
	"strings"
)

func _(s string) net.IP {
	var result net.IP
	for _, b := range net.IP(strings.Split(s, ",")[0]) { // nope: cannot modernize with the conversion
		result = append(result, b)
	}
	return result
}
