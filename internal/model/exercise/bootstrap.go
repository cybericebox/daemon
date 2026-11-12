package exerciseModel

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model/flagpattern"
)

// Limits of the device runtime keys and of the variables. Each is part of the document contract.
const (
	// MaxDeviceEnvVars bounds the env vars of one device.
	MaxDeviceEnvVars = 64
	// MaxEnvValueBytes bounds one env value.
	MaxEnvValueBytes = 4096
	// MaxBootstrapFiles bounds the files one bootstrap places.
	MaxBootstrapFiles = 32
	// MaxBootstrapContentBytes bounds the inline content of one file and the script.
	MaxBootstrapContentBytes = 64 * 1024
	// MaxBootstrapFileBytes bounds one bootstrap file, inline or a file of the archive (Source): the laboratory's own
	// ceiling. MaxBootstrapDeviceBytes bounds the script and all files of one device together (a Secret holds 1 MiB and
	// the files go in as base64). The start of a lab and the draft save and import check the same numbers.
	MaxBootstrapFileBytes   = 256 * 1024
	MaxBootstrapDeviceBytes = 512 * 1024
	// MinBootstrapTimeout and MaxBootstrapTimeout bound the time limit of a bootstrap (absent: the lab's default).
	MinBootstrapTimeout = time.Second
	MaxBootstrapTimeout = 10 * time.Minute
	// MaxCommandEntries and MaxArgsEntries bound the service command and its arguments; MaxCommandEntryBytes one entry.
	MaxCommandEntries    = 32
	MaxArgsEntries       = 64
	MaxCommandEntryBytes = 4096
	// MaxVariables bounds the variables of one topology; MaxVariableValueBytes one constant value.
	MaxVariables          = 64
	MaxVariableValueBytes = 1024
)

type (
	// SystemKind is the guest system of a node; reserved for the vm profile.
	SystemKind string
	// HardwareKind is the kind of virtual hardware of a node; reserved for the vm profile.
	HardwareKind string
)

const (
	SystemLinux   SystemKind = "linux"
	SystemWindows SystemKind = "windows"

	HardwareFast       HardwareKind = "fast"
	HardwareCompatible HardwareKind = "compatible"
)

func (s SystemKind) Valid() bool { return s == "" || s == SystemLinux || s == SystemWindows }

func (h HardwareKind) Valid() bool { return h == "" || h == HardwareFast || h == HardwareCompatible }

type (
	// Bootstrap is everything that happens to a clean device before its service starts, in one block. It runs as
	// root, only on a clean filesystem (first start, reset). A task value is a substitution ({{task.<key>}}) in an
	// env value, a file content or the script.
	Bootstrap struct {
		// Timeout is a Go duration, MinBootstrapTimeout..MaxBootstrapTimeout; empty = the lab's default.
		Timeout string `json:"timeout,omitempty"`
		// Env are the variables of the service process. A value here is always visible to a participant inside the
		// device. Secret values are stored encrypted.
		Env         []EnvVar        `json:"env,omitempty"`
		Files       []BootstrapFile `json:"files,omitempty"`
		Script      string          `json:"script,omitempty"`
		Interpreter string          `json:"interpreter,omitempty"`
	}

	// BootstrapFile places one file: exactly one of Content (text, with substitutions) and Source (a file of the
	// archive, stored like an attachment).
	BootstrapFile struct {
		Path    string         `json:"path"`
		Content string         `json:"content,omitempty"`
		Source  *AttachmentRef `json:"source,omitempty"`
		Owner   string         `json:"owner,omitempty"`
		Group   string         `json:"group,omitempty"`
		Mode    string         `json:"mode,omitempty"`
	}

	// Access is one way a participant reaches the device in the browser, without VPN. Kind says which keys apply:
	// http uses Port and Protocol (http | https, empty = http); terminal uses Command (default /bin/sh) and User (default
	// the user of the image); vnc and rdp use Port and are reserved (accepted and stored, not served yet).
	Access struct {
		Kind     AccessKind `json:"kind"`
		Port     int32      `json:"port,omitempty"`
		Protocol string     `json:"protocol,omitempty"`
		Command  string     `json:"command,omitempty"`
		User     string     `json:"user,omitempty"`
	}

	// Variable is set once for the whole infrastructure of a variant and used as {{var.<name>}}: exactly one of
	// Value (a constant) and Template (a pattern, generated once per instance).
	Variable struct {
		Name     string `json:"name"`
		Value    string `json:"value,omitempty"`
		Template string `json:"template,omitempty"`
	}
)

