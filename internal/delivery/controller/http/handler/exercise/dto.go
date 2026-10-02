package exercise

import (
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// ── catalog DTOs ──

type exerciseResponse struct {
	ID                 uuid.UUID  `json:"ID"`
	Name               string     `json:"Name"`
	Description        string     `json:"Description"`
	Tags               []string   `json:"Tags"`
	DraftVersionID     *uuid.UUID `json:"DraftVersionID"`
	PublishedVersionID *uuid.UUID `json:"PublishedVersionID"`
	ArchivedAt         *time.Time `json:"ArchivedAt"`
	HasChanges         bool       `json:"HasChanges"`
	CreatedAt          time.Time  `json:"CreatedAt"`
	CreatedBy          *uuid.UUID `json:"CreatedBy"`
	AuthorName         string     `json:"AuthorName"`
	UpdatedAt          time.Time  `json:"UpdatedAt"`
	UpdatedBy          *uuid.UUID `json:"UpdatedBy"`
	UpdatedByName      string     `json:"UpdatedByName"`
	exerciseScopeResponse
}

// exerciseScopeResponse is the W4 ownership block of cards and list items.
type exerciseScopeResponse struct {
	Scope          string              `json:"Scope"`
	OwnerEventID   *uuid.UUID          `json:"OwnerEventID"`
	OwnerEventName string              `json:"OwnerEventName"`
	OwnerEvent     *eventRefResponse   `json:"OwnerEvent"`
	AccessLevel    string              `json:"AccessLevel"` // all | selected | own | none; "" for event exercises
	AccessEventIDs []uuid.UUID         `json:"AccessEventIDs"`
	AccessEvents   []eventRefResponse  `json:"AccessEvents"`
	OriginEventID  *uuid.UUID          `json:"OriginEventID"`
	ForkedFrom     *forkSourceResponse `json:"ForkedFrom"`
	Infrastructure bool                `json:"Infrastructure"`
	// Resources is the total of the published version: min and max over its variants (equal for one variant);
	// zeros without a published version. ResourceHeavy: an approved elevation holds a device of the published
	// version above the platform frame (shown as a badge).
	Resources         resourceRangeResponse       `json:"Resources"`
	ResourceHeavy     bool                        `json:"ResourceHeavy"`
	PendingProposalID *uuid.UUID                  `json:"PendingProposalID"`
	Permissions       exercisePermissionsResponse `json:"Permissions"`
}

type eventRefResponse struct {
	ID   uuid.UUID `json:"ID"`
	Name string    `json:"Name"`
}

type forkSourceResponse struct {
	ExerciseID   uuid.UUID  `json:"ExerciseID"`
	ExerciseName string     `json:"ExerciseName"`
	VersionID    *uuid.UUID `json:"VersionID"`
}

type exercisePermissionsResponse struct {
	CanRead         bool `json:"CanRead"`
	CanEdit         bool `json:"CanEdit"`
	CanPublish      bool `json:"CanPublish"`
	CanDelete       bool `json:"CanDelete"`
	CanManageAccess bool `json:"CanManageAccess"`
	CanPropose      bool `json:"CanPropose"`
	CanExport       bool `json:"CanExport"`
}

func scopeToResponse(v exerciseUseCase.ExerciseScopeView) exerciseScopeResponse {
	out := exerciseScopeResponse{
		Scope: v.Scope, OwnerEventID: v.OwnerEventID, OwnerEventName: v.OwnerEventName, AccessLevel: v.AccessLevel,
		AccessEventIDs: v.AccessEventIDs, OriginEventID: v.OriginEventID, Infrastructure: v.Infrastructure,
		Resources: rangeToResponse(v.Resources), ResourceHeavy: v.ResourceHeavy,
		PendingProposalID: v.PendingProposalID, Permissions: exercisePermissionsResponse(v.Permissions),
	}
	if out.Scope == "" {
		out.Scope = "catalog"
	}
	if out.AccessEventIDs == nil {
		out.AccessEventIDs = []uuid.UUID{}
	}
	if v.OwnerEvent != nil {
		out.OwnerEvent = &eventRefResponse{ID: v.OwnerEvent.ID, Name: v.OwnerEvent.Name}
	}
	out.AccessEvents = make([]eventRefResponse, 0, len(v.AccessEvents))
	for _, ref := range v.AccessEvents {
		out.AccessEvents = append(out.AccessEvents, eventRefResponse{ID: ref.ID, Name: ref.Name})
	}
	if v.ForkedFrom != nil {
		out.ForkedFrom = &forkSourceResponse{ExerciseID: v.ForkedFrom.ExerciseID, ExerciseName: v.ForkedFrom.ExerciseName, VersionID: v.ForkedFrom.VersionID}
	}
	return out
}

type accessSummaryResponse struct {
	IsAdmin          bool                  `json:"IsAdmin"`
	CanCreateCatalog bool                  `json:"CanCreateCatalog"`
	CanPublish       bool                  `json:"CanPublish"`
	CanDelete        bool                  `json:"CanDelete"`
	CanExport        bool                  `json:"CanExport"`
	Events           []accessEventResponse `json:"Events"`
}

type accessEventResponse struct {
	ID                    uuid.UUID `json:"ID"`
	Name                  string    `json:"Name"`
	Tag                   string    `json:"Tag"`
	CanWrite              bool      `json:"CanWrite"`
	InfrastructureAllowed bool      `json:"InfrastructureAllowed"`
}

type setAccessRequest struct {
	AccessLevel string      `json:"AccessLevel" enums:"all,selected,own,none"`
	EventIDs    []uuid.UUID `json:"EventIDs"`
}

type proposeRequest struct {
	Note string `json:"Note"`
}

type approveProposalRequest struct {
	Name        string      `json:"Name"`
	AccessLevel string      `json:"AccessLevel" enums:"all,selected,own,none"`
	EventIDs    []uuid.UUID `json:"EventIDs"`
	Note        string      `json:"Note"`
}

type rejectProposalRequest struct {
	Note string `json:"Note"`
}

type proposalResponse struct {
	ID                uuid.UUID  `json:"ID"`
	ExerciseID        uuid.UUID  `json:"ExerciseID"`
	ExerciseName      string     `json:"ExerciseName"`
	EventID           *uuid.UUID `json:"EventID"`
	EventName         string     `json:"EventName"`
	Status            string     `json:"Status"`
	Note              string     `json:"Note"`
	ProposedBy        *uuid.UUID `json:"ProposedBy"`
	ProposedByName    string     `json:"ProposedByName"`
	ProposedAt        time.Time  `json:"ProposedAt"`
	DecidedAt         *time.Time `json:"DecidedAt"`
	DecisionNote      string     `json:"DecisionNote"`
	CatalogExerciseID *uuid.UUID `json:"CatalogExerciseID"`
}

func proposalToResponse(p exerciseUseCase.ProposalView) proposalResponse {
	return proposalResponse(p)
}

type exerciseListItemResponse struct {
	ID           uuid.UUID `json:"ID"`
	Name         string    `json:"Name"`
	Description  string    `json:"Description"`
	Tags         []string  `json:"Tags"`
	HasDraft     bool      `json:"HasDraft"`
	HasPublished bool      `json:"HasPublished"`
	// Status: none | draft_only | changed | published | archived.
	Status     string     `json:"Status" enums:"none,draft_only,changed,published,archived"`
	ArchivedAt *time.Time `json:"ArchivedAt"`
	CreatedAt  time.Time  `json:"CreatedAt"`
	UpdatedAt  time.Time  `json:"UpdatedAt"`
	exerciseScopeResponse
}

func exerciseListItemToResponse(e exerciseUseCase.ExerciseListItem) exerciseListItemResponse {
	return exerciseListItemResponse{
		ID: e.ID, Name: e.Name, Description: e.Description, Tags: e.Tags,
		HasDraft: e.HasDraft, HasPublished: e.HasPublished, Status: e.Status, ArchivedAt: e.ArchivedAt,
		CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
		exerciseScopeResponse: scopeToResponse(e.ExerciseScopeView),
	}
}

type exerciseUsageResponse struct {
	Events []exerciseUsageEventResponse `json:"Events"`
}

type exerciseUsageEventResponse struct {
	ID       uuid.UUID `json:"ID"`
	Name     string    `json:"Name"`
	Archived bool      `json:"Archived"`
}

func usageToResponse(u exerciseUseCase.ExerciseUsage) exerciseUsageResponse {
	out := exerciseUsageResponse{Events: make([]exerciseUsageEventResponse, 0, len(u.Events))}
	for _, e := range u.Events {
		out.Events = append(out.Events, exerciseUsageEventResponse{ID: e.ID, Name: e.Name, Archived: e.Archived})
	}
	return out
}

type createExerciseRequest struct {
	Name        string   `json:"Name"`
	Description string   `json:"Description"`
	Tags        []string `json:"Tags"`
	// OwnerEventID: null creates a catalog exercise (admins), an event id
	// creates one owned by that event (its managers).
	OwnerEventID *uuid.UUID `json:"OwnerEventID"`
}

type updateExerciseRequest struct {
	Name        string   `json:"Name"`
	Description string   `json:"Description"`
	Tags        []string `json:"Tags"`
}

// ── version content DTOs (mirror the domain snapshot) ──

type attachmentDTO struct {
	FileID uuid.UUID `json:"FileID"`
	Name   string    `json:"Name"`
}

type placeholderDTO struct {
	Key         string `json:"Key,omitempty"`
	Kind        string `json:"Kind"`
	IPReference string `json:"IPReference,omitempty"`
	Octets1to3  string `json:"Octets1to3,omitempty"`
	LastOctet   int    `json:"LastOctet,omitempty"`
	ShowMask    bool   `json:"ShowMask,omitempty"`
	AsLink      bool   `json:"AsLink,omitempty"`
	Scheme      string `json:"Scheme,omitempty"`
	Port        int    `json:"Port,omitempty"`
	Path        string `json:"Path,omitempty"`
	DeviceName  string `json:"DeviceName,omitempty"`
}

type taskDTO struct {
	ID          *uuid.UUID      `json:"ID,omitempty"`
	Name        string          `json:"Name"`
	Description json.RawMessage `json:"Description,omitempty" swaggertype:"object"`
	Difficulty  string          `json:"Difficulty"`
	// Flag holds the candidate flag values: empty → random at deploy time; one
	// value → fixed; several → one picked at random at deploy time. Mirrors
	// exerciseModel.Task.Flag 1:1 (no FlagSource/FlagMode wrapper).
	Flag           []string         `json:"Flag,omitempty"`
	LinkedDeviceID *uuid.UUID       `json:"LinkedDeviceID,omitempty"`
	DeviceFlagVar  string           `json:"DeviceFlagVar,omitempty"`
	Attachments    []attachmentDTO  `json:"Attachments,omitempty"`
	Placeholders   []placeholderDTO `json:"Placeholders,omitempty"`
	// Hints a team may unlock: IDs and levels match across variants, the
	// text may differ per variant. The price is set per event.
	Hints []hintDTO `json:"Hints,omitempty"`
}

type hintDTO struct {
	ID   *uuid.UUID `json:"ID,omitempty"`
	Text string     `json:"Text"`
	// Level: nudge | direction | steps | near_solution.
	Level string `json:"Level"`
}

type networkDTO struct {
	Enabled    bool           `json:"Enabled"`
	DHCP       bool           `json:"DHCP"`
	DHCPRanges []dhcpRangeDTO `json:"DHCPRanges,omitempty"`
	DNS        string         `json:"DNS,omitempty"`
}

type dhcpRangeDTO struct {
	Start int32 `json:"Start"`
	End   int32 `json:"End"`
}

type ipConfigDTO struct {
	Type       string           `json:"Type"`
	Addresses  []string         `json:"Addresses,omitempty"`
	AddressRef *networkIPRefDTO `json:"AddressRef,omitempty"`
	Gateway    string           `json:"Gateway,omitempty"`
	GatewayRef *networkIPRefDTO `json:"GatewayRef,omitempty"`
	Routes     []routeDTO       `json:"Routes,omitempty"`
}

type routeDTO struct {
	Dst    string               `json:"Dst,omitempty"`
	DstRef *networkSubnetRefDTO `json:"DstRef,omitempty"`
	Via    string               `json:"Via,omitempty"`
	ViaRef *networkIPRefDTO     `json:"ViaRef,omitempty"`
}

type networkIPRefDTO struct {
	Network string `json:"Network"`
	Host    int32  `json:"Host"`
}

type networkSubnetRefDTO struct {
	Network string `json:"Network"`
}

func toDomainNetworkIPRef(ref *networkIPRefDTO) *exerciseModel.NetworkIPRef {
	if ref == nil {
		return nil
	}
	return &exerciseModel.NetworkIPRef{Network: ref.Network, Host: ref.Host}
}

func toDomainNetworkSubnetRef(ref *networkSubnetRefDTO) *exerciseModel.NetworkSubnetRef {
	if ref == nil {
		return nil
	}
	return &exerciseModel.NetworkSubnetRef{Network: ref.Network}
}

func toNetworkIPRefDTO(ref *exerciseModel.NetworkIPRef) *networkIPRefDTO {
	if ref == nil {
		return nil
	}
	return &networkIPRefDTO{Network: ref.Network, Host: ref.Host}
}

func toNetworkSubnetRefDTO(ref *exerciseModel.NetworkSubnetRef) *networkSubnetRefDTO {
	if ref == nil {
		return nil
	}
	return &networkSubnetRefDTO{Network: ref.Network}
}

type resourcesDTO struct {
	CPURequest    string `json:"CPURequest,omitempty"`
	MemoryRequest string `json:"MemoryRequest,omitempty"`
	CPULimit      string `json:"CPULimit,omitempty"`
	MemoryLimit   string `json:"MemoryLimit,omitempty"`
}

type interfaceDTO struct {
	Name string      `json:"Name"`
	MAC  string      `json:"MAC,omitempty"`
	IP   ipConfigDTO `json:"IP"`
}

type envVarDTO struct {
	Name string `json:"Name"`
	// Value is write-only for secrets: responses blank it, an empty value on
	// save means "keep the stored one".
	Value    string `json:"Value,omitempty"`
	Secret   bool   `json:"Secret"`
	HasValue bool   `json:"HasValue"` // response-only: a stored value exists
}

type externalDTO struct {
	Port     int32  `json:"Port"`
	Protocol string `json:"Protocol"`
}

// persistenceDTO keeps a container device's writable layer across an unplanned restart (a crash, an
// eviction, a node loss). Debounce is a Go duration ("5s", 1s to 24h) of quiet before a snapshot; empty
// takes the platform default. Set at creation of a lab and immutable. Allowed on container devices only.
type persistenceDTO struct {
	Enabled  bool   `json:"Enabled"`
	Debounce string `json:"Debounce,omitempty"`
}

type deviceDTO struct {
	ID             *uuid.UUID `json:"ID,omitempty"`
	Name           string     `json:"Name"`
	Type           string     `json:"Type"`
	Image          string     `json:"Image,omitempty"`
	SecurityPreset string     `json:"SecurityPreset,omitempty"`
	// ResourcePreset is a platform preset id (see the capabilities Resources); when empty the device carries its
	// own custom Resources (limits; requests always equal limits), and with neither it gets the default preset.
	ResourcePreset string         `json:"ResourcePreset,omitempty"`
	Resources      *resourcesDTO  `json:"Resources,omitempty"`
	Interfaces     []interfaceDTO `json:"Interfaces,omitempty"`
	EnvVars        []envVarDTO    `json:"EnvVars,omitempty"`
	External       *externalDTO   `json:"External,omitempty"`
	// Persistence is absent when the device keeps no state.
	Persistence *persistenceDTO `json:"Persistence,omitempty"`
}

type endpointDTO struct {
	Kind      string     `json:"Kind"`
	DeviceID  *uuid.UUID `json:"DeviceID,omitempty"`
	Interface string     `json:"Interface,omitempty"`
}

type connectionDTO struct {
	Endpoints []endpointDTO `json:"Endpoints"`
}

type topologyDTO struct {
	VPN          networkDTO      `json:"VPN"`
	Internet     networkDTO      `json:"Internet"`
	Devices      []deviceDTO     `json:"Devices,omitempty"`
	Connections  []connectionDTO `json:"Connections,omitempty"`
	VisualRender json.RawMessage `json:"VisualRender,omitempty" swaggertype:"object"`
}

type variantDTO struct {
	ID       *uuid.UUID  `json:"ID,omitempty"`
	Index    int32       `json:"Index"`
	Note     string      `json:"Note"`
	Tasks    []taskDTO   `json:"Tasks"`
	Topology topologyDTO `json:"Topology"`
}

// resourceAmountResponse: CPU in millicores, memory in bytes.
type resourceAmountResponse struct {
	CPUMillicores int64 `json:"CPUMillicores"`
	MemoryBytes   int64 `json:"MemoryBytes"`
}

// resourcePresetResponse is a device size an author picks; the ids (micro, small, medium, large) are translated
// by the frontend.
type resourcePresetResponse struct {
	ID            string `json:"ID"`
	CPUMillicores int64  `json:"CPUMillicores"`
	MemoryBytes   int64  `json:"MemoryBytes"`
}

// resourceSettingsResponse is the platform's device resources settings. A device outside the Frame needs an
// approved elevation (up to the Ceiling) to publish; a draft always saves.
type resourceSettingsResponse struct {
	Presets       []resourcePresetResponse `json:"Presets"`
	DefaultPreset string                   `json:"DefaultPreset"`
	Frame         resourceAmountResponse   `json:"Frame"`
	Ceiling       resourceAmountResponse   `json:"Ceiling"`
	// MaxDevicesPerLab, MaxInterfacesPerDevice and MaxPortsPerSwitch are constants of the laboratory.
	MaxDevicesPerLab       int `json:"MaxDevicesPerLab"`
	MaxInterfacesPerDevice int `json:"MaxInterfacesPerDevice"`
	MaxPortsPerSwitch      int `json:"MaxPortsPerSwitch"`
	// VariantSpreadWarnPercent: the editor warns when the variants of one task differ by more than this.
	VariantSpreadWarnPercent int `json:"VariantSpreadWarnPercent"`
}

func toResourceSettings(p resourcesModel.Policy) resourceSettingsResponse {
	out := resourceSettingsResponse{
		Presets: make([]resourcePresetResponse, 0, len(p.Presets)), DefaultPreset: p.DefaultPreset,
		Frame:            resourceAmountResponse{CPUMillicores: p.Frame.CPUMillicores, MemoryBytes: p.Frame.MemoryBytes},
		Ceiling:          resourceAmountResponse{CPUMillicores: p.Ceiling.CPUMillicores, MemoryBytes: p.Ceiling.MemoryBytes},
		MaxDevicesPerLab: resourcesModel.MaxDevicesPerLab, MaxInterfacesPerDevice: resourcesModel.InterfacesPerContainerDevice,
		MaxPortsPerSwitch: resourcesModel.PortsPerSwitch, VariantSpreadWarnPercent: exerciseUseCase.SpreadWarnPercent,
	}
	for _, preset := range p.Presets {
		out.Presets = append(out.Presets, resourcePresetResponse{ID: preset.ID, CPUMillicores: preset.CPUMillicores, MemoryBytes: preset.MemoryBytes})
	}
	return out
}

// resourceTotalsResponse is what a topology or task needs: its container devices and their CPU and memory.
type resourceTotalsResponse struct {
	Devices       int   `json:"Devices"`
	CPUMillicores int64 `json:"CPUMillicores"`
	MemoryBytes   int64 `json:"MemoryBytes"`
}

// resourceRangeResponse is the least and the most a task needs over its variants; event planning reserves the Max.
type resourceRangeResponse struct {
	Min resourceTotalsResponse `json:"Min"`
	Max resourceTotalsResponse `json:"Max"`
}

func totalsToResponse(t exerciseUseCase.ResourceTotals) resourceTotalsResponse {
	return resourceTotalsResponse{Devices: t.Devices, CPUMillicores: t.CPUMillicores, MemoryBytes: t.MemoryBytes}
}

func rangeToResponse(r exerciseUseCase.ResourceRange) resourceRangeResponse {
	return resourceRangeResponse{Min: totalsToResponse(r.Min), Max: totalsToResponse(r.Max)}
}

type variantResourcesResponse struct {
	VariantID uuid.UUID `json:"VariantID"`
	resourceTotalsResponse
}

// deviceOutsideResponse is a device that passes the platform frame. Covered: an approved elevation holds it.
// AboveCeiling: no approval can cover it.
type deviceOutsideResponse struct {
	VariantID     uuid.UUID `json:"VariantID"`
	DeviceID      uuid.UUID `json:"DeviceID"`
	Name          string    `json:"Name"`
	CPUMillicores int64     `json:"CPUMillicores"`
	MemoryBytes   int64     `json:"MemoryBytes"`
	Covered       bool      `json:"Covered"`
	AboveCeiling  bool      `json:"AboveCeiling"`
}

// versionResourcesResponse is the resources view of a version: the totals (Min and Max over the variants and
// per variant), how far the variants differ, the devices outside the frame (a draft always saves; publishing
// needs each one Covered) and whether the task is resource-heavy.
type versionResourcesResponse struct {
	Min      resourceTotalsResponse     `json:"Min"`
	Max      resourceTotalsResponse     `json:"Max"`
	Variants []variantResourcesResponse `json:"Variants"`
	// SpreadPercent is the largest of (max-min)/max over CPU and memory between the variants; VariantsDiffer is
	// SpreadPercent above the warning threshold (25).
	SpreadPercent  int                     `json:"SpreadPercent"`
	VariantsDiffer bool                    `json:"VariantsDiffer"`
	Outside        []deviceOutsideResponse `json:"Outside"`
	ResourceHeavy  bool                    `json:"ResourceHeavy"`
}

func versionResourcesToResponse(r exerciseUseCase.VersionResources) versionResourcesResponse {
	out := versionResourcesResponse{
		Min: totalsToResponse(r.Min), Max: totalsToResponse(r.Max), SpreadPercent: r.SpreadPercent,
		VariantsDiffer: r.SpreadPercent > exerciseUseCase.SpreadWarnPercent, ResourceHeavy: r.Heavy,
		Variants: make([]variantResourcesResponse, 0, len(r.Variants)), Outside: make([]deviceOutsideResponse, 0, len(r.Outside)),
	}
	for _, v := range r.Variants {
		out.Variants = append(out.Variants, variantResourcesResponse{VariantID: v.VariantID, resourceTotalsResponse: totalsToResponse(v.ResourceTotals)})
	}
	for _, o := range r.Outside {
		out.Outside = append(out.Outside, deviceOutsideResponse{VariantID: o.VariantID, DeviceID: o.DeviceID, Name: o.Name, CPUMillicores: o.CPUMillicores, MemoryBytes: o.MemoryBytes, Covered: o.Covered, AboveCeiling: o.AboveCeiling})
	}
	return out
}

// elevationDeviceResponse is one device of a request (what it asks for) or of an approval (what was allowed).
type elevationDeviceResponse struct {
	DeviceID      uuid.UUID `json:"DeviceID"`
	Name          string    `json:"Name"`
	CPUMillicores int64     `json:"CPUMillicores"`
	MemoryBytes   int64     `json:"MemoryBytes"`
}

// elevationResponse is a request to take devices of a task above the platform frame. Approved holds the values
// the admin allowed per device (empty until approved); a later version keeps the approval while every value
// stays at or below them, and any raise needs a new request.
type elevationResponse struct {
	ID           uuid.UUID  `json:"ID"`
	ExerciseID   uuid.UUID  `json:"ExerciseID"`
	ExerciseName string     `json:"ExerciseName"`
	VersionID    *uuid.UUID `json:"VersionID"`
	// Status is pending, approved or rejected.
	Status          string                    `json:"Status"`
	Reason          string                    `json:"Reason"`
	Requested       []elevationDeviceResponse `json:"Requested"`
	Approved        []elevationDeviceResponse `json:"Approved"`
	DecisionNote    string                    `json:"DecisionNote"`
	RequestedBy     *uuid.UUID                `json:"RequestedBy"`
	RequestedByName string                    `json:"RequestedByName"`
	RequestedAt     time.Time                 `json:"RequestedAt"`
	DecidedBy       *uuid.UUID                `json:"DecidedBy"`
	DecidedByName   string                    `json:"DecidedByName"`
	DecidedAt       *time.Time                `json:"DecidedAt"`
}

func elevationDevicesToResponse(in []exerciseUseCase.ElevationDevice) []elevationDeviceResponse {
	out := make([]elevationDeviceResponse, 0, len(in))
	for _, d := range in {
		out = append(out, elevationDeviceResponse{DeviceID: d.DeviceID, Name: d.Name, CPUMillicores: d.CPUMillicores, MemoryBytes: d.MemoryBytes})
	}
	return out
}

func elevationToResponse(e exerciseUseCase.ElevationView) elevationResponse {
	return elevationResponse{
		ID: e.ID, ExerciseID: e.ExerciseID, ExerciseName: e.ExerciseName, VersionID: e.VersionID, Status: e.Status, Reason: e.Reason,
		Requested: elevationDevicesToResponse(e.Requested), Approved: elevationDevicesToResponse(e.Approved), DecisionNote: e.DecisionNote,
		RequestedBy: e.RequestedBy, RequestedByName: e.RequestedByName, RequestedAt: e.RequestedAt,
		DecidedBy: e.DecidedBy, DecidedByName: e.DecidedByName, DecidedAt: e.DecidedAt,
	}
}

func elevationsToResponse(in []exerciseUseCase.ElevationView) []elevationResponse {
	out := make([]elevationResponse, 0, len(in))
	for _, e := range in {
		out = append(out, elevationToResponse(e))
	}
	return out
}

type requestElevationRequest struct {
	// Reason says why the devices need more than the frame; required.
	Reason string `json:"Reason"`
}

type decideElevationRequest struct {
	// Approve true approves, false rejects. Devices are the approved values per requested device (a value may
	// be lower than requested, never above the ceiling); empty approves exactly what was requested.
	Approve bool                      `json:"Approve"`
	Note    string                    `json:"Note"`
	Devices []elevationDeviceResponse `json:"Devices"`
}

type versionResponse struct {
	ID          uuid.UUID    `json:"ID"`
	ExerciseID  uuid.UUID    `json:"ExerciseID"`
	Status      string       `json:"Status"`
	AdminNote   string       `json:"AdminNote"`
	Label       string       `json:"Label"`
	Variants    []variantDTO `json:"Variants"`
	CreatedAt   time.Time    `json:"CreatedAt"`
	CreatedBy   *uuid.UUID   `json:"CreatedBy"`
	AuthorName  string       `json:"AuthorName"`
	PublishedAt *time.Time   `json:"PublishedAt"`
	// Resources: the totals of the version (min and max over its variants, per variant), the devices outside
	// the platform frame and whether the task is resource-heavy.
	Resources versionResourcesResponse `json:"Resources"`
	// Elevation is the exercise's open resource elevation request, else its latest decided one; null when it
	// never had one.
	Elevation *elevationResponse `json:"Elevation"`
}

type versionListItemResponse struct {
	ID           uuid.UUID  `json:"ID"`
	Status       string     `json:"Status"`
	AdminNote    string     `json:"AdminNote"`
	Label        string     `json:"Label"`
	VariantCount int        `json:"VariantCount"`
	CreatedAt    time.Time  `json:"CreatedAt"`
	CreatedBy    *uuid.UUID `json:"CreatedBy"`
	AuthorName   string     `json:"AuthorName"`
	PublishedAt  *time.Time `json:"PublishedAt"`
}

type saveDraftRequest struct {
	AdminNote string       `json:"AdminNote"`
	Variants  []variantDTO `json:"Variants"`
}

// checkpointRequest: the whole body is optional. Note (≤ 500 characters,
// counted in runes) becomes the snapshot's Label — never its AdminNote.
type checkpointRequest struct {
	Note string `json:"Note" binding:"max=500"`
}

// ── DTO → domain ──

func (d variantDTO) toDomain() exerciseModel.Variant {
	tasks := make([]exerciseModel.Task, 0, len(d.Tasks))
	for _, t := range d.Tasks {
		tasks = append(tasks, t.toDomain())
	}
	out := exerciseModel.Variant{Index: d.Index, Note: d.Note, Tasks: tasks, Topology: d.Topology.toDomain()}
	if d.ID != nil {
		out.ID = *d.ID
	}
	return out
}

func (d taskDTO) toDomain() exerciseModel.Task {
	out := exerciseModel.Task{
		Name:          d.Name,
		Description:   d.Description,
		Difficulty:    exerciseModel.Difficulty(d.Difficulty),
		Flag:          d.Flag,
		DeviceFlagVar: d.DeviceFlagVar,
	}
	if d.ID != nil {
		out.ID = *d.ID
	}
	if d.LinkedDeviceID != nil {
		out.LinkedDeviceID = uuid.NullUUID{UUID: *d.LinkedDeviceID, Valid: true}
	}
	for _, a := range d.Attachments {
		out.Attachments = append(out.Attachments, exerciseModel.AttachmentRef{FileID: a.FileID, Name: a.Name})
	}
	for _, h := range d.Hints {
		hint := exerciseModel.Hint{Text: h.Text, Level: exerciseModel.HintLevel(h.Level)}
		if h.ID != nil {
			hint.ID = *h.ID
		}
		out.Hints = append(out.Hints, hint)
	}
	for _, p := range d.Placeholders {
		out.Placeholders = append(out.Placeholders, exerciseModel.Placeholder{
			Key:  p.Key,
			Kind: exerciseModel.PlaceholderKind(p.Kind), IPReference: p.IPReference,
			Octets1to3: p.Octets1to3, LastOctet: p.LastOctet, ShowMask: p.ShowMask,
			AsLink: p.AsLink, Scheme: p.Scheme, Port: p.Port, Path: p.Path,
			DeviceName: p.DeviceName,
		})
	}
	return out
}

func networkToModel(d networkDTO) exerciseModel.NetworkSpec {
	out := exerciseModel.NetworkSpec{Enabled: d.Enabled, DHCP: d.DHCP, DNS: d.DNS}
	for _, r := range d.DHCPRanges {
		out.DHCPRanges = append(out.DHCPRanges, exerciseModel.DHCPRange{Start: r.Start, End: r.End})
	}
	return out
}

func networkFromModel(n exerciseModel.NetworkSpec) networkDTO {
	out := networkDTO{Enabled: n.Enabled, DHCP: n.DHCP, DNS: n.DNS}
	for _, r := range n.DHCPRanges {
		out.DHCPRanges = append(out.DHCPRanges, dhcpRangeDTO{Start: r.Start, End: r.End})
	}
	return out
}

func (d topologyDTO) toDomain() exerciseModel.Topology {
	out := exerciseModel.Topology{
		VPN:          networkToModel(d.VPN),
		Internet:     networkToModel(d.Internet),
		VisualRender: d.VisualRender,
	}
	for _, dev := range d.Devices {
		domainDev := exerciseModel.Device{Name: dev.Name, Type: exerciseModel.DeviceType(dev.Type), Image: dev.Image, SecurityPreset: exerciseModel.SecurityPreset(dev.SecurityPreset), ResourcePreset: dev.ResourcePreset}
		if dev.Resources != nil {
			domainDev.Resources = &exerciseModel.DeviceResources{
				CPURequest: dev.Resources.CPURequest, MemoryRequest: dev.Resources.MemoryRequest,
				CPULimit: dev.Resources.CPULimit, MemoryLimit: dev.Resources.MemoryLimit,
			}
		}
		if dev.ID != nil {
			domainDev.ID = *dev.ID
		}
		for _, iface := range dev.Interfaces {
			domainIface := exerciseModel.Interface{
				Name: iface.Name, MAC: iface.MAC,
				IP: exerciseModel.IPConfig{
					Type:       exerciseModel.IPConfigType(iface.IP.Type),
					Addresses:  iface.IP.Addresses,
					AddressRef: toDomainNetworkIPRef(iface.IP.AddressRef),
					Gateway:    iface.IP.Gateway,
					GatewayRef: toDomainNetworkIPRef(iface.IP.GatewayRef),
				},
			}
			for _, route := range iface.IP.Routes {
				domainIface.IP.Routes = append(domainIface.IP.Routes, exerciseModel.Route{Dst: route.Dst, DstRef: toDomainNetworkSubnetRef(route.DstRef), Via: route.Via, ViaRef: toDomainNetworkIPRef(route.ViaRef)})
			}
			domainDev.Interfaces = append(domainDev.Interfaces, domainIface)
		}
		for _, ev := range dev.EnvVars {
			domainDev.EnvVars = append(domainDev.EnvVars, exerciseModel.EnvVar{Name: ev.Name, Value: ev.Value, Secret: ev.Secret})
		}
		if dev.External != nil {
			domainDev.External = &exerciseModel.ExternalAccess{Port: dev.External.Port, Protocol: dev.External.Protocol}
		}
		if dev.Persistence != nil {
			domainDev.Persistence = &exerciseModel.DevicePersistence{Enabled: dev.Persistence.Enabled, Debounce: dev.Persistence.Debounce}
		}
		out.Devices = append(out.Devices, domainDev)
	}
	for _, c := range d.Connections {
		conn := exerciseModel.Connection{}
		for _, ep := range c.Endpoints {
			domainEP := exerciseModel.Endpoint{Kind: exerciseModel.EndpointKind(ep.Kind), Interface: ep.Interface}
			if ep.DeviceID != nil {
				domainEP.DeviceID = *ep.DeviceID
			}
			conn.Endpoints = append(conn.Endpoints, domainEP)
		}
		out.Connections = append(out.Connections, conn)
	}
	return out
}

// ── domain/view → DTO ──

func variantsToDTO(vs []exerciseModel.Variant) []variantDTO {
	out := make([]variantDTO, 0, len(vs))
	for _, v := range vs {
		out = append(out, variantToDTO(v))
	}
	return out
}

func variantToDTO(v exerciseModel.Variant) variantDTO {
	d := variantDTO{Index: v.Index, Note: v.Note, Topology: topologyToDTO(v.Topology)}
	if v.ID != uuid.Nil {
		id := v.ID
		d.ID = &id
	}
	for _, t := range v.Tasks {
		d.Tasks = append(d.Tasks, taskToDTO(t))
	}
	return d
}

func taskToDTO(t exerciseModel.Task) taskDTO {
	out := taskDTO{
		Name: t.Name, Description: t.Description, Difficulty: string(t.Difficulty),
		Flag:          t.Flag,
		DeviceFlagVar: t.DeviceFlagVar,
	}
	if t.ID != uuid.Nil {
		id := t.ID
		out.ID = &id
	}
	if t.LinkedDeviceID.Valid {
		id := t.LinkedDeviceID.UUID
		out.LinkedDeviceID = &id
	}
	for _, a := range t.Attachments {
		out.Attachments = append(out.Attachments, attachmentDTO{FileID: a.FileID, Name: a.Name})
	}
	for _, h := range t.Hints {
		id := h.ID
		out.Hints = append(out.Hints, hintDTO{ID: &id, Text: h.Text, Level: string(h.Level)})
	}
	for _, p := range t.Placeholders {
		out.Placeholders = append(out.Placeholders, placeholderDTO{
			Key:  p.Key,
			Kind: string(p.Kind), IPReference: p.IPReference, Octets1to3: p.Octets1to3,
			LastOctet: p.LastOctet, ShowMask: p.ShowMask, DeviceName: p.DeviceName,
			AsLink: p.AsLink, Scheme: p.Scheme, Port: p.Port, Path: p.Path,
		})
	}
	return out
}

func topologyToDTO(t exerciseModel.Topology) topologyDTO {
	out := topologyDTO{
		VPN:          networkFromModel(t.VPN),
		Internet:     networkFromModel(t.Internet),
		VisualRender: t.VisualRender,
	}
	for _, dev := range t.Devices {
		id := dev.ID
		dtoDev := deviceDTO{ID: &id, Name: dev.Name, Type: string(dev.Type), Image: dev.Image, SecurityPreset: string(dev.SecurityPreset), ResourcePreset: dev.ResourcePreset}
		if dev.Resources != nil {
			dtoDev.Resources = &resourcesDTO{
				CPURequest: dev.Resources.CPURequest, MemoryRequest: dev.Resources.MemoryRequest,
				CPULimit: dev.Resources.CPULimit, MemoryLimit: dev.Resources.MemoryLimit,
			}
		}
		for _, iface := range dev.Interfaces {
			dtoIface := interfaceDTO{
				Name: iface.Name, MAC: iface.MAC,
				IP: ipConfigDTO{
					Type:       string(iface.IP.Type),
					Addresses:  iface.IP.Addresses,
					AddressRef: toNetworkIPRefDTO(iface.IP.AddressRef),
					Gateway:    iface.IP.Gateway,
					GatewayRef: toNetworkIPRefDTO(iface.IP.GatewayRef),
				},
			}
			for _, route := range iface.IP.Routes {
				dtoIface.IP.Routes = append(dtoIface.IP.Routes, routeDTO{Dst: route.Dst, DstRef: toNetworkSubnetRefDTO(route.DstRef), Via: route.Via, ViaRef: toNetworkIPRefDTO(route.ViaRef)})
			}
			dtoDev.Interfaces = append(dtoDev.Interfaces, dtoIface)
		}
		for _, ev := range dev.EnvVars {
			// Secrets are already masked in the view; HasValue keeps the admin
			// UI informed that a stored value exists. Pragmatic v1 contract:
			// HasValue == ev.Secret (a saved secret is almost always non-empty;
			// a secret saved as an empty value would show HasValue=true, which
			// is tolerable for v1 — see useCase/exercise/secret.go maskSecrets).
			dtoDev.EnvVars = append(dtoDev.EnvVars, envVarDTO{Name: ev.Name, Value: ev.Value, Secret: ev.Secret, HasValue: ev.Secret})
		}
		if dev.External != nil {
			dtoDev.External = &externalDTO{Port: dev.External.Port, Protocol: dev.External.Protocol}
		}
		if dev.Persistence != nil {
			dtoDev.Persistence = &persistenceDTO{Enabled: dev.Persistence.Enabled, Debounce: dev.Persistence.Debounce}
		}
		out.Devices = append(out.Devices, dtoDev)
	}
	for _, c := range t.Connections {
		dtoConn := connectionDTO{}
		for _, ep := range c.Endpoints {
			dtoEP := endpointDTO{Kind: string(ep.Kind), Interface: ep.Interface}
			if ep.DeviceID != uuid.Nil {
				id := ep.DeviceID
				dtoEP.DeviceID = &id
			}
			dtoConn.Endpoints = append(dtoConn.Endpoints, dtoEP)
		}
		out.Connections = append(out.Connections, dtoConn)
	}
	return out
}

func elevationPtr(e *exerciseUseCase.ElevationView) *elevationResponse {
	if e == nil {
		return nil
	}
	out := elevationToResponse(*e)
	return &out
}

func versionToResponse(v exerciseUseCase.VersionView) versionResponse {
	return versionResponse{
		ID: v.ID, ExerciseID: v.ExerciseID, Status: v.Status, AdminNote: v.AdminNote, Label: v.Label,
		Variants:  variantsToDTO(v.Variants),
		CreatedAt: v.CreatedAt, CreatedBy: v.CreatedBy, AuthorName: v.AuthorName, PublishedAt: v.PublishedAt,
		Resources: versionResourcesToResponse(v.Resources), Elevation: elevationPtr(v.Elevation),
	}
}

func exerciseToResponse(e exerciseUseCase.ExerciseView) exerciseResponse {
	return exerciseResponse{
		ID: e.ID, Name: e.Name, Description: e.Description, Tags: e.Tags,
		DraftVersionID: e.DraftVersionID, PublishedVersionID: e.PublishedVersionID,
		ArchivedAt: e.ArchivedAt, HasChanges: e.HasChanges,
		CreatedAt: e.CreatedAt, CreatedBy: e.CreatedBy, AuthorName: e.AuthorName, UpdatedAt: e.UpdatedAt, UpdatedBy: e.UpdatedBy, UpdatedByName: e.UpdatedByName,
		exerciseScopeResponse: scopeToResponse(e.ExerciseScopeView),
	}
}
