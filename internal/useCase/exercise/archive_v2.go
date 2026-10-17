package exercise

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/gofrs/uuid"
)

// archiveSchema is the only accepted value of the document's `schema` key.
const archiveSchema = "cib-exercise/1"

// The DTOs below are the portable document (snake_case keys). Domain types are reused where
// their JSON shape already is the document shape (placeholders, interfaces, env vars, ...);
// everything that differs (names instead of ids, optional ids, image forms) has its own DTO.
type (
	archiveDocV2 struct {
		Schema   string             `json:"schema"`
		Exercise archiveExerciseV2  `json:"exercise"`
		Versions []archiveVersionV2 `json:"versions"`
	}

	// archiveExerciseV2: id is accepted and ignored, a new exercise gets its own.
	archiveExerciseV2 struct {
		ID          string   `json:"id,omitempty"`
		Name        string   `json:"name"`
		Description string   `json:"description,omitempty"`
		Tags        []string `json:"tags,omitempty"`
	}

	// archiveVersionV2: id is accepted and ignored.
	archiveVersionV2 struct {
		ID        string                      `json:"id,omitempty"`
		Status    exerciseModel.VersionStatus `json:"status,omitempty"`
		AdminNote string                      `json:"admin_note,omitempty"`
		Label     string                      `json:"label,omitempty"`
		Variants  []archiveVariantV2          `json:"variants"`
	}

	archiveVariantV2 struct {
		ID       string            `json:"id,omitempty"`
		Index    int32             `json:"index,omitempty"`
		Note     string            `json:"note,omitempty"`
		Tasks    []archiveTaskV2   `json:"tasks,omitempty"`
		Topology archiveTopologyV2 `json:"topology"`
	}

	archiveTaskV2 struct {
		ID             string                      `json:"id,omitempty"`
		Name           string                      `json:"name"`
		Description    json.RawMessage             `json:"description,omitempty"`
		Difficulty     exerciseModel.Difficulty    `json:"difficulty,omitempty"`
		Flag           []string                    `json:"flag,omitempty"`
		LinkedDevice   string                      `json:"linked_device,omitempty"`
		LinkedDeviceID string                      `json:"linked_device_id,omitempty"`
		DeviceFlagVar  string                      `json:"device_flag_var,omitempty"`
		Attachments    []archiveAttachmentV2       `json:"attachments,omitempty"`
		Placeholders   []exerciseModel.Placeholder `json:"placeholders,omitempty"`
		Hints          []archiveHintV2             `json:"hints,omitempty"`
	}

	// archiveAttachmentV2: File is relative to the task folder; Name defaults to its base name.
	archiveAttachmentV2 struct {
		File string `json:"file"`
		Name string `json:"name,omitempty"`
	}

	archiveHintV2 struct {
		ID    string                  `json:"id,omitempty"`
		Text  string                  `json:"text"`
		Level exerciseModel.HintLevel `json:"level,omitempty"`
	}

	archiveTopologyV2 struct {
		VPN          exerciseModel.NetworkSpec `json:"vpn"`
		Internet     exerciseModel.NetworkSpec `json:"internet"`
		Devices      []archiveDeviceV2         `json:"devices,omitempty"`
		Connections  []archiveConnectionV2     `json:"connections,omitempty"`
		VisualRender json.RawMessage           `json:"visual_render,omitempty"`
	}

	archiveDeviceV2 struct {
		ID             string                           `json:"id,omitempty"`
		Name           string                           `json:"name"`
		Type           exerciseModel.DeviceType         `json:"type"`
		Image          archiveImageV2                   `json:"image,omitzero"`
		SecurityPreset exerciseModel.SecurityPreset     `json:"security_preset,omitempty"`
		ResourcePreset string                           `json:"resource_preset,omitempty"`
		Interfaces     []exerciseModel.Interface        `json:"interfaces,omitempty"`
		EnvVars        []exerciseModel.EnvVar           `json:"env_vars,omitempty"`
		External       *exerciseModel.ExternalAccess    `json:"external,omitempty"`
		Persistence    *exerciseModel.DevicePersistence `json:"persistence,omitempty"`
	}

	archiveConnectionV2 struct {
		Endpoints []archiveEndpointV2 `json:"endpoints"`
	}

	// archiveEndpointV2: a device is named (Device) or addressed by id (DeviceID, wins when both).
	archiveEndpointV2 struct {
		Kind      exerciseModel.EndpointKind `json:"kind"`
		Device    string                     `json:"device,omitempty"`
		DeviceID  string                     `json:"device_id,omitempty"`
		Interface string                     `json:"interface,omitempty"`
	}
)

