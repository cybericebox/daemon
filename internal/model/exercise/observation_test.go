package exerciseModel_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func ptr[T any](v T) *T { return &v }

// Every built-in observation is validated by its own rules; the error names the key (conditions[1].<kind>.<key>).
func TestValidateDocument_Observations(t *testing.T) {
	sum := strings.Repeat("a", 64)
	for name, tc := range map[string]struct {
		condition exerciseModel.Condition
		bad       string // the key that is wrong, "" for valid
	}{
		"file type and checksum":          {exerciseModel.Condition{File: &exerciseModel.FileObservation{Path: "/x", Type: "file", Checksum: sum}}, ""},
		"file symlink":                    {exerciseModel.Condition{File: &exerciseModel.FileObservation{Path: "/x", Type: "symlink"}}, ""},
		"file unknown type":               {exerciseModel.Condition{File: &exerciseModel.FileObservation{Path: "/x", Type: "socket"}}, "file.type"},
		"file bad checksum":               {exerciseModel.Condition{File: &exerciseModel.FileObservation{Path: "/x", Checksum: "abc"}}, "file.checksum"},
		"file checksum of a substitution": {exerciseModel.Condition{File: &exerciseModel.FileObservation{Path: "/x", Checksum: "{{var.pw}}"}}, ""},
		"checksum of a directory":         {exerciseModel.Condition{File: &exerciseModel.FileObservation{Path: "/x", Type: "directory", Checksum: sum}}, "file.type"},
		"file absent with a type":         {exerciseModel.Condition{File: &exerciseModel.FileObservation{Path: "/x", Exists: ptr(false), Type: "file"}}, "file.exists"},
		"path from a variable":            {exerciseModel.Condition{File: &exerciseModel.FileObservation{Path: "/srv/{{var.pw}}/x"}}, ""},

		"directory":                     {exerciseModel.Condition{Directory: &exerciseModel.DirectoryObservation{Path: "/srv/app", Mode: "0750", Owner: "www-data", Group: "www-data", HasEntries: []string{"config.php", "uploads"}, LacksEntries: []string{"install.php"}}}, ""},
		"directory absent":              {exerciseModel.Condition{Directory: &exerciseModel.DirectoryObservation{Path: "/srv/app", Exists: ptr(false)}}, ""},
		"directory absent with entries": {exerciseModel.Condition{Directory: &exerciseModel.DirectoryObservation{Path: "/srv/app", Exists: ptr(false), HasEntries: []string{"a"}}}, "directory.exists"},
		"directory relative path":       {exerciseModel.Condition{Directory: &exerciseModel.DirectoryObservation{Path: "srv"}}, "directory.path"},
		"directory entry with a slash":  {exerciseModel.Condition{Directory: &exerciseModel.DirectoryObservation{Path: "/srv", HasEntries: []string{"a/b"}}}, "directory.has_entries"},
		"directory too many entries":    {exerciseModel.Condition{Directory: &exerciseModel.DirectoryObservation{Path: "/srv", LacksEntries: make([]string, exerciseModel.MaxDirectoryEntries+1)}}, "directory.lacks_entries"},

		"process by name":     {exerciseModel.Condition{Process: &exerciseModel.ProcessObservation{Name: "nginx", User: "www-data"}}, ""},
		"process by pattern":  {exerciseModel.Condition{Process: &exerciseModel.ProcessObservation{Matches: `nginx: master`, Running: ptr(false)}}, ""},
		"process without key": {exerciseModel.Condition{Process: &exerciseModel.ProcessObservation{User: "root"}}, "process.name"},
		"process name path":   {exerciseModel.Condition{Process: &exerciseModel.ProcessObservation{Name: "/usr/sbin/nginx"}}, "process.name"},
		"process bad pattern": {exerciseModel.Condition{Process: &exerciseModel.ProcessObservation{Matches: `(`}}, "process.matches"},

		"port":                {exerciseModel.Condition{Port: &exerciseModel.PortObservation{Port: 8080, Protocol: "udp", Address: "127.0.0.1", Process: "nginx"}}, ""},
		"port not listening":  {exerciseModel.Condition{Port: &exerciseModel.PortObservation{Port: 22, Listening: ptr(false)}}, ""},
		"port zero":           {exerciseModel.Condition{Port: &exerciseModel.PortObservation{}}, "port.port"},
		"port protocol":       {exerciseModel.Condition{Port: &exerciseModel.PortObservation{Port: 1, Protocol: "sctp"}}, "port.protocol"},
		"port bad address":    {exerciseModel.Condition{Port: &exerciseModel.PortObservation{Port: 1, Address: "localhost"}}, "port.address"},
		"port closed + owner": {exerciseModel.Condition{Port: &exerciseModel.PortObservation{Port: 1, Listening: ptr(false), Process: "x"}}, "port.listening"},

		"kernel_param equals":  {exerciseModel.Condition{KernelParam: &exerciseModel.KernelParamObservation{Name: "net.ipv4.ip_forward", Equals: "1"}}, ""},
		"kernel_param matches": {exerciseModel.Condition{KernelParam: &exerciseModel.KernelParamObservation{Name: "kernel.randomize_va_space", Matches: `^[12]$`}}, ""},
		"kernel_param no rule": {exerciseModel.Condition{KernelParam: &exerciseModel.KernelParamObservation{Name: "net.ipv4.ip_forward"}}, "kernel_param.equals"},
		"kernel_param path":    {exerciseModel.Condition{KernelParam: &exerciseModel.KernelParamObservation{Name: "net/ipv4/ip_forward", Equals: "1"}}, "kernel_param.name"},

		"request tcp":          {exerciseModel.Condition{Request: &exerciseModel.RequestObservation{Protocol: "tcp", Port: 5432}}, ""},
		"request http":         {exerciseModel.Condition{Request: &exerciseModel.RequestObservation{Protocol: "http", Host: "127.0.0.1", Port: 80, Method: "HEAD", Path: "/health", Status: 204, BodyContains: "ok", BodyMatches: `"state":\s*"up"`}}, ""},
		"request host name":    {exerciseModel.Condition{Request: &exerciseModel.RequestObservation{Protocol: "https", Host: "app.local", Port: 443}}, ""},
		"request unknown":      {exerciseModel.Condition{Request: &exerciseModel.RequestObservation{Protocol: "ftp", Port: 21}}, "request.protocol"},
		"request no port":      {exerciseModel.Condition{Request: &exerciseModel.RequestObservation{Protocol: "tcp"}}, "request.port"},
		"tcp with a path":      {exerciseModel.Condition{Request: &exerciseModel.RequestObservation{Protocol: "tcp", Port: 1, Path: "/"}}, "request.path"},
		"tcp with a body rule": {exerciseModel.Condition{Request: &exerciseModel.RequestObservation{Protocol: "tcp", Port: 1, BodyContains: "x"}}, "request.body_contains"},
		"request method":       {exerciseModel.Condition{Request: &exerciseModel.RequestObservation{Protocol: "http", Port: 80, Method: "DELETE"}}, "request.method"},
		"request status":       {exerciseModel.Condition{Request: &exerciseModel.RequestObservation{Protocol: "http", Port: 80, Status: 700}}, "request.status"},
		"request relative":     {exerciseModel.Condition{Request: &exerciseModel.RequestObservation{Protocol: "http", Port: 80, Path: "health"}}, "request.path"},
		"request bad host":     {exerciseModel.Condition{Request: &exerciseModel.RequestObservation{Protocol: "http", Port: 80, Host: "bad host"}}, "request.host"},
		"request bad pattern":  {exerciseModel.Condition{Request: &exerciseModel.RequestObservation{Protocol: "http", Port: 80, BodyMatches: `[`}}, "request.body_matches"},

		"permission":         {exerciseModel.Condition{Permission: &exerciseModel.PermissionObservation{User: "www-data", Path: "/etc/shadow", Read: ptr(false), Write: ptr(false)}}, ""},
		"permission no key":  {exerciseModel.Condition{Permission: &exerciseModel.PermissionObservation{User: "www-data", Path: "/etc/shadow"}}, "permission.read"},
		"permission no user": {exerciseModel.Condition{Permission: &exerciseModel.PermissionObservation{Path: "/x", Read: ptr(true)}}, "permission.user"},
		"permission path":    {exerciseModel.Condition{Permission: &exerciseModel.PermissionObservation{User: "u", Path: "x", Read: ptr(true)}}, "permission.path"},

		"two observations": {exerciseModel.Condition{Directory: &exerciseModel.DirectoryObservation{Path: "/x"}, Port: &exerciseModel.PortObservation{Port: 1}}, "observation"},
		"custom user kept": {exerciseModel.Condition{Custom: &exerciseModel.CustomObservation{Run: "true"}}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			err := stateVariant(tc.condition, exerciseModel.StateCheck{}).ValidateDocument()
			if tc.bad == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, exerciseModel.ErrStateInvalid.Err())
			var withContext interface{ PublicContext() map[string]any }
			require.ErrorAs(t, err, &withContext)
			require.Equal(t, "check.state.conditions[1]."+tc.bad, withContext.PublicContext()["field"])
		})
	}
}