// IsZero lets encoding/json omit an empty bootstrap.
func (b Bootstrap) IsZero() bool {
	return b.Timeout == "" && len(b.Env) == 0 && len(b.Files) == 0 && b.Script == "" &&
		b.Interpreter == ""
}

// AccessKind is the kind of an access entry.
type AccessKind string

const (
	AccessHTTP     AccessKind = "http"
	AccessTerminal AccessKind = "terminal"
	AccessVNC      AccessKind = "vnc"
	AccessRDP      AccessKind = "rdp"
)

// MaxAccessEntries bounds the access list of a device: one entry per kind.
const MaxAccessEntries = 4

// Valid reports a known kind.
func (k AccessKind) Valid() bool {
	switch k {
	case AccessHTTP, AccessTerminal, AccessVNC, AccessRDP:
		return true
	default:
		return false
	}
}

// Served reports whether the platform runs the kind (vnc and rdp are only accepted and stored for now).
func (k AccessKind) Served() bool { return k == AccessHTTP || k == AccessTerminal }

// AccessOf returns the entry of a kind.
func (d Device) AccessOf(kind AccessKind) (Access, bool) {
	for _, a := range d.Access {
		if a.Kind == kind {
			return a, true
		}
	}
	return Access{}, false
}

// HasAccess reports whether the device has an entry of the kind.
func (d Device) HasAccess(kind AccessKind) bool {
	_, ok := d.AccessOf(kind)
	return ok
}

// HTTPAccess is the web access of the device: its http entry with the protocol filled in (http when the entry leaves it out).
func (d Device) HTTPAccess() (Access, bool) {
	a, ok := d.AccessOf(AccessHTTP)
	if ok && a.Protocol == "" {
		a.Protocol = "http"
	}
	return a, ok
}

// validateAccess checks the access list of a node: known kinds, at most one entry of each, the keys each kind allows.
func validateAccess(list []Access, bad func(string) error) error {
	if len(list) > MaxAccessEntries {
		return bad("access")
	}
	seen := make(map[AccessKind]struct{}, len(list))
	for i, a := range list {
		field := fmt.Sprintf("access[%d]", i+1)
		if !a.Kind.Valid() {
			return bad(field + ".kind")
		}
		if _, dup := seen[a.Kind]; dup {
			return bad(field + ".kind")
		}
		seen[a.Kind] = struct{}{}
		switch a.Kind {
		case AccessTerminal:
			switch {
			case a.Port != 0:
				return bad(field + ".port")
			case a.Protocol != "":
				return bad(field + ".protocol")
			case a.Command != "" && !validInterpreter(a.Command):
				return bad(field + ".command")
			case a.User != "" && !accountNameRe.MatchString(a.User):
				return bad(field + ".user")
			}
			continue
		case AccessHTTP:
			if a.Protocol != "" && a.Protocol != "http" && a.Protocol != "https" {
				return bad(field + ".protocol")
			}
		default: // vnc, rdp
			if a.Protocol != "" {
				return bad(field + ".protocol")
			}
		}
		if a.Port < 1 || a.Port > 65535 {
			return bad(field + ".port")
		}
		if a.Command != "" {
			return bad(field + ".command")
		}
		if a.User != "" {
			return bad(field + ".user")
		}
	}
	return nil
}

