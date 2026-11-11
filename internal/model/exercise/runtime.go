package exerciseModel

import (
	"crypto/rand"
	"io"
	"strings"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model/flagpattern"
)

// What the laboratory applies today, and where the follow-up plugs in.
//
// The platform sends a device's bootstrap.env to the laboratory through the existing environment mechanism
// (labagent.buildDeviceEnv reads Device.Bootstrap.Env of the resolved topology). Everything else the document can
// say about a device or a task is stored and validated, but the laboratory does not apply it yet:
//
//   - bootstrap.files, bootstrap.script (+ interpreter, timeout), command, args, terminal: Device.UnappliedKeys;
//     the follow-up sends the resolved bootstrap (ResolvedInstance.Bootstraps) and the device keys next to the
//     environment in labagent.translate (buildDeviceEnv's neighbours) once the laboratory SDK carries them.
//   - check.state: Variant.StateTasks; the follow-up sends the resolved conditions (ResolvedInstance.Conditions)
//     to the laboratory's checker and marks the task by its result.
//   - the rule that forbids check.state on events, and the gating of a task by its `requires` at solve time, are
//     applied where a task is solved (event use case) in the follow-up; the data is Task.Requires and Check.
//
// Until then a lab that needs any of it is refused at start (RequireLabSupported) instead of starting half-working.

// UnsupportedByLab lists, in document terms, what the variant asks of the laboratory that is not applied yet:
// "tasks[<key>].check.state" for a state task, "device <name>: <key>" for a device key. Empty when the variant can
// run as it is.
func (v Variant) UnsupportedByLab() []string {
	var out []string
	for _, task := range v.StateTasks() {
		out = append(out, "task "+task.Key+": check.state")
	}
	for _, device := range v.Topology.Devices {
		for _, key := range device.UnappliedKeys() {
			out = append(out, "device "+device.Name+": "+key)
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
// The values are not stored anywhere yet: they are fixed for one built lab (see ResolveInstance).
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
