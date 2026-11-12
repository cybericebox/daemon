package exerciseModel

import (
	"net"
	"regexp"
	"strings"
	"unicode"
)

// The observations of check.state next to file and custom (docs/specs/2026-11-11-exercise-schema-1.0.md section 4.2). All
// of them but custom are read by the platform's own program inside the device and run nothing of the device. The keys
// are the document's; a condition holds when every key that is written holds.

// Limits of the observations. Each is part of the document contract.
const (
	// MaxDirectoryEntries bounds has_entries and lacks_entries of a directory observation.
	MaxDirectoryEntries = 32
	// MaxEntryNameBytes bounds one directory entry name.
	MaxEntryNameBytes = 255
	// MaxProcessNameBytes bounds a process name.
	MaxProcessNameBytes = 256
	// MaxKernelParamBytes bounds a sysctl name.
	MaxKernelParamBytes = 256
	// MaxRequestHostBytes bounds the host of a request; MaxRequestPathBytes its path.
	MaxRequestHostBytes = 253
	MaxRequestPathBytes = 2048
)

type (
	// DirectoryObservation looks at a directory of the device. Exists is true unless set to false; false stands alone.
	DirectoryObservation struct {
		Path         string   `json:"path"`
		Exists       *bool    `json:"exists,omitempty"`
		Mode         string   `json:"mode,omitempty"`
		Owner        string   `json:"owner,omitempty"`
		Group        string   `json:"group,omitempty"`
		HasEntries   []string `json:"has_entries,omitempty"`
		LacksEntries []string `json:"lacks_entries,omitempty"`
	}

	// ProcessObservation looks for a process by name or by a pattern on its command line. Running is true unless set to
	// false.
	ProcessObservation struct {
		Name    string `json:"name,omitempty"`
		Matches string `json:"matches,omitempty"`
		User    string `json:"user,omitempty"`
		Running *bool  `json:"running,omitempty"`
	}

	// PortObservation looks at a port the device listens on. Protocol is tcp or udp (empty = tcp); Listening is true
	// unless set to false; Address is a local address (empty = any); Process names the owner of the listener.
	PortObservation struct {
		Port      int32  `json:"port"`
		Protocol  string `json:"protocol,omitempty"`
		Listening *bool  `json:"listening,omitempty"`
		Address   string `json:"address,omitempty"`
		Process   string `json:"process,omitempty"`
	}

	// KernelParamObservation reads a sysctl by its dotted name.
	KernelParamObservation struct {
		Name    string `json:"name"`
		Equals  string `json:"equals,omitempty"`
		Matches string `json:"matches,omitempty"`
	}

	// RequestObservation makes a connection from inside the device: tcp opens a connection, http and https make a request
	// (Method GET or HEAD, empty = GET; Path empty = /; Status empty = 200). Host empty = 127.0.0.1.
	RequestObservation struct {
		Protocol     string `json:"protocol"`
		Host         string `json:"host,omitempty"`
		Port         int32  `json:"port"`
		Method       string `json:"method,omitempty"`
		Path         string `json:"path,omitempty"`
		Status       int32  `json:"status,omitempty"`
		BodyContains string `json:"body_contains,omitempty"`
		BodyMatches  string `json:"body_matches,omitempty"`
	}

	// PermissionObservation tests, without changing anything, whether User may read, write or execute Path. Each written
	// boolean must equal the real answer; one left out (nil) is not tested.
	PermissionObservation struct {
		User    string `json:"user"`
		Path    string `json:"path"`
		Read    *bool  `json:"read,omitempty"`
		Write   *bool  `json:"write,omitempty"`
		Execute *bool  `json:"execute,omitempty"`
	}
)

// ObservationKind names an observation in the document.
type ObservationKind string

// The kinds of observation a condition may hold, in the order the document lists them.
const (
	ObservationFile        ObservationKind = "file"
	ObservationDirectory   ObservationKind = "directory"
	ObservationProcess     ObservationKind = "process"
	ObservationPort        ObservationKind = "port"
	ObservationKernelParam ObservationKind = "kernel_param"
	ObservationRequest     ObservationKind = "request"
	ObservationPermission  ObservationKind = "permission"
	ObservationCustom      ObservationKind = "custom"
)

// ObservationKinds lists every kind.
var ObservationKinds = []ObservationKind{
	ObservationFile, ObservationDirectory, ObservationProcess, ObservationPort,
	ObservationKernelParam, ObservationRequest, ObservationPermission, ObservationCustom,
}

