package exercise

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"reflect"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	appErr "github.com/cybericebox/daemon/pkg/err"
	"github.com/gofrs/uuid"
)

// archiveSchema is the only accepted value of the document's `schema` key.
const archiveSchema = "cib-exercise/1.0"

// The DTOs below are the portable document (snake_case keys). Domain types are reused where
// their JSON shape already is the document shape (placeholders, interfaces, env vars, ...);
// everything that differs (names instead of ids, optional ids, image forms) has its own DTO.
type (
	archiveDocV2 struct {
		Schema   string             `json:"schema"`
		Exercise archiveExerciseV2  `json:"exercise"`
		Versions []archiveVersionV2 `json:"versions"`
	}

	// archiveExerciseV2: id suggests a re-import target; a new exercise gets its own.
	archiveExerciseV2 struct {
		ID          string   `json:"id,omitempty"`
		Name        string   `json:"name"`
		Description string   `json:"description,omitempty"`
		Tags        []string `json:"tags,omitempty"`
	}

	// archiveVersionV2: id is accepted and ignored.
	archiveVersionV2 struct {
		ID     string                      `json:"id,omitempty"`
		Status exerciseModel.VersionStatus `json:"status,omitempty"`
		// Older archives keep metadata only at the root. New archives preserve
		// the authored metadata of each snapshot, including historical names.
		Exercise  *archiveExerciseV2 `json:"exercise,omitempty"`
		AdminNote string             `json:"admin_note,omitempty"`
		Label     string             `json:"label,omitempty"`
		Variants  []archiveVariantV2 `json:"variants"`
	}

	archiveVariantV2 struct {
		ID    string `json:"id,omitempty"`
		Index int32  `json:"index,omitempty"`
		Note  string `json:"note,omitempty"`
		// LinkedTo: the 1-based position of the base variant; the variant then holds only its own parts.
		LinkedTo *int `json:"linked_to,omitempty"`
		// Weight: how often the variant is given out relative to the others (absent = 1).
		Weight *int `json:"weight,omitempty"`
		// DeviceImages (a linked variant only): its own image for base devices, by device name.
		DeviceImages map[string]archiveImageV2 `json:"device_images,omitempty"`
		Tasks        []archiveTaskV2           `json:"tasks,omitempty"`
		Topology     archiveTopologyV2         `json:"topology"`
	}

	archiveTaskV2 struct {
		ID          string                   `json:"id,omitempty"`
		Key         string                   `json:"key,omitempty"`
		Name        string                   `json:"name"`
		Description json.RawMessage          `json:"description,omitempty"`
		Difficulty  exerciseModel.Difficulty `json:"difficulty,omitempty"`
		Requires    []string                 `json:"requires,omitempty"`
		// Check holds exactly one of answer and state (a linked variant's task may carry only check.answer.values).
		Check        *archiveCheckV2             `json:"check,omitempty"`
		Attachments  []archiveAttachmentV2       `json:"attachments,omitempty"`
		Placeholders []exerciseModel.Placeholder `json:"placeholders,omitempty"`
		Hints        []archiveHintV2             `json:"hints,omitempty"`
	}

	archiveCheckV2 struct {
		Answer *archiveAnswerV2 `json:"answer,omitempty"`
		State  *archiveStateV2  `json:"state,omitempty"`
	}

	// archiveAnswerV2: ignore_case and trim are pointers so that naming them on a flag is seen and refused.
	archiveAnswerV2 struct {
		Type       exerciseModel.AnswerType `json:"type,omitempty"`
		Values     []string                 `json:"values,omitempty"`
		InputHint  string                   `json:"input_hint,omitempty"`
		IgnoreCase *bool                    `json:"ignore_case,omitempty"`
		Trim       *bool                    `json:"trim,omitempty"`
	}

	archiveStateV2 struct {
		Mode       exerciseModel.StateMode `json:"mode,omitempty"`
		Interval   archiveInterval         `json:"interval,omitempty"`
		Manual     bool                    `json:"manual,omitempty"`
		Conditions []archiveConditionV2    `json:"conditions,omitempty"`
	}

	// archiveConditionV2 names its device by name.
	archiveConditionV2 struct {
		Device  string `json:"device"`
		Timeout string `json:"timeout,omitempty"`
		// Exactly one observation. The keys of the built-in ones are the document's and the model's own.
		File        *archiveFileCheckV2                   `json:"file,omitempty"`
		Directory   *exerciseModel.DirectoryObservation   `json:"directory,omitempty"`
		Process     *exerciseModel.ProcessObservation     `json:"process,omitempty"`
		Port        *exerciseModel.PortObservation        `json:"port,omitempty"`
		KernelParam *exerciseModel.KernelParamObservation `json:"kernel_param,omitempty"`
		Request     *exerciseModel.RequestObservation     `json:"request,omitempty"`
		Permission  *exerciseModel.PermissionObservation  `json:"permission,omitempty"`
		Custom      *archiveCustomCheckV2                 `json:"custom,omitempty"`
	}

	archiveFileCheckV2 struct {
		Path     string `json:"path"`
		Exists   *bool  `json:"exists,omitempty"`
		Type     string `json:"type,omitempty"`
		Contains string `json:"contains,omitempty"`
		Matches  string `json:"matches,omitempty"`
		Checksum string `json:"checksum,omitempty"`
		Mode     string `json:"mode,omitempty"`
		Owner    string `json:"owner,omitempty"`
		Group    string `json:"group,omitempty"`
	}

	archiveCustomCheckV2 struct {
		Run            string `json:"run"`
		Interpreter    string `json:"interpreter,omitempty"`
		User           string `json:"user,omitempty"`
		OutputContains string `json:"output_contains,omitempty"`
		OutputMatches  string `json:"output_matches,omitempty"`
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
		Variables    []archiveVariableV2   `json:"variables,omitempty"`
		Devices      []archiveDeviceV2     `json:"devices,omitempty"`
		Connections  []archiveConnectionV2 `json:"connections,omitempty"`
		VisualRender json.RawMessage       `json:"visual_render,omitempty"`
	}

	archiveDeviceV2 struct {
		ID             string                           `json:"id,omitempty"`
		Name           string                           `json:"name"`
		Type           exerciseModel.DeviceType         `json:"type"`
		Profile        exerciseModel.NodeProfile        `json:"profile,omitempty"`
		Image          archiveImageV2                   `json:"image,omitzero"`
		SecurityPreset exerciseModel.SecurityPreset     `json:"security_preset,omitempty"`
		ResourcePreset string                           `json:"resource_preset,omitempty"`
		Interfaces     []exerciseModel.Interface        `json:"interfaces,omitempty"`
		Command        []string                         `json:"command,omitempty"`
		Args           []string                         `json:"args,omitempty"`
		System         exerciseModel.SystemKind         `json:"system,omitempty"`
		Hardware       exerciseModel.HardwareKind       `json:"hardware,omitempty"`
		Bootstrap      *archiveBootstrapV2              `json:"bootstrap,omitempty"`
		Access         []exerciseModel.Access           `json:"access,omitempty"`
		Persistence    *exerciseModel.DevicePersistence `json:"persistence,omitempty"`
		WaitReadyFile  bool                             `json:"wait_ready_file,omitempty"`
		// The settings of a gateway (vpn_gateway, internet_gateway).
		DHCP               bool                      `json:"dhcp,omitempty"`
		DHCPRanges         []exerciseModel.DHCPRange `json:"dhcp_ranges,omitempty"`
		DNS                string                    `json:"dns,omitempty"`
		ReverseConnections bool                      `json:"reverse_connections,omitempty"`
	}

	// archiveEnvVarV2 is a bootstrap env var as the document writes it (the server-side origin of a secret is not).
	archiveEnvVarV2 struct {
		Name   string `json:"name"`
		Value  string `json:"value"`
		Secret bool   `json:"secret,omitempty"`
	}

	archiveBootstrapV2 struct {
		Timeout     string                   `json:"timeout,omitempty"`
		Env         []archiveEnvVarV2        `json:"env,omitempty"`
		Files       []archiveBootstrapFileV2 `json:"files,omitempty"`
		Script      string                   `json:"script,omitempty"`
		Interpreter string                   `json:"interpreter,omitempty"`
	}

	// archiveBootstrapFileV2: Source is a file of the archive (relative to the task folder), like an attachment.
	archiveBootstrapFileV2 struct {
		Path    string `json:"path"`
		Content string `json:"content,omitempty"`
		Source  string `json:"source,omitempty"`
		Owner   string `json:"owner,omitempty"`
		Group   string `json:"group,omitempty"`
		Mode    string `json:"mode,omitempty"`
	}

	archiveVariableV2 struct {
		Name     string `json:"name"`
		Value    string `json:"value,omitempty"`
		Template string `json:"template,omitempty"`
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

// archiveInterval is the schedule of a state check: a duration text ("30s"), or the number 0 for no schedule.
type archiveInterval string

func (i *archiveInterval) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if bytes.Equal(trimmed, []byte("0")) {
		*i = "0"
		return nil
	}
	var text string
	if json.Unmarshal(trimmed, &text) != nil {
		return archiveInvalid("interval must be a duration such as 30s, or 0")
	}
	*i = archiveInterval(text)
	return nil
}

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
	if i.File != "" {
		return json.Marshal(map[string]string{"file": i.File})
	}
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
// but the JSON string "cib-exercise/1.0".
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

// archiveImageFileUnsupported is the one place ErrArchiveImageFileUnsupported is raised: an image file reached a
// path that does not import image files (the synchronous import; archives naming image files run as a job).
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
	if err := checkLinkedVariantKeys(payload); err != nil {
		return archiveDocV2{}, err
	}
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
// device names to ids, optional ids parsed. Exercise and version ids are ignored. An image file is refused.
func (d archiveDocV2) toParsed(files map[string][]byte) (ParsedExerciseArchive, error) {
	return d.toParsedWithImages(files, nil)
}

// toParsedWithImages is toParsed for a folder whose device image files were read aside (images, by path relative
// to the folder): a device naming one gets a marker image (imageFileMarker) the import replaces with the pushed
// image's platform reference. nil images: an image file is refused (archiveImageFileUnsupported).
func (d archiveDocV2) toParsedWithImages(files map[string][]byte, images map[string]*archiveImageFile) (ParsedExerciseArchive, error) {
	parsed := ParsedExerciseArchive{
		SourceExerciseID: uuid.FromStringOrNil(d.Exercise.ID),
		Exercise:         exerciseModel.Exercise{Name: d.Exercise.Name, Description: d.Exercise.Description, Tags: d.Exercise.Tags},
		Files:            files,
	}
	links := newArchiveLinks(files, &parsed)
	links.images = images
	for vi, version := range d.Versions {
		statusPath := fmt.Sprintf("versions[%d].status", vi+1)
		if version.Status == "" {
			version.Status = exerciseModel.VersionStatusDraft // optional: an import makes a draft whatever it says
		}
		if !version.Status.Valid() {
			return ParsedExerciseArchive{}, unknownEnum(statusPath, string(version.Status))
		}
		metadata := d.Exercise
		if version.Exercise != nil {
			metadata = *version.Exercise
		}
		mapped := exerciseModel.ExerciseVersion{Status: version.Status, AdminNote: version.AdminNote, Label: version.Label,
			Name: metadata.Name, Description: metadata.Description, Tags: metadata.Tags}
		// Ids first: a linked variant names its base by position, possibly a later one.
		// A repeated id would make the link name two variants at once.
		ids := make([]uuid.UUID, len(version.Variants))
		seen := make(map[uuid.UUID]bool, len(version.Variants))
		for xi, variant := range version.Variants {
			idPath := fmt.Sprintf("versions[%d].variants[%d].id", vi+1, xi+1)
			id, err := optionalUUID(idPath, variant.ID)
			if err != nil {
				return ParsedExerciseArchive{}, err
			}
			if seen[id] {
				return ParsedExerciseArchive{}, archiveInvalid(idPath + " repeats the id of another variant of the version")
			}
			if id == uuid.Nil {
				id = freshID()
			}
			seen[id] = true
			ids[xi] = id
		}
		// Unlinked variants first: a linked variant names the devices of its base (possibly a later one) by name.
		values := make([]exerciseModel.Variant, len(version.Variants))
		for _, linkedPass := range []bool{false, true} {
			for xi, variant := range version.Variants {
				if (variant.LinkedTo != nil) != linkedPass {
					continue
				}
				path := fmt.Sprintf("versions[%d].variants[%d]", vi+1, xi+1)
				var value exerciseModel.Variant
				var err error
				if variant.LinkedTo != nil {
					value, err = variant.toLinkedDomain(path, xi, version.Variants, values, ids, links)
				} else {
					value, err = variant.toDomain(path, xi+1, ids[xi], links)
				}
				if err != nil {
					return ParsedExerciseArchive{}, err
				}
				values[xi] = value
			}
		}
		mapped.Variants = append(mapped.Variants, values...)
		parsed.Versions = append(parsed.Versions, mapped)
	}
	if err := links.orphan(); err != nil {
		return ParsedExerciseArchive{}, err
	}
	return parsed, nil
}

// checkArchiveWeight refuses a variant weight outside 1..MaxVariantWeight; an absent weight (nil) means 1 and is not
// written into the document. Zero is refused: to stop giving a variant out, delete it.
// weightValue is the weight a document names (0 when it names none).
func weightValue(weight *int) int {
	if weight == nil {
		return 0
	}
	return *weight
}

// weightPointer is a stored weight as the document writes it: nothing for an unset one.
func weightPointer(weight int) *int {
	if weight == 0 {
		return nil
	}
	return &weight
}

func checkArchiveWeight(path string, weight *int) error {
	if weight != nil && (*weight < 1 || *weight > exerciseModel.MaxVariantWeight) {
		return archiveInvalid(fmt.Sprintf("%s.weight must be a whole number from 1 to %d", path, exerciseModel.MaxVariantWeight))
	}
	return nil
}

// toDomain maps one variant of the archive; number is its 1-based position in the version, named on the model errors.
func (v archiveVariantV2) toDomain(path string, number int, id uuid.UUID, links *archiveLinks) (exerciseModel.Variant, error) {
	if err := checkArchiveWeight(path, v.Weight); err != nil {
		return exerciseModel.Variant{}, err
	}
	if len(v.DeviceImages) > 0 {
		return exerciseModel.Variant{}, archiveInvalid(path + ".device_images is only for a linked variant (with linked_to)")
	}
	topology, deviceIDs, err := v.Topology.toDomain(path+".topology", links)
	if err != nil {
		return exerciseModel.Variant{}, err
	}
	variant := exerciseModel.Variant{ID: id, Index: v.Index, Note: v.Note, Weight: weightValue(v.Weight), Topology: topology}
	for ti, task := range v.Tasks {
		value, taskErr := task.toDomain(fmt.Sprintf("%s.tasks[%d]", path, ti+1), deviceIDs, links)
		if taskErr != nil {
			return exerciseModel.Variant{}, taskErr
		}
		variant.Tasks = append(variant.Tasks, value)
	}
	variant.NormalizeTaskKeys()
	// The document is strict: a generator learns here, with the path of the variant, what breaks the contract.
	if err = variant.Topology.ValidateDeviceRuntime(); err != nil {
		return exerciseModel.Variant{}, withArchivePath(exerciseModel.InVariant(err, number), path)
	}
	if err = variant.ValidateDocument(); err != nil {
		return exerciseModel.Variant{}, withArchivePath(exerciseModel.InVariant(err, number), path)
	}
	if err = variant.ValidateBootstrapSizes(links.sourceSize); err != nil {
		return exerciseModel.Variant{}, withArchivePath(exerciseModel.InVariant(err, number), path)
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
	check, err := t.Check.toDomain(path+".check", deviceIDs)
	if err != nil {
		return exerciseModel.Task{}, err
	}
	task := exerciseModel.Task{
		ID:           id,
		Key:          t.Key,
		Name:         t.Name,
		Description:  description,
		Difficulty:   t.Difficulty,
		Requires:     t.Requires,
		Check:        check,
		Placeholders: t.Placeholders,
	}
	for ai, attachment := range t.Attachments {
		ref, linkErr := links.attachment(fmt.Sprintf("%s.attachments[%d]", path, ai+1), attachment)
		if linkErr != nil {
			return exerciseModel.Task{}, linkErr
		}
		task.Attachments = append(task.Attachments, ref)
	}
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

// toDomain maps the check of a task: at most one of answer and state here (naming both is refused); a task with
// none is an incomplete draft, which the publish check refuses. A linked variant's task never goes through this
// (it carries only the answer values).
func (c *archiveCheckV2) toDomain(
	path string,
	deviceIDs map[string]uuid.UUID,
) (exerciseModel.Check, error) {
	if c == nil || (c.Answer == nil && c.State == nil) {
		return exerciseModel.Check{}, nil // a draft may be incomplete; the publish check asks for exactly one
	}
	if c.Answer != nil && c.State != nil {
		return exerciseModel.Check{}, archiveInvalid(
			path + " must hold exactly one of answer and state",
		)
	}
	if c.Answer != nil {
		answer, err := c.Answer.toDomain(path + ".answer")
		return exerciseModel.Check{Answer: answer}, err
	}
	state, err := c.State.toDomain(path+".state", deviceIDs)
	return exerciseModel.Check{State: state}, err
}

func (a *archiveAnswerV2) toDomain(path string) (*exerciseModel.AnswerCheck, error) {
	if a.Type == "" {
		return nil, archiveInvalid(path + ".type: required")
	}
	if !a.Type.Valid() {
		return nil, unknownEnum(path+".type", string(a.Type))
	}
	if a.Type == exerciseModel.AnswerTypeFlag && (a.IgnoreCase != nil || a.Trim != nil) {
		key := "ignore_case"
		if a.IgnoreCase == nil {
			key = "trim"
		}
		return nil, archiveInvalid(fmt.Sprintf("%s.%s is only for the type text", path, key))
	}
	answer := &exerciseModel.AnswerCheck{
		Type:      a.Type,
		Values:    a.Values,
		InputHint: a.InputHint,
		Trim:      a.Trim,
	}
	if a.IgnoreCase != nil {
		answer.IgnoreCase = *a.IgnoreCase
	}
	return answer, nil
}

func (s *archiveStateV2) toDomain(
	path string,
	deviceIDs map[string]uuid.UUID,
) (*exerciseModel.StateCheck, error) {
	if !s.Mode.Valid() {
		return nil, unknownEnum(path+".mode", string(s.Mode))
	}
	state := &exerciseModel.StateCheck{Mode: s.Mode, Interval: string(s.Interval), Manual: s.Manual}
	for ci, c := range s.Conditions {
		conditionPath := fmt.Sprintf("%s.conditions[%d]", path, ci+1)
		device, err := resolveDevice(conditionPath+".device", c.Device, "", deviceIDs)
		if err != nil {
			return nil, err
		}
		if device == uuid.Nil {
			return nil, archiveInvalid(conditionPath + ".device: required")
		}
		condition := exerciseModel.Condition{DeviceID: device, Timeout: c.Timeout}
		if f := c.File; f != nil {
			condition.File = &exerciseModel.FileObservation{
				Path:     f.Path,
				Exists:   f.Exists,
				Type:     f.Type,
				Contains: f.Contains,
				Matches:  f.Matches,
				Checksum: f.Checksum,
				Mode:     f.Mode,
				Owner:    f.Owner,
				Group:    f.Group,
			}
		}
		condition.Directory, condition.Process, condition.Port = c.Directory, c.Process, c.Port
		condition.KernelParam, condition.Request, condition.Permission = c.KernelParam, c.Request, c.Permission
		if o := c.Custom; o != nil {
			condition.Custom = &exerciseModel.CustomObservation{
				Run:            o.Run,
				Interpreter:    o.Interpreter,
				User:           o.User,
				OutputContains: o.OutputContains,
				OutputMatches:  o.OutputMatches,
			}
		}
		if len(condition.Kinds()) != 1 {
			return nil, archiveInvalid(conditionPath + " must hold exactly one observation: file, directory, process, port, kernel_param, request, permission or custom")
		}
		state.Conditions = append(state.Conditions, condition)
	}
	return state, nil
}

// withArchivePath adds the 1-based document path to a model validation error as its public "field"... the
// model names the task or device itself; the path says which variant of which version.
func withArchivePath(failure error, path string) error {
	var creator appErr.ErrorCreator
	if !errors.As(failure, &creator) {
		return failure
	}
	return creator.WithPublicContext("path", path).Err()
}

// absentIfNull treats a JSON null (visual_render: null) as no value, so it is not stored as the text null.
func absentIfNull(raw json.RawMessage) json.RawMessage {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	return raw
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

func (t archiveTopologyV2) toDomain(
	path string,
	links *archiveLinks,
) (exerciseModel.Topology, map[string]uuid.UUID, error) {
	topology := exerciseModel.Topology{
		VisualRender: absentIfNull(t.VisualRender),
	}
	for _, variable := range t.Variables {
		topology.Variables = append(
			topology.Variables,
			exerciseModel.Variable{
				Name:     variable.Name,
				Value:    variable.Value,
				Template: variable.Template,
			},
		)
	}
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
		if device.Profile != "" && !device.Profile.Valid() {
			return exerciseModel.Topology{}, nil, unknownEnum(
				devicePath+".profile",
				string(device.Profile),
			)
		}
		if device.SecurityPreset != "" && !device.SecurityPreset.Valid() {
			return exerciseModel.Topology{}, nil, unknownEnum(devicePath+".security_preset", string(device.SecurityPreset))
		}
		for ii, iface := range device.Interfaces {
			if iface.IP.Type != "" && !iface.IP.Type.Valid() {
				return exerciseModel.Topology{}, nil, unknownEnum(fmt.Sprintf("%s.interfaces[%d].ip.type", devicePath, ii+1), string(iface.IP.Type))
			}
		}
		image := device.Image.Ref
		if strings.HasPrefix(image, imageFileMarker) {
			return exerciseModel.Topology{}, nil, archiveInvalid(devicePath + ".image is not a valid image reference")
		}
		if device.Image.File != "" {
			if links.images == nil {
				return exerciseModel.Topology{}, nil, archiveImageFileUnsupported(device.Name)
			}
			marker, linkErr := links.image(device.Name, device.Image.File)
			if linkErr != nil {
				return exerciseModel.Topology{}, nil, linkErr
			}
			image = marker
		}
		bootstrap, bootstrapErr := device.Bootstrap.toDomain(devicePath+".bootstrap", links)
		if bootstrapErr != nil {
			return exerciseModel.Topology{}, nil, bootstrapErr
		}
		if _, seen := deviceIDs[device.Name]; seen {
			deviceIDs[device.Name] = ambiguousDevice
		} else {
			deviceIDs[device.Name] = id
		}
		domainDevice := exerciseModel.Device{
			ID:             id,
			Name:           device.Name,
			Type:           device.Type,
			Profile:        device.Profile,
			Image:          image,
			SecurityPreset: device.SecurityPreset,
			ResourcePreset: device.ResourcePreset,
			Interfaces:     device.Interfaces,
			Command:        device.Command,
			Args:           device.Args,
			System:         device.System,
			Hardware:       device.Hardware,
			Bootstrap:      bootstrap,
			Access:         device.Access,
			Persistence:    device.Persistence,
			WaitReadyFile:  device.WaitReadyFile,

			DHCP: device.DHCP, DHCPRanges: device.DHCPRanges, DNS: device.DNS, ReverseConnections: device.ReverseConnections,
		}
		domainDevice.Normalize() // the former type container is a node with the container profile
		topology.Devices = append(topology.Devices, domainDevice)
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

// buildV2Document writes the portable document of one task: schema first, then exercise (name, description,
// tags) and every version. Enums are written by name; variant, task, hint and device ids are written so a
// re-import is lossless; an editor description stays an object. paths maps each attached file id to the path
// it is stored at in the archive. The output is indented plain JSON without comments.
func buildV2Document(e exerciseModel.Exercise, versions []exerciseModel.ExerciseVersion, paths map[uuid.UUID]string) ([]byte, error) {
	return buildV2DocumentWithImages(e, versions, paths, nil)
}

// buildV2DocumentWithImages is buildV2Document for an export «з образами»: a device whose platform image has a path in
// imagePaths (by the image's digest) names that file, `image: {file: …}`.
func buildV2DocumentWithImages(e exerciseModel.Exercise, versions []exerciseModel.ExerciseVersion, paths map[uuid.UUID]string, imagePaths map[string]string) ([]byte, error) {
	doc := archiveDocV2{Schema: archiveSchema, Exercise: archiveExerciseV2{ID: e.ID.String(), Name: e.Name, Description: e.Description, Tags: e.Tags}}
	for _, version := range versions {
		metadata := e.WithVersionMetadata(version)
		out := archiveVersionV2{Status: version.Status, AdminNote: version.AdminNote, Label: version.Label, Variants: []archiveVariantV2{},
			Exercise: &archiveExerciseV2{Name: metadata.Name, Description: metadata.Description, Tags: metadata.Tags}}
		for _, variant := range version.Variants {
			if linked, ok := exportLinkedVariantV2(variant, version.Variants, paths, imagePaths); ok {
				out.Variants = append(out.Variants, linked)
				continue
			}
			out.Variants = append(out.Variants, exportVariantV2(variant, paths, imagePaths))
		}
		doc.Versions = append(doc.Versions, out)
	}
	if doc.Versions == nil {
		doc.Versions = []archiveVersionV2{}
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// exportVariantV2 writes one variant in full.
func exportVariantV2(variant exerciseModel.Variant, paths map[uuid.UUID]string, imagePaths map[string]string) archiveVariantV2 {
	refs := newDeviceRefs(variant.Topology.Devices)
	topology := archiveTopologyV2{
		VisualRender: variant.Topology.VisualRender,
	}
	for _, variable := range variant.Topology.Variables {
		topology.Variables = append(
			topology.Variables,
			archiveVariableV2{
				Name:     variable.Name,
				Value:    variable.Value,
				Template: variable.Template,
			},
		)
	}
	for _, device := range variant.Topology.Devices {
		exported := archiveDeviceV2{
			ID: optionalID(
				device.ID,
			),
			Name:           device.Name,
			Type:           device.Type,
			Profile:        device.Profile,
			Image:          exportDeviceImage(device.Image, imagePaths),
			SecurityPreset: device.SecurityPreset,
			ResourcePreset: device.ResourcePreset,
			Interfaces:     device.Interfaces,
			Command:        device.Command,
			Args:           device.Args,
			System:         device.System,
			Hardware:       device.Hardware,
			Bootstrap: exportBootstrapV2(
				device.Bootstrap,
				paths,
			),
			Access:        device.Access,
			Persistence:   device.Persistence,
			WaitReadyFile: device.WaitReadyFile,

			DHCP: device.DHCP, DHCPRanges: device.DHCPRanges, DNS: device.DNS, ReverseConnections: device.ReverseConnections,
		}
		topology.Devices = append(topology.Devices, exported)
	}
	for _, connection := range variant.Topology.Connections {
		out := archiveConnectionV2{Endpoints: []archiveEndpointV2{}}
		for _, endpoint := range connection.Endpoints {
			mapped := archiveEndpointV2{Kind: endpoint.Kind, Interface: endpoint.Interface}
			mapped.Device, mapped.DeviceID = refs.ref(endpoint.DeviceID)
			out.Endpoints = append(out.Endpoints, mapped)
		}
		topology.Connections = append(topology.Connections, out)
	}
	result := archiveVariantV2{ID: optionalID(variant.ID), Index: variant.Index, Note: variant.Note, Weight: weightPointer(variant.Weight), Topology: topology}
	for _, task := range variant.Tasks {
		result.Tasks = append(result.Tasks, exportTaskV2(task, refs, paths))
	}
	return result
}

// toDomain maps a bootstrap block; its env keeps the secret flag of the document (a secret value is sealed later
// and only a password-protected archive may carry one).
func (b *archiveBootstrapV2) toDomain(
	path string,
	links *archiveLinks,
) (exerciseModel.Bootstrap, error) {
	if b == nil {
		return exerciseModel.Bootstrap{}, nil
	}
	if len(b.Env) > exerciseModel.MaxDeviceEnvVars {
		return exerciseModel.Bootstrap{}, archiveInvalid(
			fmt.Sprintf("%s.env has more than %d variables", path, exerciseModel.MaxDeviceEnvVars),
		)
	}
	if len(b.Files) > exerciseModel.MaxBootstrapFiles {
		return exerciseModel.Bootstrap{}, archiveInvalid(
			fmt.Sprintf("%s.files has more than %d files", path, exerciseModel.MaxBootstrapFiles),
		)
	}
	out := exerciseModel.Bootstrap{Timeout: b.Timeout, Script: b.Script, Interpreter: b.Interpreter}
	for _, v := range b.Env {
		out.Env = append(
			out.Env,
			exerciseModel.EnvVar{Name: v.Name, Value: v.Value, Secret: v.Secret},
		)
	}
	for fi, file := range b.Files {
		filePath := fmt.Sprintf("%s.files[%d]", path, fi+1)
		if (file.Content == "") == (file.Source == "") {
			return exerciseModel.Bootstrap{}, archiveInvalid(
				filePath + " must hold exactly one of content and source",
			)
		}
		mapped := exerciseModel.BootstrapFile{
			Path:    file.Path,
			Content: file.Content,
			Owner:   file.Owner,
			Group:   file.Group,
			Mode:    file.Mode,
		}
		if file.Source != "" {
			ref, err := links.attachment(filePath+".source", archiveAttachmentV2{File: file.Source})
			if err != nil {
				return exerciseModel.Bootstrap{}, err
			}
			mapped.Source = &ref
		}
		out.Files = append(out.Files, mapped)
	}
	return out, nil
}

func exportBootstrapV2(b exerciseModel.Bootstrap, paths map[uuid.UUID]string) *archiveBootstrapV2 {
	if b.IsZero() {
		return nil
	}
	out := &archiveBootstrapV2{Timeout: b.Timeout, Script: b.Script, Interpreter: b.Interpreter}
	for _, v := range b.Env {
		out.Env = append(out.Env, archiveEnvVarV2{Name: v.Name, Value: v.Value, Secret: v.Secret})
	}
	for _, file := range b.Files {
		mapped := archiveBootstrapFileV2{
			Path:    file.Path,
			Content: file.Content,
			Owner:   file.Owner,
			Group:   file.Group,
			Mode:    file.Mode,
		}
		if file.Source != nil {
			mapped.Source = paths[file.Source.FileID]
		}
		out.Files = append(out.Files, mapped)
	}
	return out
}

// exportDeviceImage is the image as the document writes it: the file of its platform image when the export carries
// the image files, else the reference.
func exportDeviceImage(image string, imagePaths map[string]string) archiveImageV2 {
	if ref, ok := imageRegistryModel.ParsePlatformRef(image); ok {
		if file, shipped := imagePaths[ref.Digest]; shipped {
			return archiveImageV2{File: file}
		}
	}
	return archiveImageV2{Ref: image}
}

func exportTaskV2(task exerciseModel.Task, refs deviceRefs, paths map[uuid.UUID]string) archiveTaskV2 {
	out := archiveTaskV2{
		ID: optionalID(
			task.ID,
		),
		Key:          task.Key,
		Name:         task.Name,
		Description:  task.Description,
		Difficulty:   task.Difficulty,
		Requires:     task.Requires,
		Check:        exportCheckV2(task.Check, refs),
		Placeholders: task.Placeholders,
	}
	out.Attachments = exportAttachmentRefsV2(task.Attachments, paths)
	for _, hint := range task.Hints {
		out.Hints = append(out.Hints, archiveHintV2{ID: optionalID(hint.ID), Text: hint.Text, Level: hint.Level})
	}
	return out
}

// exportCheckV2 writes the check of a task; a task with no check at all (an unfinished draft) writes none.
func exportCheckV2(check exerciseModel.Check, refs deviceRefs) *archiveCheckV2 {
	out := &archiveCheckV2{}
	if a := check.Answer; a != nil {
		answer := &archiveAnswerV2{
			Type:      a.Type,
			Values:    a.Values,
			InputHint: a.InputHint,
			Trim:      a.Trim,
		}
		if a.IgnoreCase {
			answer.IgnoreCase = &a.IgnoreCase
		}
		out.Answer = answer
	}
	if s := check.State; s != nil {
		state := &archiveStateV2{
			Mode:     s.Mode,
			Interval: archiveInterval(s.Interval),
			Manual:   s.Manual,
		}
		for _, c := range s.Conditions {
			name, _ := refs.ref(c.DeviceID)
			condition := archiveConditionV2{Device: name, Timeout: c.Timeout}
			if f := c.File; f != nil {
				condition.File = &archiveFileCheckV2{
					Path:     f.Path,
					Exists:   f.Exists,
					Type:     f.Type,
					Contains: f.Contains,
					Matches:  f.Matches,
					Checksum: f.Checksum,
					Mode:     f.Mode,
					Owner:    f.Owner,
					Group:    f.Group,
				}
			}
			condition.Directory, condition.Process, condition.Port = c.Directory, c.Process, c.Port
			condition.KernelParam, condition.Request, condition.Permission = c.KernelParam, c.Request, c.Permission
			if o := c.Custom; o != nil {
				condition.Custom = &archiveCustomCheckV2{
					Run:            o.Run,
					Interpreter:    o.Interpreter,
					User:           o.User,
					OutputContains: o.OutputContains,
					OutputMatches:  o.OutputMatches,
				}
			}
			state.Conditions = append(state.Conditions, condition)
		}
		out.State = state
	}
	if out.Answer == nil && out.State == nil {
		return nil
	}
	return out
}

// exportAttachmentRefsV2 writes attachments as links to their archive paths; the shown name only when it is
// not the file's own name.
func exportAttachmentRefsV2(attachments []exerciseModel.AttachmentRef, paths map[uuid.UUID]string) []archiveAttachmentV2 {
	var out []archiveAttachmentV2
	for _, attachment := range attachments {
		file := paths[attachment.FileID]
		mapped := archiveAttachmentV2{File: file}
		if name := exportShownName(attachment.Name); name != path.Base(file) {
			mapped.Name = name
		}
		out = append(out, mapped)
	}
	return out
}

// exportShownName keeps a shown name the import accepts as it is; "" stays "" (the file name is shown); any
// other name is made safe with exportName.
func exportShownName(name string) string {
	if name == "" || (!strings.ContainsAny(name, "/\\") && !strings.ContainsFunc(name, unicode.IsControl) &&
		utf8.RuneCountInString(name) <= maxAttachmentNameRunes) {
		return name
	}
	return exportName(name)
}

func optionalID(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

// deviceRefs names the devices of one variant for references: by name when the name is the device's alone,
// by id as well when another device shares it or the device is not in the variant.
type deviceRefs struct {
	names  map[uuid.UUID]string
	counts map[string]int
}

func newDeviceRefs(devices []exerciseModel.Device) deviceRefs {
	refs := deviceRefs{names: make(map[uuid.UUID]string, len(devices)), counts: make(map[string]int, len(devices))}
	for _, device := range devices {
		refs.names[device.ID] = device.Name
		refs.counts[device.Name]++
	}
	return refs
}

// ref returns the name and, when the name alone does not find the device, the id; nil is neither.
func (r deviceRefs) ref(id uuid.UUID) (name, rawID string) {
	if id == uuid.Nil {
		return "", ""
	}
	name, known := r.names[id]
	if known && name != "" && r.counts[name] == 1 {
		return name, ""
	}
	return name, id.String()
}