// archiveImageV2 is a device image: a reference string ("nginx:1.27") or the reserved object
// form {"file": "<path>"} for an image shipped inside the archive.
type archiveImageV2 struct {
	Ref  string
	File string
}

const archiveImageShape = "image must be a reference string or an object with the single key file"

func (i *archiveImageV2) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var ref string
	if json.Unmarshal(trimmed, &ref) == nil {
		*i = archiveImageV2{Ref: ref}
		return nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(trimmed, &object) != nil || len(object) != 1 {
		return archiveInvalid(archiveImageShape)
	}
	rawFile, ok := object["file"]
	var file string
	if !ok || json.Unmarshal(rawFile, &file) != nil {
		return archiveInvalid(archiveImageShape)
	}
	if file == "" {
		return archiveInvalid("image.file must name a file; the object form is reserved")
	}
	*i = archiveImageV2{File: file}
	return nil
}

func (i archiveImageV2) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.Ref)
}

// archiveInvalid is the one place ErrArchiveInvalid is raised; reason is shown to the author.
// extra is key, value pairs of more public context (for example "expected").
func archiveInvalid(reason string, extra ...string) error {
	creator := exerciseModel.ErrArchiveInvalid.WithPublicContext("reason", reason)
	for i := 0; i+1 < len(extra); i += 2 {
		creator = creator.WithPublicContext(extra[i], extra[i+1])
	}
	return creator.Err()
}

// archiveBothDocuments is the one place ErrArchiveBothDocuments is raised.
func archiveBothDocuments() error {
	return exerciseModel.ErrArchiveBothDocuments.Err()
}

// archiveUnsupportedSchema: the document does not declare the one schema this import reads.
func archiveUnsupportedSchema() error {
	return exerciseModel.ErrArchiveSchemaUnsupported.WithPublicContext("expected", archiveSchema).Err()
}