// UnappliedKeys names, in document terms, the runtime keys of a node that the laboratory cannot apply on its profile: the
// bootstrap list (files, script), command, args and the terminal access of a vm-profile device need a guest agent that does
// not exist yet; the reserved access kinds vnc and rdp are not served on any profile. (A container and a micro-VM apply all of them; bootstrap.env is applied everywhere.)
func (d Device) UnappliedKeys() []string {
	var keys []string
	// vnc and rdp are reserved: nothing serves them on any profile, so a lab is not started as if they worked.
	for _, kind := range []AccessKind{AccessVNC, AccessRDP} {
		if d.HasAccess(kind) {
			keys = append(keys, "access."+string(kind))
		}
	}
	if d.EffectiveProfile() != NodeProfileVM {
		return keys
	}
	if len(d.Command) > 0 {
		keys = append(keys, "command")
	}
	if len(d.Args) > 0 {
		keys = append(keys, "args")
	}
	if len(d.Bootstrap.Files) > 0 {
		keys = append(keys, "bootstrap.files")
	}
	if d.Bootstrap.Script != "" {
		keys = append(keys, "bootstrap.script")
	}
	if d.HasAccess(AccessTerminal) {
		keys = append(keys, "access.terminal")
	}
	return keys
}

var (
	// slugRe is a task key and a variable name: it is written inside {{task.<key>}} and {{var.<name>}}.
	slugRe    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

// ValidKey reports whether a task key or variable name is well-formed.
func ValidKey(s string) bool { return slugRe.MatchString(s) }

// validateDeviceRuntime checks the runtime keys of one device on their own. It returns an error naming the
// document key that is wrong; the cross-references (substitutions) are checked per variant.
func validateDeviceRuntime(d Device) error {
	bad := func(field string) error {
		return ErrDeviceRuntimeInvalid.WithPublicContext("device", d.Name).
			WithPublicContext("field", field).
			Err()
	}
	if d.Type.IsForwarding() {
		if len(d.Command) > 0 || len(d.Args) > 0 || d.System != "" || d.Hardware != "" ||
			!d.Bootstrap.IsZero() ||
			len(d.Access) > 0 {
			return ErrDeviceTypeInvalid.WithContext("device", d.Name).Err()
		}
		return nil
	}
	if len(d.Command) > MaxCommandEntries || len(d.Args) > MaxArgsEntries {
		return bad("command")
	}
	for i, entry := range d.Command {
		if entry == "" || len(entry) > MaxCommandEntryBytes || strings.ContainsRune(entry, 0) {
			return bad(fmt.Sprintf("command[%d]", i+1))
		}
	}
	for i, entry := range d.Args {
		if len(entry) > MaxCommandEntryBytes || strings.ContainsRune(entry, 0) {
			return bad(fmt.Sprintf("args[%d]", i+1))
		}
	}
	if !d.System.Valid() {
		return bad("system")
	}
	if !d.Hardware.Valid() {
		return bad("hardware")
	}
	if d.EffectiveProfile() != NodeProfileVM {
		if d.System == SystemWindows {
			return bad("system")
		}
		if d.Hardware == HardwareCompatible {
			return bad("hardware")
		}
	}
	if err := validateAccess(d.Access, bad); err != nil {
		return err
	}
	return validateBootstrap(d, bad)
}

func validateBootstrap(d Device, bad func(string) error) error {
	b := d.Bootstrap
	if b.Timeout != "" && !validDurationIn(b.Timeout, MinBootstrapTimeout, MaxBootstrapTimeout) {
		return bad("bootstrap.timeout")
	}
	if len(b.Env) > MaxDeviceEnvVars {
		return bad("bootstrap.env")
	}
	names := make(map[string]struct{}, len(b.Env))
	for i, env := range b.Env {
		field := fmt.Sprintf("bootstrap.env[%d]", i+1)
		if !envNameRe.MatchString(env.Name) {
			return bad(field + ".name")
		}
		if _, duplicate := names[env.Name]; duplicate {
			return bad(field + ".name")
		}
		names[env.Name] = struct{}{}
		// A secret value is stored sealed; its length is not the author's text any more.
		if !env.Secret &&
			(len(env.Value) > MaxEnvValueBytes || strings.ContainsRune(env.Value, 0)) {
			return bad(field + ".value")
		}
	}
	if len(b.Files) > MaxBootstrapFiles {
		return bad("bootstrap.files")
	}
	paths := make(map[string]struct{}, len(b.Files))
	for i, file := range b.Files {
		field := fmt.Sprintf("bootstrap.files[%d]", i+1)
		switch {
		case !validAbsolutePath(file.Path):
			return bad(field + ".path")
		case (file.Source != nil) == (file.Content != ""):
			return bad(field + ".content")
		case len(file.Content) > MaxBootstrapContentBytes || strings.ContainsRune(file.Content, 0):
			return bad(field + ".content")
		case file.Source != nil && file.Source.FileID == uuid.Nil:
			return bad(field + ".source")
		case file.Owner != "" && !accountNameRe.MatchString(file.Owner):
			return bad(field + ".owner")
		case file.Group != "" && !accountNameRe.MatchString(file.Group):
			return bad(field + ".group")
		case file.Mode != "" && !fileModeRe.MatchString(file.Mode):
			return bad(field + ".mode")
		}
		if _, duplicate := paths[file.Path]; duplicate {
			return bad(field + ".path")
		}
		paths[file.Path] = struct{}{}
	}
	if len(b.Script) > MaxBootstrapContentBytes || strings.ContainsRune(b.Script, 0) {
		return bad("bootstrap.script")
	}
	if b.Interpreter != "" && (b.Script == "" || !validInterpreter(b.Interpreter)) {
		return bad("bootstrap.interpreter")
	}
	return nil
}

// validateVariables checks the variables of a topology: unique valid names, exactly one of value and template.
func validateVariables(vars []Variable) error {
	if len(vars) > MaxVariables {
		return ErrVariableInvalid.WithPublicContext("field", "variables").Err()
	}
	seen := make(map[string]struct{}, len(vars))
	for i, v := range vars {
		bad := func(key string) error {
			return ErrVariableInvalid.WithPublicContext("field", fmt.Sprintf("variables[%d].%s", i+1, key)).
				Err()
		}
		if !ValidKey(v.Name) {
			return bad("name")
		}
		if _, duplicate := seen[v.Name]; duplicate {
			return bad("name")
		}
		seen[v.Name] = struct{}{}
		if (v.Value == "") == (v.Template == "") {
			return bad("value")
		}
		if v.Template != "" {
			if _, err := flagpattern.ParseVariable(
				v.Template,
			); err != nil ||
				utf8.RuneCountInString(v.Template) > MaxTextAnswerRunes {
				return bad("template")
			}
		} else if len(v.Value) > MaxVariableValueBytes || strings.ContainsFunc(v.Value, unicode.IsControl) {
			return bad("value")
		}
	}
	return nil
}

// ValidateDeviceRuntime checks the runtime keys (command, args, system, hardware, terminal, bootstrap) of every
// device of the topology on their own.
func (t Topology) ValidateDeviceRuntime() error {
	if err := t.validateVPNOptions(); err != nil {
		return err
	}
	for _, device := range t.Devices {
		if err := validateDeviceRuntime(device); err != nil {
			return err
		}
	}
	return nil
}

// ValidateBootstrapSizes checks the sizes a bootstrap takes in the lab: a file over MaxBootstrapFileBytes, or a script
// and files over MaxBootstrapDeviceBytes in all, are refused with ErrBootstrapTooLarge naming the device, the field and,
// for a file, its path in the device (file). The size of a file of the archive is asked through size (its stored or imported
// bytes); a file whose size is unknown is an error of size itself.
func (v Variant) ValidateBootstrapSizes(size func(AttachmentRef) (int64, error)) error {
	for _, d := range v.Topology.Devices {
		total := int64(len(d.Bootstrap.Script))
		for i, f := range d.Bootstrap.Files {
			field := fmt.Sprintf("bootstrap.files[%d]", i+1)
			n := int64(len(f.Content))
			if f.Source != nil {
				var err error
				if n, err = size(*f.Source); err != nil {
					return err
				}
				field += ".source"
			}
			if n > MaxBootstrapFileBytes {
				return ErrBootstrapTooLarge.WithPublicContext("device", d.Name).WithPublicContext("field", field).
					WithPublicContext("file", f.Path).WithPublicContext("limit", MaxBootstrapFileBytes).Err()
			}
			total += n
		}
		if total > MaxBootstrapDeviceBytes {
			return ErrBootstrapTooLarge.WithPublicContext("device", d.Name).WithPublicContext("field", "bootstrap").
				WithPublicContext("limit", MaxBootstrapDeviceBytes).Err()
		}
	}
	return nil
}
