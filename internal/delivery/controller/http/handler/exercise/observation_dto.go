package exercise

import exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"

// The built-in observations of check.state next to File and Custom. Exactly one observation is set on a condition. They
// run nothing of the device. Expected text may use the task and variable substitutions of the document.

type directoryCheckDTO struct {
	Path         string   `json:"Path"`
	Exists       *bool    `json:"Exists,omitempty"`
	Mode         string   `json:"Mode,omitempty"`
	Owner        string   `json:"Owner,omitempty"`
	Group        string   `json:"Group,omitempty"`
	HasEntries   []string `json:"HasEntries,omitempty"`
	LacksEntries []string `json:"LacksEntries,omitempty"`
}

// processCheckDTO: at least one of Name and Matches. Running is true unless set to false.
type processCheckDTO struct {
	Name    string `json:"Name,omitempty"`
	Matches string `json:"Matches,omitempty"`
	User    string `json:"User,omitempty"`
	Running *bool  `json:"Running,omitempty"`
}

// portCheckDTO: Protocol tcp | udp (empty = tcp); Listening is true unless set to false.
type portCheckDTO struct {
	Port      int32  `json:"Port"`
	Protocol  string `json:"Protocol,omitempty" enums:"tcp,udp"`
	Listening *bool  `json:"Listening,omitempty"`
	Address   string `json:"Address,omitempty"`
	Process   string `json:"Process,omitempty"`
}

// kernelParamCheckDTO: at least one of Equals and Matches.
type kernelParamCheckDTO struct {
	Name    string `json:"Name"`
	Equals  string `json:"Equals,omitempty"`
	Matches string `json:"Matches,omitempty"`
}

// requestCheckDTO: Protocol tcp | http | https. Method (GET | HEAD), Path, Status and Body* are for http and https only.
type requestCheckDTO struct {
	Protocol     string `json:"Protocol" enums:"tcp,http,https"`
	Host         string `json:"Host,omitempty"`
	Port         int32  `json:"Port"`
	Method       string `json:"Method,omitempty" enums:"GET,HEAD"`
	Path         string `json:"Path,omitempty"`
	Status       int32  `json:"Status,omitempty"`
	BodyContains string `json:"BodyContains,omitempty"`
	BodyMatches  string `json:"BodyMatches,omitempty"`
}

// permissionCheckDTO: at least one of Read, Write and Execute; each written value must equal the real answer.
type permissionCheckDTO struct {
	User    string `json:"User"`
	Path    string `json:"Path"`
	Read    *bool  `json:"Read,omitempty"`
	Write   *bool  `json:"Write,omitempty"`
	Execute *bool  `json:"Execute,omitempty"`
}

func (c conditionDTO) builtInToDomain(out *exerciseModel.Condition) {
	if d := c.Directory; d != nil {
		out.Directory = &exerciseModel.DirectoryObservation{
			Path: d.Path, Exists: d.Exists, Mode: d.Mode, Owner: d.Owner, Group: d.Group,
			HasEntries: d.HasEntries, LacksEntries: d.LacksEntries,
		}
	}
	if p := c.Process; p != nil {
		out.Process = &exerciseModel.ProcessObservation{Name: p.Name, Matches: p.Matches, User: p.User, Running: p.Running}
	}
	if p := c.Port; p != nil {
		out.Port = &exerciseModel.PortObservation{
			Port: p.Port, Protocol: p.Protocol, Listening: p.Listening, Address: p.Address, Process: p.Process,
		}
	}
	if k := c.KernelParam; k != nil {
		out.KernelParam = &exerciseModel.KernelParamObservation{Name: k.Name, Equals: k.Equals, Matches: k.Matches}
	}
	if r := c.Request; r != nil {
		out.Request = &exerciseModel.RequestObservation{
			Protocol: r.Protocol, Host: r.Host, Port: r.Port, Method: r.Method, Path: r.Path, Status: r.Status,
			BodyContains: r.BodyContains, BodyMatches: r.BodyMatches,
		}
	}
	if p := c.Permission; p != nil {
		out.Permission = &exerciseModel.PermissionObservation{User: p.User, Path: p.Path, Read: p.Read, Write: p.Write, Execute: p.Execute}
	}
}

func builtInToDTO(c exerciseModel.Condition, out *conditionDTO) {
	if d := c.Directory; d != nil {
		out.Directory = &directoryCheckDTO{
			Path: d.Path, Exists: d.Exists, Mode: d.Mode, Owner: d.Owner, Group: d.Group,
			HasEntries: d.HasEntries, LacksEntries: d.LacksEntries,
		}
	}
	if p := c.Process; p != nil {
		out.Process = &processCheckDTO{Name: p.Name, Matches: p.Matches, User: p.User, Running: p.Running}
	}
	if p := c.Port; p != nil {
		out.Port = &portCheckDTO{Port: p.Port, Protocol: p.Protocol, Listening: p.Listening, Address: p.Address, Process: p.Process}
	}
	if k := c.KernelParam; k != nil {
		out.KernelParam = &kernelParamCheckDTO{Name: k.Name, Equals: k.Equals, Matches: k.Matches}
	}
	if r := c.Request; r != nil {
		out.Request = &requestCheckDTO{
			Protocol: r.Protocol, Host: r.Host, Port: r.Port, Method: r.Method, Path: r.Path, Status: r.Status,
			BodyContains: r.BodyContains, BodyMatches: r.BodyMatches,
		}
	}
	if p := c.Permission; p != nil {
		out.Permission = &permissionCheckDTO{User: p.User, Path: p.Path, Read: p.Read, Write: p.Write, Execute: p.Execute}
	}
}
