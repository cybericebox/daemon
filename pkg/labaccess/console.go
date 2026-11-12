package labaccess

import (
	"errors"
	"net/url"
	"strings"
)

// TerminalHost is the first label of the host that serves the device consoles of a lab (the chart value terminal.host of
// the laboratory; "terminal" by default). The console of a device is one page on that host, so the access token is signed
// with this host instead of <device>-<code>.
const TerminalHost = "terminal"

// ConsoleSession gives the Session that opens the console of one device of a lab: the access URL on the terminal host and the
// `next` page /?lab=<lab>&device=<device>. webURL is any web address of a device of the same lab (https://<device>-<code>.<base>/...)
// and only tells the scheme and the base domain; a lab with no web device cannot say it, and the console is then not available.
func ConsoleSession(webURL, lab, device string) (accessURL, next string, err error) {
	origin, parseErr := url.Parse(webURL)
	if parseErr != nil || origin.Scheme == "" || origin.Hostname() == "" || lab == "" || device == "" {
		return "", "", errors.New("labaccess: no base address for the console")
	}
	_, base, found := strings.Cut(origin.Hostname(), ".")
	if !found || base == "" {
		return "", "", errors.New("labaccess: no base address for the console")
	}
	host := TerminalHost + "." + base
	if port := origin.Port(); port != "" {
		host += ":" + port
	}
	console := url.URL{Scheme: origin.Scheme, Host: host, Path: "/"}
	return console.String(), "/?" + url.Values{"lab": {lab}, "device": {device}}.Encode(), nil
}
