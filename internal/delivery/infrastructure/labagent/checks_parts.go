package labagent

import (
	"fmt"
	"strconv"
	"strings"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
)

// The mapping of one observation of a condition to the laboratory message (agent API contract 1.5, built-in observations
// included). It is the only place that knows the observation messages. A condition with no observation is an error naming it,
// and a target laboratory that does not run a kind is refused earlier by the capability check
// (infrastructure.AgentFeatures.RequireLabOptions), so nothing is ever left out silently. Every text key reaches the lab already
// resolved (substitutions made); the lab keeps the expectations in the checker and sends only "where to look" to the device.

// buildCheckPart maps the observation of a resolved condition.
func buildCheckPart(device string, c exerciseModel.Condition) (*labpb.CheckPart, error) {
	switch {
	case c.File != nil:
		return &labpb.CheckPart{Observation: &labpb.CheckPart_File{File: buildFileObservation(*c.File)}}, nil
	case c.Directory != nil:
		return &labpb.CheckPart{Observation: &labpb.CheckPart_Directory{Directory: buildDirectoryObservation(*c.Directory)}}, nil
	case c.Process != nil:
		p := c.Process
		return &labpb.CheckPart{Observation: &labpb.CheckPart_Process{Process: &labpb.ProcessObservation{
			Name: optionalText(p.Name), Matches: optionalText(p.Matches), User: optionalText(p.User), Running: p.Running,
		}}}, nil
	case c.Port != nil:
		p := c.Port
		return &labpb.CheckPart{Observation: &labpb.CheckPart_Port{Port: &labpb.PortObservation{
			Port: p.Port, Protocol: p.Protocol, Listening: p.Listening, Address: optionalText(p.Address), Process: optionalText(p.Process),
		}}}, nil
	case c.KernelParam != nil:
		k := c.KernelParam
		return &labpb.CheckPart{Observation: &labpb.CheckPart_KernelParam{KernelParam: &labpb.KernelParamObservation{
			Name: k.Name, Equals: optionalText(k.Equals), Matches: optionalText(k.Matches),
		}}}, nil
	case c.Request != nil:
		r := c.Request
		out := &labpb.RequestObservation{
			Protocol: r.Protocol, Host: r.Host, Port: r.Port, Method: r.Method, Path: r.Path,
			BodyContains: optionalText(r.BodyContains), BodyMatches: optionalText(r.BodyMatches),
		}
		if r.Status != 0 {
			out.Status = &r.Status
		}
		return &labpb.CheckPart{Observation: &labpb.CheckPart_Request{Request: out}}, nil
	case c.Permission != nil:
		p := c.Permission
		return &labpb.CheckPart{Observation: &labpb.CheckPart_Permission{Permission: &labpb.PermissionObservation{
			User: p.User, Path: p.Path, Read: p.Read, Write: p.Write, Execute: p.Execute,
		}}}, nil
	case c.Custom != nil:
		return &labpb.CheckPart{Observation: &labpb.CheckPart_Custom{Custom: buildCustomObservation(*c.Custom)}}, nil
	}
	return nil, unsupportedObservation(device, "an empty observation")
}

func unsupportedObservation(device, what string) error {
	return exerciseModel.ErrRuntimeNotSupported.WithPublicContext("features", "device "+device+": check "+what).
		WithMessage(fmt.Sprintf("the laboratory contract has no %s observation yet", what)).Err()
}

// buildFileObservation: exists is true unless the author said false (a file expectation implies it), every other
// expectation is optional.
func buildFileObservation(f exerciseModel.FileObservation) *labpb.FileObservation {
	exists := true
	if f.Exists != nil {
		exists = *f.Exists
	}
	out := &labpb.FileObservation{
		Path:            f.Path,
		Exists:          &exists,
		ContentContains: optionalText(f.Contains),
		ContentMatches:  optionalText(f.Matches),
		Owner:           optionalText(f.Owner),
		Group:           optionalText(f.Group),
		Type:            optionalText(f.Type),
		Checksum:        optionalText(strings.ToLower(f.Checksum)),
	}
	if mode, err := strconv.ParseUint(f.Mode, 8, 32); err == nil && f.Mode != "" {
		bits := uint32(mode)
		out.Mode = &bits
	}
	return out
}

// buildCustomObservation: the author's run is a script (a command line is a one-line script), run with the interpreter.
func buildCustomObservation(o exerciseModel.CustomObservation) *labpb.CustomObservation {
	return &labpb.CustomObservation{
		Script:         o.Run,
		Interpreter:    o.Interpreter,
		User:           o.User,
		OutputContains: optionalText(o.OutputContains),
		OutputMatches:  optionalText(o.OutputMatches),
	}
}

// buildDirectoryObservation: exists is true unless the author said false; the mode is permission bits.
func buildDirectoryObservation(d exerciseModel.DirectoryObservation) *labpb.DirectoryObservation {
	exists := true
	if d.Exists != nil {
		exists = *d.Exists
	}
	out := &labpb.DirectoryObservation{
		Path: d.Path, Exists: &exists, Owner: optionalText(d.Owner), Group: optionalText(d.Group),
		HasEntries: d.HasEntries, LacksEntries: d.LacksEntries,
	}
	if mode, err := strconv.ParseUint(d.Mode, 8, 32); err == nil && d.Mode != "" {
		bits := uint32(mode)
		out.Mode = &bits
	}
	return out
}
