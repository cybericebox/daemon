package mailUseCase

import "strings"

// reservedTLDs are the top-level names RFC 2606 reserves: they never resolve to a mail server, so a message to
// them can only bounce and hurt the reputation of the sending provider. The load-test and seed accounts use them.
var reservedTLDs = map[string]bool{"invalid": true, "test": true, "example": true, "localhost": true}

// reservedAddress reports whether the address ends in a reserved top-level name.
func reservedAddress(addr string) bool {
	at := strings.LastIndexByte(addr, '@')
	if at < 0 {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(addr[at+1:]), "."))
	if i := strings.LastIndexByte(host, '.'); i >= 0 {
		host = host[i+1:]
	}
	return reservedTLDs[host]
}
