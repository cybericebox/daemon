package labagent

import (
	"fmt"
	"strconv"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
)

// The mapping of one observation of a condition to the laboratory message (agent API contract 1.5). It is the only place
// that knows which observation kinds the contract has: today file and custom. When the laboratory contract gains the
// built-in kinds (directory, process, port, kernel_param, request, permission) and the file keys type and checksum, they
// are mapped here; until then a condition that uses one is an error naming it, and the target laboratory is refused
// earlier by the capability check (infrastructure.AgentFeatures.RequireLabOptions), so nothing is ever left out silently.

// buildCheckPart maps the observation of a resolved condition.
func buildCheckPart(device string, c exerciseModel.Condition) (*labpb.CheckPart, error) {
	switch {
	case c.File != nil:
		if c.File.Type != "" || c.File.Checksum != "" {
			return nil, unsupportedObservation(device, "file.type and file.checksum")
		}
		return &labpb.CheckPart{Observation: &labpb.CheckPart_File{File: buildFileObservation(*c.File)}}, nil
	case c.Custom != nil:
		return &labpb.CheckPart{Observation: &labpb.CheckPart_Custom{Custom: buildCustomObservation(*c.Custom)}}, nil
	default:
		kinds := c.Kinds()
		if len(kinds) == 1 {
			return nil, unsupportedObservation(device, string(kinds[0]))
		}
		return nil, unsupportedObservation(device, "an empty observation")
	}
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