// Every text key of every observation takes the substitutions, and the references are checked like any other.
func TestObservationsTakeSubstitutions(t *testing.T) {
	condition := exerciseModel.Condition{Request: &exerciseModel.RequestObservation{
		Protocol: "http", Host: "{{var.pw}}", Port: 80, Path: "/{{var.pw}}", BodyContains: "{{var.pw}}", BodyMatches: `k={{var.pw}}`,
	}}
	v := stateVariant(condition, exerciseModel.StateCheck{})
	require.NoError(t, v.ValidateDocument())
	require.NoError(t, v.ValidateSubstitutions())

	broken := stateVariant(exerciseModel.Condition{Directory: &exerciseModel.DirectoryObservation{Path: "/srv/{{var.ghost}}"}}, exerciseModel.StateCheck{})
	require.ErrorIs(t, broken.ValidateSubstitutions(), exerciseModel.ErrSubstitutionUnresolved.Err())

	// MapTexts reaches every text key once; a regular expression is told so.
	seen := map[string]bool{}
	mapped, err := exerciseModel.Condition{KernelParam: &exerciseModel.KernelParamObservation{Name: "a.b", Equals: "1", Matches: "x"}}.
		MapTexts(func(place, text string, pattern bool) (string, error) {
			seen[fmt.Sprintf("%s:%t", place, pattern)] = true
			return text + "!", nil
		})
	require.NoError(t, err)
	require.Equal(t, map[string]bool{"kernel_param.name:false": true, "kernel_param.equals:false": true, "kernel_param.matches:true": true}, seen)
	require.Equal(t, "1!", mapped.KernelParam.Equals)
}