// Kinds returns the kinds the condition holds, in document order; a valid condition holds exactly one.
func (c Condition) Kinds() []ObservationKind {
	var out []ObservationKind
	for _, held := range []struct {
		kind ObservationKind
		set  bool
	}{
		{ObservationFile, c.File != nil}, {ObservationDirectory, c.Directory != nil},
		{ObservationProcess, c.Process != nil}, {ObservationPort, c.Port != nil},
		{ObservationKernelParam, c.KernelParam != nil}, {ObservationRequest, c.Request != nil},
		{ObservationPermission, c.Permission != nil}, {ObservationCustom, c.Custom != nil},
	} {
		if held.set {
			out = append(out, held.kind)
		}
	}
	return out
}

var (
	checksumRe    = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	kernelParamRe = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$`)
	hostRe        = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
	methodRe      = regexp.MustCompile(`^(GET|HEAD)$`)
)

func hasSubstitution(s string) bool { return strings.Contains(s, "{{") }

// validEntryName: one name directly in a directory.
func validEntryName(s string) bool {
	return s != "" && s != "." && s != ".." && len(s) <= MaxEntryNameBytes && !strings.ContainsAny(s, "/\x00") &&
		!strings.ContainsFunc(s, unicode.IsControl)
}

func validEntryNames(list []string) bool {
	if len(list) > MaxDirectoryEntries {
		return false
	}
	for _, name := range list {
		if !validEntryName(name) {
			return false
		}
	}
	return true
}

func validPort(p int32) bool { return p >= 1 && p <= 65535 }

// validObservationShape checks one observation other than file and custom; it returns the document key that is wrong
// ("directory.path"), or "". Text with substitutions is checked as it will read once they are made (stand-ins).
func validObservationShape(c Condition) string {
	switch {
	case c.Directory != nil:
		return validDirectoryShape(*c.Directory)
	case c.Process != nil:
		return validProcessShape(*c.Process)
	case c.Port != nil:
		return validPortShape(*c.Port)
	case c.KernelParam != nil:
		return validKernelParamShape(*c.KernelParam)
	case c.Request != nil:
		return validRequestShape(*c.Request)
	case c.Permission != nil:
		return validPermissionShape(*c.Permission)
	}
	return ""
}

func validDirectoryShape(d DirectoryObservation) string {
	switch {
	case !validAbsolutePath(substitutionStandIn(d.Path)):
		return "directory.path"
	case d.Mode != "" && !fileModeRe.MatchString(d.Mode):
		return "directory.mode"
	case d.Owner != "" && !accountNameRe.MatchString(d.Owner):
		return "directory.owner"
	case d.Group != "" && !accountNameRe.MatchString(d.Group):
		return "directory.group"
	case !validEntryNames(d.HasEntries):
		return "directory.has_entries"
	case !validEntryNames(d.LacksEntries):
		return "directory.lacks_entries"
	case d.Exists != nil && !*d.Exists &&
		(d.Mode != "" || d.Owner != "" || d.Group != "" || len(d.HasEntries) > 0 || len(d.LacksEntries) > 0):
		return "directory.exists"
	}
	return ""
}

func validProcessShape(p ProcessObservation) string {
	switch {
	case p.Name == "" && p.Matches == "":
		return "process.name"
	case len(p.Name) > MaxProcessNameBytes || strings.ContainsAny(p.Name, "/\x00") || !validExpectationText(p.Name):
		return "process.name"
	case !validExpectationRegexp(p.Matches):
		return "process.matches"
	case p.User != "" && !accountNameRe.MatchString(p.User):
		return "process.user"
	case p.Running != nil && !*p.Running && p.User != "":
		return "process.running"
	}
	return ""
}

func validPortShape(p PortObservation) string {
	switch {
	case !validPort(p.Port):
		return "port.port"
	case p.Protocol != "" && p.Protocol != "tcp" && p.Protocol != "udp":
		return "port.protocol"
	case p.Address != "" && net.ParseIP(p.Address) == nil:
		return "port.address"
	case len(p.Process) > MaxProcessNameBytes || strings.ContainsAny(p.Process, "/\x00") || !validExpectationText(p.Process):
		return "port.process"
	case p.Listening != nil && !*p.Listening && (p.Address != "" || p.Process != ""):
		return "port.listening"
	}
	return ""
}

func validKernelParamShape(k KernelParamObservation) string {
	switch {
	case k.Name == "" || len(k.Name) > MaxKernelParamBytes || !kernelParamRe.MatchString(substitutionStandIn(k.Name)):
		return "kernel_param.name"
	case k.Equals == "" && k.Matches == "":
		return "kernel_param.equals"
	case !validExpectationText(k.Equals):
		return "kernel_param.equals"
	case !validExpectationRegexp(k.Matches):
		return "kernel_param.matches"
	}
	return ""
}

func validRequestShape(r RequestObservation) string {
	web := r.Protocol == "http" || r.Protocol == "https"
	switch {
	case !web && r.Protocol != "tcp":
		return "request.protocol"
	case !validPort(r.Port):
		return "request.port"
	case r.Host != "" && (len(r.Host) > MaxRequestHostBytes || !validRequestHost(substitutionStandIn(r.Host))):
		return "request.host"
	case !web && r.Method != "":
		return "request.method"
	case !web && r.Path != "":
		return "request.path"
	case !web && r.Status != 0:
		return "request.status"
	case !web && (r.BodyContains != "" || r.BodyMatches != ""):
		return "request.body_contains"
	case r.Method != "" && !methodRe.MatchString(r.Method):
		return "request.method"
	case r.Path != "" && !validRequestPath(substitutionStandIn(r.Path)):
		return "request.path"
	case r.Status != 0 && (r.Status < 100 || r.Status > 599):
		return "request.status"
	case !validExpectationText(r.BodyContains):
		return "request.body_contains"
	case !validExpectationRegexp(r.BodyMatches):
		return "request.body_matches"
	}
	return ""
}

func validRequestHost(s string) bool { return net.ParseIP(s) != nil || hostRe.MatchString(s) }

func validRequestPath(s string) bool {
	return len(s) <= MaxRequestPathBytes && strings.HasPrefix(s, "/") &&
		!strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) })
}

func validPermissionShape(p PermissionObservation) string {
	switch {
	case p.User == "" || !accountNameRe.MatchString(p.User):
		return "permission.user"
	case !validAbsolutePath(substitutionStandIn(p.Path)):
		return "permission.path"
	case p.Read == nil && p.Write == nil && p.Execute == nil:
		return "permission.read"
	}
	return ""
}

// MapTexts returns the condition with every text key of its observation passed through f: the substitutions of the
// document work in contains, matches, equals, checksum, path, body_contains, body_matches, output_*, run, host. f gets the
// place of the text (for example "file.contains"), the text and whether it is a regular expression. The condition itself
// is not changed.
func (c Condition) MapTexts(f func(place, text string, pattern bool) (string, error)) (Condition, error) {
	var err error
	text := func(place string, s *string, pattern bool) {
		if err == nil && *s != "" {
			*s, err = f(place, *s, pattern)
		}
	}
	if c.File != nil {
		v := *c.File
		text("file.path", &v.Path, false)
		text("file.contains", &v.Contains, false)
		text("file.matches", &v.Matches, true)
		text("file.checksum", &v.Checksum, false)
		c.File = &v
	}
	if c.Directory != nil {
		v := *c.Directory
		text("directory.path", &v.Path, false)
		c.Directory = &v
	}
	if c.Process != nil {
		v := *c.Process
		text("process.name", &v.Name, false)
		text("process.matches", &v.Matches, true)
		c.Process = &v
	}
	if c.Port != nil {
		v := *c.Port
		text("port.process", &v.Process, false)
		c.Port = &v
	}
	if c.KernelParam != nil {
		v := *c.KernelParam
		text("kernel_param.name", &v.Name, false)
		text("kernel_param.equals", &v.Equals, false)
		text("kernel_param.matches", &v.Matches, true)
		c.KernelParam = &v
	}
	if c.Request != nil {
		v := *c.Request
		text("request.host", &v.Host, false)
		text("request.path", &v.Path, false)
		text("request.body_contains", &v.BodyContains, false)
		text("request.body_matches", &v.BodyMatches, true)
		c.Request = &v
	}
	if c.Permission != nil {
		v := *c.Permission
		text("permission.path", &v.Path, false)
		c.Permission = &v
	}
	if c.Custom != nil {
		v := *c.Custom
		text("custom.run", &v.Run, false)
		text("custom.output_contains", &v.OutputContains, false)
		text("custom.output_matches", &v.OutputMatches, true)
		c.Custom = &v
	}
	return c, err
}
