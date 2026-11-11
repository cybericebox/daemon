package exerciseModel

import (
	"crypto/rand"
	"fmt"
	"io"
	"strings"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model/flagpattern"
)

// What the laboratory applies (agent API contract 1.5).
//
// The platform sends a device's bootstrap.env through the existing environment mechanism
// (labagent.buildDeviceEnv reads Device.Bootstrap.Env of the resolved topology). bootstrap.files and bootstrap.script (with
// the interpreter and the time limit) travel in the bootstrap message, command/args (else the image's) as the service
// command, the terminal block as it is, and the conditions of a state task as laboratory checks (see lab.go and
// labagent.translate). What is not applied is only what needs a guest agent: a device with the vm profile (itself refused
// where a lab is deployed) and bootstrap, terminal or a check on it.

// UnsupportedByLab lists, in document terms, what the variant asks of the laboratory that is not applied: "device <name>:
// <key>" for a bootstrap list, a terminal or a command of a vm profile device, and "task <key>: check.state" for a state
// task that observes one. Empty when the variant can run as it is.
func (v Variant) UnsupportedByLab() []string {
	var out []string
	vm := map[string]bool{}
	for _, device := range v.Topology.Devices {
		if device.EffectiveProfile() != NodeProfileVM {
			continue
		}
		vm[device.ID.String()] = true
		for _, key := range device.UnappliedKeys() {
			out = append(out, "device "+device.Name+": "+key)
		}
	}
	for _, task := range v.StateTasks() {
		for _, c := range task.Check.State.Conditions {
			if vm[c.DeviceID.String()] {
				out = append(out, "task "+task.Key+": check.state")
				break
			}
		}
	}
	return out
}

// RequireLabSupported refuses a variant whose lab would start without something the document asks for.
func (v Variant) RequireLabSupported() error {
	if unsupported := v.UnsupportedByLab(); len(unsupported) > 0 {
		return ErrRuntimeNotSupported.WithPublicContext("features", strings.Join(unsupported, "; ")).
			Err()
	}
	return nil
}

// AnswerTasks lists the tasks of the variant that are answered by a typed value, in task order.
func (v Variant) AnswerTasks() []Task {
	var out []Task
	for _, task := range v.Tasks {
		if task.Check.Answer != nil {
			out = append(out, task)
		}
	}
	return out
}

// PinVariables draws one value for every variable of the topology: a constant is itself, a template is generated.
// The caller stores what it draws with the instance (instance_variables) and passes the stored values to PinnedVariables on
// every later build, so a rebuilt or reset lab gets the same values.
func (v Variant) PinVariables(source io.Reader) (map[string]string, error) {
	if source == nil {
		source = rand.Reader
	}
	out := make(map[string]string, len(v.Topology.Variables))
	for _, variable := range v.Topology.Variables {
		if variable.Template == "" {
			out[variable.Name] = variable.Value
			continue
		}
		pattern, err := flagpattern.ParseVariable(variable.Template)
		if err != nil {
			return nil, ErrVariableInvalid.WithPublicContext("field", "variables."+variable.Name+".template").
				Err()
		}
		if out[variable.Name], err = pattern.Generate(source); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// PinnedVariables completes the stored values of an instance: a variable that has a stored value keeps it (a constant
// always takes the document's current text), a variable without one is drawn now. fresh lists the names drawn now, so the
// caller stores exactly those. A stored value of a variable the topology no longer has is dropped.
func (v Variant) PinnedVariables(stored map[string]string, source io.Reader) (values map[string]string, fresh []string, err error) {
	values = make(map[string]string, len(v.Topology.Variables))
	var missing Variant
	for _, variable := range v.Topology.Variables {
		if variable.Template == "" {
			values[variable.Name] = variable.Value
			continue
		}
		if value, ok := stored[variable.Name]; ok {
			values[variable.Name] = value
			continue
		}
		missing.Topology.Variables = append(missing.Topology.Variables, variable)
	}
	drawn, err := missing.PinVariables(source)
	if err != nil {
		return nil, nil, err
	}
	for _, variable := range missing.Topology.Variables {
		values[variable.Name] = drawn[variable.Name]
		fresh = append(fresh, variable.Name)
	}
	return values, fresh, nil
}

// WithBootstrapFiles returns a copy of the topology whose bootstrap files that name a file of the archive (Source) hold its
// bytes as Content, read through read. A file that cannot be read is an error naming the device and the path.
func (t Topology) WithBootstrapFiles(read func(AttachmentRef) ([]byte, error)) (Topology, error) {
	out := t
	out.Devices = make([]Device, len(t.Devices))
	copy(out.Devices, t.Devices)
	for i := range out.Devices {
		device := &out.Devices[i]
		if len(device.Bootstrap.Files) == 0 {
			continue
		}
		files := make([]BootstrapFile, len(device.Bootstrap.Files))
		copy(files, device.Bootstrap.Files)
		for j := range files {
			if files[j].Source == nil {
				continue
			}
			body, err := read(*files[j].Source)
			if err != nil {
				return Topology{}, ErrDeviceRuntimeInvalid.WithPublicContext("device", device.Name).
					WithPublicContext("field", fmt.Sprintf("bootstrap.files[%d].source", j+1)).
					WithError(err).
					Err()
			}
			files[j].Content, files[j].Source = string(body), nil
		}
		device.Bootstrap.Files = files
	}
	return out, nil
}

// WithBootstraps returns a copy of the topology whose devices carry the resolved bootstrap (substitutions made).
// The stored topology is not changed. A bootstrap for a device the topology does not have is an error.
func (t Topology) WithBootstraps(resolved []ResolvedBootstrap) (Topology, error) {
	out := t
	out.Devices = make([]Device, len(t.Devices))
	copy(out.Devices, t.Devices)
	index := make(map[uuid.UUID]int, len(out.Devices))
	for i, device := range out.Devices {
		index[device.ID] = i
	}
	for _, item := range resolved {
		i, found := index[item.DeviceID]
		if !found {
			return Topology{}, ErrRuntimeDeviceUnresolved.Err()
		}
		out.Devices[i].Bootstrap = item.Bootstrap
	}
	return out, nil
}

// WithoutEnvOfTasks returns a copy of the variant whose devices lack the bootstrap.env entries that place the value
// of one of the tasks (by key): the way a lab is built for an instance that has no value for a task (an event that
// does not use it), so the lab starts without that variable, as it always did. Entries that also place something
// else are dropped whole. The variant itself is not changed.
func (v Variant) WithoutEnvOfTasks(keys map[string]struct{}) Variant {
	if len(keys) == 0 {
		return v
	}
	v.Topology.Devices = append([]Device(nil), v.Topology.Devices...)
	for i := range v.Topology.Devices {
		device := &v.Topology.Devices[i]
		var kept []EnvVar
		for _, env := range device.Bootstrap.Env {
			refs, err := SubstitutionRefs(env.Value)
			dropped := false
			if err == nil {
				for _, ref := range refs {
					if _, unpinned := keys[ref.Name]; ref.Kind == RefTask && unpinned {
						dropped = true
					}
				}
			}
			if !dropped {
				kept = append(kept, env)
			}
		}
		device.Bootstrap.Env = kept
	}
	return v
}
