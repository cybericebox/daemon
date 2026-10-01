package exerciseModel

import "github.com/gofrs/uuid"

// FlagLink is one task whose flag reaches a device through an env var.
type FlagLink struct {
	TaskID   uuid.UUID
	DeviceID uuid.UUID
	Var      string
	Name     string // task name, for test deploy reports
	// Candidates are the task's flag values, for callers that resolve the flag.
	Candidates []string
	Flag       string // resolved by the caller; empty links are skipped
}

// FlagLinks lists the tasks that hand their flag to a device.
func (v Variant) FlagLinks() []FlagLink {
	var links []FlagLink
	for _, task := range v.Tasks {
		if !task.LinkedDeviceID.Valid || task.DeviceFlagVar == "" {
			continue
		}
		links = append(links, FlagLink{TaskID: task.ID, DeviceID: task.LinkedDeviceID.UUID, Var: task.DeviceFlagVar, Name: task.Name, Candidates: task.Flag})
	}
	return links
}

// WithFlagEnv returns a copy of the topology with one env var per link set to
// the flag of its link. A link without a flag is skipped. The stored topology
// is never mutated. An author env var of the same name is a conflict; topology
// validation normally rejects that earlier.
func (t Topology) WithFlagEnv(links []FlagLink) (Topology, error) {
	out := t
	out.Devices = make([]Device, len(t.Devices))
	copy(out.Devices, t.Devices)
	index := make(map[uuid.UUID]int, len(out.Devices))
	for i, device := range out.Devices {
		index[device.ID] = i
	}
	for _, link := range links {
		if link.Flag == "" {
			continue
		}
		i, found := index[link.DeviceID]
		if !found {
			return Topology{}, ErrFlagDeviceUnresolved.WithContext("var", link.Var).Err()
		}
		device := &out.Devices[i]
		for _, env := range device.EnvVars {
			if env.Name == link.Var {
				return Topology{}, ErrFlagEnvironmentConflict.WithContext("device", device.Name).WithContext("env_var", link.Var).Err()
			}
		}
		envs := make([]EnvVar, len(device.EnvVars), len(device.EnvVars)+1)
		copy(envs, device.EnvVars)
		device.EnvVars = append(envs, EnvVar{Name: link.Var, Value: link.Flag})
	}
	return out, nil
}
