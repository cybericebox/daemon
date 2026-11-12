package exerciseModel

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/gofrs/uuid"
)

// Substitutions of the infrastructure, the whole list:
//
//	{{task.<key>}}  the pinned answer of a task
//	{{var.<name>}}  a variable of the infrastructure
//
// They work in bootstrap.env[].value, bootstrap.files[].content, bootstrap.script and in the expectations of a
// condition (contains, matches, output_contains, output_matches). `\{{` is a literal `{{`. The {{placeholder}}
// of a task description is another mechanism and is not touched here. Any other `{{...}}` is plain text.

// RefKind is what a substitution names.
type RefKind string

const (
	RefTask RefKind = "task"
	RefVar  RefKind = "var"
)

// Ref is one substitution found in a text.
type Ref struct {
	Kind RefKind
	Name string
}

func (r Ref) String() string { return "{{" + string(r.Kind) + "." + r.Name + "}}" }

var (
	refRe = regexp.MustCompile(`^\{\{(task|var)\.([a-z][a-z0-9_]{0,31})\}\}`)
	// A `{{task.` or `{{var.` that is not a well-formed reference is an error, not plain text.
	refPrefixRe = regexp.MustCompile(`^\{\{(task|var)\.`)
)

// substPart is a piece of a scanned text: plain text or a reference.
type substPart struct {
	text string
	ref  *Ref
}

// scanSubstitutions splits a text into plain pieces and references. A malformed reference is an error.
func scanSubstitutions(s string) ([]substPart, error) {
	var parts []substPart
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			parts = append(parts, substPart{text: plain.String()})
			plain.Reset()
		}
	}
	for i := 0; i < len(s); {
		rest := s[i:]
		switch {
		case strings.HasPrefix(rest, `\{{`):
			plain.WriteString("{{")
			i += 3
		case strings.HasPrefix(rest, "{{"):
			if m := refRe.FindStringSubmatch(rest); m != nil {
				flush()
				parts = append(parts, substPart{ref: &Ref{Kind: RefKind(m[1]), Name: m[2]}})
				i += len(m[0])
				continue
			}
			if refPrefixRe.MatchString(rest) {
				return nil, fmt.Errorf("malformed substitution near %q", truncateForMessage(rest))
			}
			plain.WriteString("{{")
			i += 2
		default:
			plain.WriteByte(s[i])
			i++
		}
	}
	flush()
	return parts, nil
}

func truncateForMessage(s string) string {
	if len(s) > 24 {
		return s[:24]
	}
	return s
}

// SubstitutionRefs lists the references of a text, in order; an error when one is malformed.
func SubstitutionRefs(s string) ([]Ref, error) {
	parts, err := scanSubstitutions(s)
	if err != nil {
		return nil, err
	}
	var refs []Ref
	for _, part := range parts {
		if part.ref != nil {
			refs = append(refs, *part.ref)
		}
	}
	return refs, nil
}

// substitutionStandIn replaces every reference by a harmless literal, so a regexp with references can be compiled
// to check its syntax. A malformed text compiles as it is (and usually fails).
func substitutionStandIn(s string) string {
	parts, err := scanSubstitutions(s)
	if err != nil {
		return s
	}
	var out strings.Builder
	for _, part := range parts {
		if part.ref != nil {
			out.WriteString("x")
			continue
		}
		out.WriteString(part.text)
	}
	return out.String()
}

// Substitute replaces the references of a text through lookup. A reference lookup cannot answer is an error.
// With quote, every substituted value is escaped for a regular expression (the field is a pattern and the value a
// literal piece of it); plain text pieces are left as the author wrote them.
func Substitute(s string, lookup func(Ref) (string, bool), quote bool) (string, error) {
	parts, err := scanSubstitutions(s)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for _, part := range parts {
		if part.ref == nil {
			out.WriteString(part.text)
			continue
		}
		value, ok := lookup(*part.ref)
		if !ok {
			return "", fmt.Errorf("unresolved substitution %s", part.ref)
		}
		if quote {
			value = regexp.QuoteMeta(value)
		}
		out.WriteString(value)
	}
	return out.String(), nil
}