// probeArchiveSchema reads only the schema key of a (comment-free) document and accepts nothing
// but the JSON string "cib-exercise/1".
func probeArchiveSchema(payload []byte) error {
	var probe struct {
		Schema json.RawMessage `json:"schema"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return decodeFailure(payload, err)
	}
	var schema string
	if json.Unmarshal(probe.Schema, &schema) != nil || schema != archiveSchema {
		return archiveUnsupportedSchema()
	}
	return nil
}

// archiveImageFileUnsupported is the one place ErrArchiveImageFileUnsupported is raised.
func archiveImageFileUnsupported(device string) error {
	return exerciseModel.ErrArchiveImageFileUnsupported.WithPublicContext("device", device).Err()
}

// decodeArchiveV2 reads one document strictly: exact (case-sensitive) known keys only, no
// trailing data, enums by name. Every failure is ErrArchiveInvalid.
func decodeArchiveV2(payload []byte) (archiveDocV2, error) {
	var generic any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&generic); err != nil {
		return archiveDocV2{}, decodeFailure(payload, err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return archiveDocV2{}, archiveInvalid("the document has data after its end")
	}
	// encoding/json matches keys case-insensitively, so unknown keys are checked on the generic
	// tree against the exact tags first; DisallowUnknownFields below stays as a backstop.
	if err := checkArchiveKeys("", generic, reflect.TypeFor[archiveDocV2]()); err != nil {
		return archiveDocV2{}, err
	}
	var doc archiveDocV2
	strict := json.NewDecoder(bytes.NewReader(payload))
	strict.DisallowUnknownFields()
	if err := strict.Decode(&doc); err != nil {
		return archiveDocV2{}, decodeFailure(payload, err)
	}
	if doc.Schema != archiveSchema {
		return archiveDocV2{}, archiveInvalid(fmt.Sprintf("schema must be %q", archiveSchema))
	}
	return doc, nil
}

// decodeFailure turns an encoding/json failure into ErrArchiveInvalid. The reason is built here
// and never carries the decoder's own text or Go type names: a type error names the 1-based
// path and the expected JSON kind; a syntax error names the line.
func decodeFailure(payload []byte, err error) error {
	if errors.Is(err, exerciseModel.ErrArchiveInvalid.Err()) {
		return err
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		where := pathAtOffset(payload, typeErr.Offset)
		if where == "" {
			where = typeErr.Field
		}
		if where == "" {
			where = "the document"
		}
		return archiveInvalid(fmt.Sprintf("%s must be %s, got %s", where, jsonKindName(typeErr.Type), typeErr.Value))
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return archiveInvalid(fmt.Sprintf("invalid JSON at line %d", lineAt(payload, syntaxErr.Offset)))
	}
	if key, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		return archiveInvalid("unknown key " + key)
	}
	return archiveInvalid("invalid JSON")
}

// lineAt is the 1-based line of the byte the decoder stopped at.
func lineAt(payload []byte, offset int64) int {
	if offset > int64(len(payload)) {
		offset = int64(len(payload))
	}
	if offset > 0 {
		offset-- // Offset counts the bytes read, including the offending one
	}
	return 1 + bytes.Count(payload[:offset], []byte("\n"))
}

// jsonKindName names, in JSON terms, what a Go type expects.
func jsonKindName(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "a boolean"
	case reflect.Slice, reflect.Array:
		return "a list"
	case reflect.Struct, reflect.Map:
		return "an object"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return "a number"
	}
	return "another kind of value"
}

// pathAtOffset finds the 1-based path ("versions[1].variants[1].tasks[2].name") of the value the
// decoder had consumed at offset; "" when it cannot be found.
func pathAtOffset(payload []byte, offset int64) string {
	type frame struct {
		array     bool
		n         int
		key       string
		expectKey bool
	}
	var stack []frame
	pathOf := func() string {
		var b strings.Builder
		for _, f := range stack {
			if f.array {
				fmt.Fprintf(&b, "[%d]", f.n)
			} else if f.key != "" {
				if b.Len() > 0 {
					b.WriteByte('.')
				}
				b.WriteString(f.key)
			}
		}
		return b.String()
	}
	valueDone := func() {
		if len(stack) > 0 && !stack[len(stack)-1].array {
			stack[len(stack)-1].expectKey = true
		}
	}
	valueStart := func() {
		if len(stack) > 0 && stack[len(stack)-1].array {
			stack[len(stack)-1].n++
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	for {
		token, err := decoder.Token()
		if err != nil {
			return ""
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{', '[':
				valueStart()
				if decoder.InputOffset() >= offset {
					return pathOf() // the container itself has the wrong type
				}
				stack = append(stack, frame{array: delim == '[', expectKey: delim == '{'})
			default:
				found := decoder.InputOffset() >= offset
				where := pathOf()
				stack = stack[:len(stack)-1]
				if found {
					return where
				}
				valueDone()
			}
			continue
		}
		if len(stack) > 0 && !stack[len(stack)-1].array && stack[len(stack)-1].expectKey {
			key, _ := token.(string)
			stack[len(stack)-1].key, stack[len(stack)-1].expectKey = key, false
			continue
		}
		valueStart()
		if decoder.InputOffset() >= offset {
			return pathOf()
		}
		valueDone()
	}
}

var jsonUnmarshalerType = reflect.TypeFor[json.Unmarshaler]()

// checkArchiveKeys walks the generic JSON tree next to the Go type it decodes into and names the
// first key (alphabetical, for stable messages) no field declares, matching case-sensitively.
// Paths are 1-based, as in every message to the author.
func checkArchiveKeys(path string, value any, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if reflect.PointerTo(t).Implements(jsonUnmarshalerType) {
		return nil // opaque or custom: json.RawMessage, archiveImageV2
	}
	switch t.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return nil // a type mismatch, reported by the decode
		}
		fields := make(map[string]reflect.Type, t.NumField())
		for i := range t.NumField() {
			field := t.Field(i)
			if name, _, _ := strings.Cut(field.Tag.Get("json"), ","); name != "" && name != "-" {
				fields[name] = field.Type
			}
		}
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fieldType, known := fields[key]
			if !known {
				where := path
				if where == "" {
					where = "the document"
				}
				return archiveInvalid(fmt.Sprintf("unknown key %q in %s", key, where))
			}
			child := key
			if path != "" {
				child = path + "." + key
			}
			if err := checkArchiveKeys(child, object[key], fieldType); err != nil {
				return err
			}
		}
	case reflect.Slice:
		items, ok := value.([]any)
		if !ok {
			return nil
		}
		for i, item := range items {
			if err := checkArchiveKeys(fmt.Sprintf("%s[%d]", path, i+1), item, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

// descriptionToRaw maps a task description to the stored editor document: a string is plain text
// with {{key}} variables, an object is an editor document taken as is, null or absent is none.
func descriptionToRaw(raw json.RawMessage, placeholders []exerciseModel.Placeholder, taskName string) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	switch trimmed[0] {
	case '"':
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, archiveInvalid("description must be text or an editor document")
		}
		return plainTextToDocument(text, placeholders, taskName)
	case '{':
		return raw, nil
	}
	return nil, archiveInvalid("description must be text or an editor document")
}

// optionalUUID parses an optional id; empty is uuid.Nil.
func optionalUUID(path, raw string) (uuid.UUID, error) {
	if raw == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.FromString(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, archiveInvalid(path + " must be a UUID")
	}
	return id, nil
}

func freshID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// toParsed maps the document to the domain: fresh UUIDv7 for id-less variants and devices,
// device names to ids, optional ids parsed. Exercise and version ids are ignored.
func (d archiveDocV2) toParsed(files map[string][]byte) (ParsedExerciseArchive, error) {
	parsed := ParsedExerciseArchive{
		Exercise: exerciseModel.Exercise{Name: d.Exercise.Name, Description: d.Exercise.Description, Tags: d.Exercise.Tags},
		Files:    files,
	}
	links := newArchiveLinks(files, &parsed)
	for vi, version := range d.Versions {
		if version.Status != "" && !version.Status.Valid() {
			return ParsedExerciseArchive{}, unknownEnum(fmt.Sprintf("versions[%d].status", vi+1), string(version.Status))
		}
		mapped := exerciseModel.ExerciseVersion{Status: version.Status, AdminNote: version.AdminNote, Label: version.Label}
		for xi, variant := range version.Variants {
			path := fmt.Sprintf("versions[%d].variants[%d]", vi+1, xi+1)
			value, err := variant.toDomain(path, links)
			if err != nil {
				return ParsedExerciseArchive{}, err
			}
			mapped.Variants = append(mapped.Variants, value)
		}
		parsed.Versions = append(parsed.Versions, mapped)
	}
	if err := links.orphan(); err != nil {
		return ParsedExerciseArchive{}, err
	}
	return parsed, nil
}

func (v archiveVariantV2) toDomain(path string, links *archiveLinks) (exerciseModel.Variant, error) {
	id, err := optionalUUID(path+".id", v.ID)
	if err != nil {
		return exerciseModel.Variant{}, err
	}
	if id == uuid.Nil {
		id = freshID()
	}
	topology, deviceIDs, err := v.Topology.toDomain(path + ".topology")
	if err != nil {
		return exerciseModel.Variant{}, err
	}
	variant := exerciseModel.Variant{ID: id, Index: v.Index, Note: v.Note, Topology: topology}
	for ti, task := range v.Tasks {
		value, taskErr := task.toDomain(fmt.Sprintf("%s.tasks[%d]", path, ti+1), deviceIDs, links)
		if taskErr != nil {
			return exerciseModel.Variant{}, taskErr
		}
		variant.Tasks = append(variant.Tasks, value)
	}
	return variant, nil
}

func (t archiveTaskV2) toDomain(path string, deviceIDs map[string]uuid.UUID, links *archiveLinks) (exerciseModel.Task, error) {
	id, err := optionalUUID(path+".id", t.ID)
	if err != nil {
		return exerciseModel.Task{}, err
	}
	if t.Difficulty != "" && !t.Difficulty.Valid() {
		return exerciseModel.Task{}, unknownEnum(path+".difficulty", string(t.Difficulty))
	}
	description, err := descriptionToRaw(t.Description, t.Placeholders, t.Name)
	if err != nil {
		return exerciseModel.Task{}, err
	}
	task := exerciseModel.Task{
		ID: id, Name: t.Name, Description: description, Difficulty: t.Difficulty, Flag: t.Flag,
		DeviceFlagVar: t.DeviceFlagVar, Placeholders: t.Placeholders,
	}
	for ai, attachment := range t.Attachments {
		ref, linkErr := links.attachment(fmt.Sprintf("%s.attachments[%d]", path, ai+1), attachment)
		if linkErr != nil {
			return exerciseModel.Task{}, linkErr
		}
		task.Attachments = append(task.Attachments, ref)
	}
	linked, err := resolveDevice(path+".linked_device", t.LinkedDevice, t.LinkedDeviceID, deviceIDs)
	if err != nil {
		return exerciseModel.Task{}, err
	}
	task.LinkedDeviceID = uuid.NullUUID{UUID: linked, Valid: linked != uuid.Nil}
	for hi, hint := range t.Hints {
		if !hintLevelValid(hint.Level) {
			return exerciseModel.Task{}, unknownEnum(fmt.Sprintf("%s.hints[%d].level", path, hi+1), string(hint.Level))
		}
		hintID, hintErr := optionalUUID(fmt.Sprintf("%s.hints[%d].id", path, hi+1), hint.ID)
		if hintErr != nil {
			return exerciseModel.Task{}, hintErr
		}
		level := hint.Level
		if level == "" {
			level = exerciseModel.HintLevelNudge // an omitted level is the lightest, like saved hints without one
		}
		task.Hints = append(task.Hints, exerciseModel.Hint{ID: hintID, Text: hint.Text, Level: level})
	}
	return task, nil
}

// unknownEnum names the field and the value an enum name could not be matched for.
func unknownEnum(path, value string) error {
	return archiveInvalid(fmt.Sprintf("%s: unknown value %q", path, value))
}

// hintLevelValid accepts an omitted level (a nudge) or one of the four named levels.
func hintLevelValid(level exerciseModel.HintLevel) bool {
	switch level {
	case "", exerciseModel.HintLevelNudge, exerciseModel.HintLevelDirection, exerciseModel.HintLevelSteps, exerciseModel.HintLevelNearSolution:
		return true
	}
	return false
}

// ambiguousDevice marks a name two devices share, so a reference by that name cannot be resolved.
var ambiguousDevice = uuid.UUID{0xff}

// resolveDevice picks the device a reference points to: the id wins when both are given, a name
// must belong to exactly one device. Neither given is uuid.Nil.
func resolveDevice(path, name, rawID string, deviceIDs map[string]uuid.UUID) (uuid.UUID, error) {
	if rawID != "" {
		return optionalUUID(path+"_id", rawID)
	}
	if name == "" {
		return uuid.Nil, nil
	}
	id, ok := deviceIDs[name]
	if !ok || id == ambiguousDevice {
		return uuid.Nil, archiveInvalid(fmt.Sprintf("%s: no single device named %q", path, name))
	}
	return id, nil
}

func (t archiveTopologyV2) toDomain(path string) (exerciseModel.Topology, map[string]uuid.UUID, error) {
	topology := exerciseModel.Topology{VPN: t.VPN, Internet: t.Internet, VisualRender: t.VisualRender}
	deviceIDs := make(map[string]uuid.UUID, len(t.Devices))
	for di, device := range t.Devices {
		devicePath := fmt.Sprintf("%s.devices[%d]", path, di+1)
		id, err := optionalUUID(devicePath+".id", device.ID)
		if err != nil {
			return exerciseModel.Topology{}, nil, err
		}
		if id == uuid.Nil {
			id = freshID()
		}
		if device.Type != "" && !device.Type.Valid() {
			return exerciseModel.Topology{}, nil, unknownEnum(devicePath+".type", string(device.Type))
		}
		if device.SecurityPreset != "" && !device.SecurityPreset.Valid() {
			return exerciseModel.Topology{}, nil, unknownEnum(devicePath+".security_preset", string(device.SecurityPreset))
		}
		for ii, iface := range device.Interfaces {
			if iface.IP.Type != "" && !iface.IP.Type.Valid() {
				return exerciseModel.Topology{}, nil, unknownEnum(fmt.Sprintf("%s.interfaces[%d].ip.type", devicePath, ii+1), string(iface.IP.Type))
			}
		}
		if device.Image.File != "" {
			return exerciseModel.Topology{}, nil, archiveImageFileUnsupported(device.Name)
		}
		if _, seen := deviceIDs[device.Name]; seen {
			deviceIDs[device.Name] = ambiguousDevice
		} else {
			deviceIDs[device.Name] = id
		}
		topology.Devices = append(topology.Devices, exerciseModel.Device{
			ID: id, Name: device.Name, Type: device.Type, Image: device.Image.Ref,
			SecurityPreset: device.SecurityPreset, ResourcePreset: device.ResourcePreset,
			Interfaces: device.Interfaces, EnvVars: device.EnvVars,
			External: device.External, Persistence: device.Persistence,
		})
	}
	for ci, connection := range t.Connections {
		mapped := exerciseModel.Connection{}
		for ei, endpoint := range connection.Endpoints {
			if endpoint.Kind != "" && !endpoint.Kind.Valid() {
				return exerciseModel.Topology{}, nil, unknownEnum(fmt.Sprintf("%s.connections[%d].endpoints[%d].kind", path, ci+1, ei+1), string(endpoint.Kind))
			}
			endpointPath := fmt.Sprintf("%s.connections[%d].endpoints[%d].device", path, ci+1, ei+1)
			id, err := resolveDevice(endpointPath, endpoint.Device, endpoint.DeviceID, deviceIDs)
			if err != nil {
				return exerciseModel.Topology{}, nil, err
			}
			mapped.Endpoints = append(mapped.Endpoints, exerciseModel.Endpoint{Kind: endpoint.Kind, DeviceID: id, Interface: endpoint.Interface})
		}
		topology.Connections = append(topology.Connections, mapped)
	}
	return topology, deviceIDs, nil
}