func TestKindsAndCapabilities(t *testing.T) {
	c := exerciseModel.Condition{Port: &exerciseModel.PortObservation{Port: 1}}
	require.Equal(t, []exerciseModel.ObservationKind{exerciseModel.ObservationPort}, c.Kinds())
	require.Len(t, exerciseModel.ObservationKinds, 8)
	var all exerciseModel.Condition
	all.Directory, all.Port = &exerciseModel.DirectoryObservation{}, &exerciseModel.PortObservation{}
	require.Equal(t, []exerciseModel.ObservationKind{exerciseModel.ObservationDirectory, exerciseModel.ObservationPort}, all.Kinds())

	// NeedsOf lists the kinds the checks use, and the file keys that only a newer laboratory knows.
	checks := []exerciseModel.LabCheck{{ID: "s:1", Parts: []exerciseModel.LabCheckPart{
		{Condition: exerciseModel.Condition{File: &exerciseModel.FileObservation{Path: "/x", Checksum: strings.Repeat("a", 64)}}},
		{Condition: exerciseModel.Condition{Process: &exerciseModel.ProcessObservation{Name: "nginx"}}},
		{Condition: exerciseModel.Condition{Custom: &exerciseModel.CustomObservation{Run: "true"}}},
	}}}
	require.Equal(t, []string{"custom", "file", "file.checksum", "process"}, exerciseModel.NeedsOf(exerciseModel.Topology{}, checks).Observations)
}