// refField is one text of a variant that may hold substitutions, with the document path that names it.
type refField struct {
	path string
	text string
	// pattern: the field is a regular expression (values are quoted)
	pattern bool
	// delivery: a bootstrap field, so a task referenced here is delivered into the lab
	delivery bool
}

// substitutionFields lists, in document order, every text of the variant that may hold substitutions. A secret env
// value is sealed at rest and is not listed (it is read after it is opened).
func (v Variant) substitutionFields() []refField {
	var fields []refField
	for _, device := range v.Topology.Devices {
		prefix := "device " + device.Name + ": "
		for i, env := range device.Bootstrap.Env {
			if !env.Secret {
				fields = append(
					fields,
					refField{
						path:     fmt.Sprintf("%sbootstrap.env[%d].value", prefix, i+1),
						text:     env.Value,
						delivery: true,
					},
				)
			}
		}
		for i, file := range device.Bootstrap.Files {
			fields = append(
				fields,
				refField{
					path:     fmt.Sprintf("%sbootstrap.files[%d].content", prefix, i+1),
					text:     file.Content,
					delivery: true,
				},
			)
		}
		fields = append(
			fields,
			refField{
				path:     prefix + "bootstrap.script",
				text:     device.Bootstrap.Script,
				delivery: true,
			},
		)
	}
	for _, task := range v.Tasks {
		if task.Check.State == nil {
			continue
		}
		for i, c := range task.Check.State.Conditions {
			prefix := fmt.Sprintf("task %s: check.state.conditions[%d].", task.Key, i+1)
			_, _ = c.MapTexts(func(place, text string, pattern bool) (string, error) {
				fields = append(fields, refField{path: prefix + place, text: text, pattern: pattern})
				return text, nil
			})
		}
	}
	return fields
}

// ValidateSubstitutions checks that every substitution of the variant is well-formed and resolves: a task key must
// be the key of a task with an answer, a variable name must be a variable of the topology.
func (v Variant) ValidateSubstitutions() error {
	answers := make(map[string]struct{}, len(v.Tasks))
	for _, task := range v.Tasks {
		if task.Check.Answer != nil && task.Key != "" {
			answers[task.Key] = struct{}{}
		}
	}
	vars := make(map[string]struct{}, len(v.Topology.Variables))
	for _, variable := range v.Topology.Variables {
		vars[variable.Name] = struct{}{}
	}
	for _, field := range v.substitutionFields() {
		refs, err := SubstitutionRefs(field.text)
		if err != nil {
			return ErrSubstitutionUnresolved.WithPublicContext("field", field.path).
				WithPublicContext("reference", err.Error()).
				Err()
		}
		for _, ref := range refs {
			known := map[RefKind]map[string]struct{}{RefTask: answers, RefVar: vars}[ref.Kind]
			if _, ok := known[ref.Name]; !ok {
				return ErrSubstitutionUnresolved.WithPublicContext("field", field.path).
					WithPublicContext("reference", ref.String()).
					Err()
			}
		}
	}
	return nil
}

// DeliveredTaskKeys is the set of task keys that some device's bootstrap places into the lab.
func (v Variant) DeliveredTaskKeys() map[string]struct{} {
	delivered := map[string]struct{}{}
	for _, field := range v.substitutionFields() {
		if !field.delivery {
			continue
		}
		refs, err := SubstitutionRefs(field.text)
		if err != nil {
			continue // reported by ValidateSubstitutions
		}
		for _, ref := range refs {
			if ref.Kind == RefTask {
				delivered[ref.Name] = struct{}{}
			}
		}
	}
	// A secret env value is sealed here; the sealed text carries no readable reference.
	return delivered
}

// PinnedValues are the values of one instance: the answer of each task by key, the variables by name.
type PinnedValues struct {
	Tasks map[string]string
	Vars  map[string]string
}

