package exerciseModel

import (
	"encoding/json"
	"net"
	"slices"
	"strconv"
	"strings"
)

// hasTaskDescriptionContent accepts text or an inline dynamic value, not an
// empty editor document. Draft saves intentionally skip this publish-only rule.
func hasTaskDescriptionContent(description json.RawMessage) bool {
	var document any
	if len(description) == 0 || json.Unmarshal(description, &document) != nil {
		return false
	}
	var walk func(any) bool
	walk = func(value any) bool {
		switch node := value.(type) {
		case map[string]any:
			if node["type"] == "text" {
				content, _ := node["text"].(string)
				return strings.TrimSpace(content) != ""
			}
			if node["type"] == "variable" {
				name, _ := node["varName"].(string)
				return name != ""
			}
			for _, child := range node {
				if walk(child) {
					return true
				}
			}
		case []any:
			for _, child := range node {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	return walk(document)
}

type PlaceholderKind string

const (
	// PlaceholderGatewaySubnet is the /24 of a gateway of the lab (Placeholder.Gateway names the gateway device).
	PlaceholderGatewaySubnet PlaceholderKind = "gateway.subnet"
	PlaceholderIP            PlaceholderKind = "ip"            // IP constructor
	PlaceholderExternalLink  PlaceholderKind = "external.link" // proxy access URL
)

const maxPlaceholderPathLength = 200

// PlaceholderLinkSchemes is the one list of schemes an IP placeholder link may
// use. Adding a scheme (ftp, ssh, rdp…) means adding it here and to the
// editor's LINK_SCHEMES (exercises-frontend src/lib/placeholderLink.ts); the
// URL itself is built the same way for every scheme: scheme://ip[:port][path].
var PlaceholderLinkSchemes = []string{"http", "https"}

// ipLinkInvalid reports whether the link form of an IP placeholder is wrong:
// link fields without AsLink, a mask on a link, an unknown scheme, a port out
// of range or a path that is not a plain absolute path. Every rule shares the
// ONE ErrPlaceholderLinkInvalid call site in validatePlaceholders.
func ipLinkInvalid(p Placeholder) bool {
	if !p.AsLink {
		return p.Scheme != "" || p.Port != 0 || p.Path != ""
	}
	if p.ShowMask || !slices.Contains(PlaceholderLinkSchemes, p.Scheme) {
		return true
	}
	if p.Port < 0 || p.Port > 65535 {
		return true
	}
	if p.Path == "" {
		return false
	}
	return len(p.Path) > maxPlaceholderPathLength || !strings.HasPrefix(p.Path, "/") ||
		strings.ContainsAny(p.Path, " \t\r\n\"'`<>\\")
}

// Placeholder is a structured description placeholder declared by the editor;
// it is validated against the variant's topology at publish time.
type Placeholder struct {
	Key  string          `json:"key,omitempty"`
	Kind PlaceholderKind `json:"kind"`

	// IP constructor (Kind == PlaceholderIP)
	IPReference string `json:"ip_reference,omitempty"` // "gateway" | "static"
	// Gateway (gateway.subnet, and ip with ip_reference "gateway") is the name of the gateway device whose /24 it is.
	Gateway    string `json:"gateway,omitempty"`
	Octets1to3 string `json:"octets_1to3,omitempty"` // static only
	LastOctet  int    `json:"last_octet,omitempty"`  // 0..255
	ShowMask   bool   `json:"show_mask,omitempty"`

	// IP constructor link form: the resolved value is scheme://ip[:port][path].
	AsLink bool   `json:"as_link,omitempty"`
	Scheme string `json:"scheme,omitempty"` // one of PlaceholderLinkSchemes; required with AsLink
	Port   int    `json:"port,omitempty"`   // 1..65535; 0 = the scheme default
	Path   string `json:"path,omitempty"`   // starts with "/"; no spaces or quotes

	// External link (Kind == PlaceholderExternalLink): a link to one access entry of the device. Access is the kind of the
	// entry (empty = http). The device must have an entry of that kind; vnc and rdp validate and resolve to nothing yet.
	DeviceName string     `json:"device_name,omitempty"`
	Access     AccessKind `json:"access,omitempty"`
}

// validateInlinePlaceholderRefs checks only explicit Lexical variable nodes.
// Legacy documents without such nodes remain unchanged and publish normally.
func validateInlinePlaceholderRefs(description json.RawMessage, placeholders []Placeholder) error {
	if len(description) == 0 {
		return nil
	}
	var document any
	if err := json.Unmarshal(description, &document); err != nil {
		return ErrPlaceholderInvalid.Err()
	}
	known := make(map[string]struct{}, len(placeholders))
	for _, placeholder := range placeholders {
		if placeholder.Key != "" {
			known[placeholder.Key] = struct{}{}
		}
	}
	var walk func(any) bool
	walk = func(value any) bool {
		switch node := value.(type) {
		case map[string]any:
			if node["type"] == "variable" {
				name, ok := node["varName"].(string)
				if !ok {
					return false
				}
				if _, exists := known[name]; !exists {
					return false
				}
			}
			for _, child := range node {
				if !walk(child) {
					return false
				}
			}
		case []any:
			for _, child := range node {
				if !walk(child) {
					return false
				}
			}
		}
		return true
	}
	if !walk(document) {
		return ErrPlaceholderInvalid.Err()
	}
	return nil
}

// validatePlaceholders rejects placeholders that are malformed or reference
// nodes absent from the topology. Ported from the legacy daemon; the subnet
// contract for IP references is a fixed /24.
func (t Topology) validatePlaceholders(phs []Placeholder) error {
	deviceByName := make(map[string]Device, len(t.Devices))
	for _, d := range t.Devices {
		deviceByName[d.Name] = d
	}
	for _, p := range phs {
		if p.Kind == PlaceholderIP && ipLinkInvalid(p) {
			return ErrPlaceholderLinkInvalid.WithContext("placeholder", string(p.Kind)).Err()
		}
		malformed, missingNode := classifyPlaceholder(t, deviceByName, p)
		if malformed {
			return ErrPlaceholderInvalid.WithContext("placeholder", string(p.Kind)).Err()
		}
		if missingNode {
			failure := ErrPlaceholderNode.WithContext("placeholder", string(p.Kind))
			if p.Kind == PlaceholderExternalLink {
				failure = failure.WithPublicContext("device", p.DeviceName).WithPublicContext("access", string(p.EffectiveAccess()))
			}
			return failure.Err()
		}
	}
	return nil
}

// classifyPlaceholder reports (malformed, missingNode) for one placeholder.
func classifyPlaceholder(t Topology, byName map[string]Device, p Placeholder) (bool, bool) {
	switch p.Kind {
	case PlaceholderGatewaySubnet:
		return p.Gateway == "", p.Gateway != "" && !t.gatewayRefValid(p.Gateway)
	case PlaceholderIP:
		if p.LastOctet < 0 || p.LastOctet > 255 {
			return true, false
		}
		switch p.IPReference {
		case "gateway":
			return p.Gateway == "", p.Gateway != "" && !t.gatewayRefValid(p.Gateway)
		case "static":
			address := net.ParseIP(p.Octets1to3 + "." + strconv.Itoa(p.LastOctet))
			return address == nil || address.To4() == nil, false
		default:
			return true, false
		}
	case PlaceholderExternalLink:
		if !p.EffectiveAccess().Valid() {
			return true, false
		}
		d, ok := byName[p.DeviceName]
		return false, !ok || !d.HasAccess(p.EffectiveAccess())
	default:
		return true, false
	}
}

// EffectiveAccess is the access kind an external.link placeholder points to (http when it names none).
func (p Placeholder) EffectiveAccess() AccessKind {
	if p.Access == "" {
		return AccessHTTP
	}
	return p.Access
}