func (p PinnedValues) lookup(ref Ref) (string, bool) {
	switch ref.Kind {
	case RefTask:
		value, ok := p.Tasks[ref.Name]
		return value, ok
	case RefVar:
		value, ok := p.Vars[ref.Name]
		return value, ok
	}
	return "", false
}

type (
	// ResolvedBootstrap is the bootstrap of one device with every substitution made.
	ResolvedBootstrap struct {
		DeviceID  uuid.UUID
		Bootstrap Bootstrap
	}

	// ResolvedConditions are the conditions of one state task with every substitution made.
	ResolvedConditions struct {
		TaskKey    string
		Conditions []Condition
	}

	// ResolvedInstance is what an instance runs: the bootstrap of each device (in topology order, devices without a
	// bootstrap left out) and the conditions of each state task (in task order).
	ResolvedInstance struct {
		Bootstraps []ResolvedBootstrap
		Conditions []ResolvedConditions
	}
)

// ResolveInstance substitutes the pinned values of an instance into the bootstrap of every device and the
// conditions of every state task. The variant is not changed. A reference that has no pinned value is an error.
// Secret env values must already be opened by the caller.
func (v Variant) ResolveInstance(p PinnedValues) (ResolvedInstance, error) {
	var out ResolvedInstance
	unresolved := func(path string, err error) error {
		return ErrSubstitutionUnresolved.WithPublicContext("field", path).
			WithPublicContext("reference", err.Error()).
			Err()
	}
	sub := func(path, text string, quote bool) (string, error) {
		value, err := Substitute(text, p.lookup, quote)
		if err != nil {
			return "", unresolved(path, err)
		}
		return value, nil
	}
	for _, device := range v.Topology.Devices {
		if device.Bootstrap.IsZero() {
			continue
		}
		prefix := "device " + device.Name + ": "
		resolved := device.Bootstrap
		resolved.Env = make([]EnvVar, len(device.Bootstrap.Env))
		resolved.Files = make([]BootstrapFile, len(device.Bootstrap.Files))
		var err error
		for i, env := range device.Bootstrap.Env {
			resolved.Env[i] = env
			if resolved.Env[i].Value, err = sub(
				fmt.Sprintf("%sbootstrap.env[%d].value", prefix, i+1),
				env.Value,
				false,
			); err != nil {
				return ResolvedInstance{}, err
			}
		}
		for i, file := range device.Bootstrap.Files {
			resolved.Files[i] = file
			if resolved.Files[i].Content, err = sub(
				fmt.Sprintf("%sbootstrap.files[%d].content", prefix, i+1),
				file.Content,
				false,
			); err != nil {
				return ResolvedInstance{}, err
			}
		}
		if resolved.Script, err = sub(
			prefix+"bootstrap.script",
			device.Bootstrap.Script,
			false,
		); err != nil {
			return ResolvedInstance{}, err
		}
		if len(resolved.Env) == 0 {
			resolved.Env = nil
		}
		if len(resolved.Files) == 0 {
			resolved.Files = nil
		}
		out.Bootstraps = append(
			out.Bootstraps,
			ResolvedBootstrap{DeviceID: device.ID, Bootstrap: resolved},
		)
	}
	for _, task := range v.Tasks {
		if task.Check.State == nil {
			continue
		}
		resolved := ResolvedConditions{
			TaskKey:    task.Key,
			Conditions: make([]Condition, len(task.Check.State.Conditions)),
		}
		for i, c := range task.Check.State.Conditions {
			prefix := fmt.Sprintf("task %s: check.state.conditions[%d].", task.Key, i+1)
			resolved.Conditions[i] = c
			var err error
			if resolved.Conditions[i], err = c.MapTexts(func(place, text string, pattern bool) (string, error) {
				return sub(prefix+place, text, pattern)
			}); err != nil {
				return ResolvedInstance{}, err
			}
		}
		out.Conditions = append(out.Conditions, resolved)
	}
	return out, nil
}
