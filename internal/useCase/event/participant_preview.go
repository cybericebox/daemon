package event

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantPreviewRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/platformSettingsRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/flagpattern"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	preview "github.com/cybericebox/daemon/internal/model/participantPreview"
	settingsModel "github.com/cybericebox/daemon/internal/model/platformSettings"
	"github.com/cybericebox/daemon/internal/model/rbac"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
	"github.com/cybericebox/daemon/pkg/labaccess"
	"github.com/gofrs/uuid"
)

// PreviewTestLabs grants only the existing owned test-lab operations. The public
// author endpoints retain their independent author-write authorization.
type PreviewTestLabs interface {
	EventPreviewAllocated(context.Context, uuid.UUID) (bool, error)
	DeployEventPreviewVariantTest(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (exerciseModel.DeployHandle, error)
	CanStartEventPreview(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (bool, error)
	DeployTestStatus(context.Context, uuid.UUID, uuid.UUID) (exerciseModel.LabDeployStatus, error)
	CheckTestFlag(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string) (bool, error)
	OpenTestDeployLink(context.Context, uuid.UUID, uuid.UUID, string, int32) (labaccess.Link, error)
	DestroyDeployTest(context.Context, uuid.UUID, uuid.UUID) error
}
type PreviewSelection struct {
	ID                uuid.UUID `json:"ID"`
	ExerciseVersionID uuid.UUID `json:"ExerciseVersionID"`
	VariantID         uuid.UUID `json:"VariantID"`
	Name              string    `json:"Name"`
	VariantIndex      int32     `json:"VariantIndex"`
	VariantPosition   int       `json:"VariantPosition"`
}
type PreviewSettings struct {
	DefaultSelections   []PreviewSelection `json:"DefaultSelections"`
	DefaultGroupName    string             `json:"DefaultGroupName"`
	DynamicAllocated    bool               `json:"DynamicAllocated"`
	GroupName           string             `json:"GroupName"`
	GroupNameOverride   *string            `json:"GroupNameOverride"`
	Inherit             bool               `json:"Inherit"`
	Selections          []PreviewSelection `json:"Selections"`
	EffectiveSelections []PreviewSelection `json:"EffectiveSelections"`
}
type PreviewSettingsInput struct {
	GroupName  *string            `json:"GroupName"`
	Inherit    bool               `json:"Inherit"`
	Selections []PreviewSelection `json:"Selections"`
}
type PreviewCatalogVariant struct {
	Infrastructure bool      `json:"Infrastructure"`
	Position       int       `json:"Position"`
	ID             uuid.UUID `json:"ID"`
	Index          int32     `json:"Index"`
}
type PreviewCatalogItem struct {
	ID                uuid.UUID               `json:"ID"`
	ExerciseVersionID uuid.UUID               `json:"ExerciseVersionID"`
	Name              string                  `json:"Name"`
	Variants          []PreviewCatalogVariant `json:"Variants"`
	Infrastructure    bool                    `json:"Infrastructure"`
	Eligible          bool                    `json:"Eligible"`
	Reason            string                  `json:"Reason"`
}
type PreviewChallenge struct {
	OwnChallengeView
	PreviewSelectionID     uuid.UUID  `json:"PreviewSelectionID"`
	PreviewRunID           *uuid.UUID `json:"PreviewRunID"`
	PreviewDynamic         bool       `json:"PreviewDynamic"`
	PreviewExpiresAt       *time.Time `json:"PreviewExpiresAt"`
	PreviewAttempts        int        `json:"PreviewAttempts"`
	PreviewCorrectAttempts int        `json:"PreviewCorrectAttempts"`
	PreviewCanStart        bool       `json:"PreviewCanStart"`
}
type PreviewBoard struct {
	Challenges   []PreviewChallenge    `json:"Challenges"`
	Availability ChallengeAvailability `json:"Availability"`
	Stages       []BoardStageView      `json:"Stages"`
	CurrentStage *CurrentStageView     `json:"CurrentStage"`
	ServerNow    time.Time             `json:"ServerNow"`
}
type PreviewRunView struct {
	ID          uuid.UUID           `json:"ID"`
	SelectionID uuid.UUID           `json:"SelectionID"`
	ExpiresAt   time.Time           `json:"ExpiresAt"`
	Dynamic     bool                `json:"Dynamic"`
	Lab         *ParticipantLabView `json:"Lab"`
}

func previewUnavailable() error { return preview.ErrUnavailable.Err() }
func previewInvalid() error     { return preview.ErrInvalid.Err() }
func previewFailure(err error) error {
	return model.ErrPlatform.WithError(err).WithMessage("Failed to persist participant preview").Err()
}

// previewVersion applies publication and global/event catalog boundaries on every operation.
func (u *EventUseCase) previewVersion(ctx context.Context, event, versionID, variantID uuid.UUID) (exerciseModel.ExerciseVersion, exerciseModel.Exercise, exerciseModel.Variant, error) {
	version, err := u.exercises.GetVersion(ctx, versionID)
	if err != nil {
		return version, exerciseModel.Exercise{}, exerciseModel.Variant{}, err
	}
	entry, err := u.exercises.GetByID(ctx, version.ExerciseID)
	if err != nil {
		return version, entry, exerciseModel.Variant{}, err
	}
	if version.Status != exerciseModel.VersionStatusPublished || version.PublishedAt == nil || entry.ArchivedAt != nil {
		return version, entry, exerciseModel.Variant{}, previewUnavailable()
	}
	if event == uuid.Nil {
		if entry.Scope != exerciseModel.ScopeCatalog || entry.AccessLevel != exerciseModel.AccessAllEvents {
			return version, entry, exerciseModel.Variant{}, previewUnavailable()
		}
	} else {
		available, e := u.exercises.AvailableToEvent(ctx, entry.ID, event)
		if e != nil {
			return version, entry, exerciseModel.Variant{}, e
		}
		if !available {
			return version, entry, exerciseModel.Variant{}, previewUnavailable()
		}
	}
	for _, variant := range version.Variants {
		if variant.ID == variantID && variantID != uuid.Nil {
			// The selected variant determines infrastructure; a dynamic sibling never blocks a static task.
			if event != uuid.Nil && len(variant.Topology.Devices) > 0 {
				ev, e := u.events.GetByID(ctx, event)
				if e != nil {
					return version, entry, variant, e
				}
				if !ev.InfrastructureAllowed {
					return version, entry, variant, previewUnavailable()
				}
			}
			return version, entry, variant, nil
		}
	}
	return version, entry, exerciseModel.Variant{}, previewInvalid()
}

func selectionIdentity(version, variant uuid.UUID) uuid.UUID {
	return uuid.NewV5(uuid.NamespaceURL, "cybericebox:participant-preview:"+version.String()+":"+variant.String())
}
func (u *EventUseCase) validatePreviewSelections(ctx context.Context, event uuid.UUID, in []PreviewSelection) ([]preview.Selection, []PreviewSelection, error) {
	stored := make([]preview.Selection, 0, len(in))
	views := make([]PreviewSelection, 0, len(in))
	seen := map[uuid.UUID]bool{}
	for _, item := range in {
		version, entry, variant, err := u.previewVersion(ctx, event, item.ExerciseVersionID, item.VariantID)
		if err != nil {
			return nil, nil, err
		}
		if event != uuid.Nil && len(variant.Topology.Devices) > 0 {
			if u.previewLabs == nil {
				return nil, nil, previewUnavailable()
			}
			allocated, e := u.previewLabs.EventPreviewAllocated(ctx, event)
			if e != nil {
				return nil, nil, e
			}
			if !allocated {
				return nil, nil, previewUnavailable()
			}
		}
		id := selectionIdentity(item.ExerciseVersionID, item.VariantID)
		if seen[id] {
			return nil, nil, previewInvalid()
		}
		seen[id] = true
		stored = append(stored, preview.Selection{ID: id, ExerciseVersionID: item.ExerciseVersionID, VariantID: item.VariantID})
		views = append(views, PreviewSelection{ID: id, ExerciseVersionID: item.ExerciseVersionID, VariantID: item.VariantID, Name: entry.Name, VariantIndex: variant.Index, VariantPosition: previewVariantPosition(version, variant.ID)})
	}
	return stored, views, nil
}
func (u *EventUseCase) previewDefaults(ctx context.Context) (preview.Defaults, error) {
	setting, err := platformSettingsRepo.New(u.repo).GetByKey(ctx, preview.SettingKey)
	if repositoryTools.IsObjectNotFoundError(err) {
		return preview.Defaults{Selections: []preview.Selection{}}, nil
	}
	if err != nil {
		return preview.Defaults{}, previewFailure(err)
	}
	var out preview.Defaults
	// Existing installations stored a bare array before the logical group was named.
	if len(setting.Value) > 0 && strings.HasPrefix(strings.TrimSpace(string(setting.Value)), "[") {
		err = json.Unmarshal(setting.Value, &out.Selections)
	} else {
		err = json.Unmarshal(setting.Value, &out)
	}
	if err != nil {
		return out, previewFailure(err)
	}
	safe := make([]preview.Selection, 0, len(out.Selections))
	for _, item := range out.Selections {
		if _, _, _, e := u.previewVersion(ctx, uuid.Nil, item.ExerciseVersionID, item.VariantID); e == nil {
			safe = append(safe, item)
		}
	}
	out.Selections = safe
	return out, nil
}
func (u *EventUseCase) previewDefaultSelections(ctx context.Context) ([]preview.Selection, error) {
	config, err := u.previewDefaults(ctx)
	return config.Selections, err
}
func (u *EventUseCase) previewSelections(ctx context.Context, event uuid.UUID) ([]preview.Selection, bool, error) {
	config, err := participantPreviewRepo.New(u.repo).Configuration(ctx, event)
	if repositoryTools.IsObjectNotFoundError(err) || err == nil && config.Inherit {
		items, e := u.previewDefaultSelections(ctx)
		return items, true, e
	}
	return config.Selections, false, err
}
func normalizePreviewGroupName(in *string) (*string, error) {
	if in == nil {
		return nil, nil
	}
	name := strings.TrimSpace(*in)
	if name == "" || utf8.RuneCountInString(name) > 100 || strings.IndexFunc(*in, unicode.IsControl) >= 0 {
		return nil, preview.ErrGroupNameInvalid.Err()
	}
	return &name, nil
}

func (u *EventUseCase) previewSelectionViews(ctx context.Context, event uuid.UUID, in []preview.Selection) []PreviewSelection {
	out := make([]PreviewSelection, 0, len(in))
	for _, item := range in {
		version, entry, variant, err := u.previewVersion(ctx, event, item.ExerciseVersionID, item.VariantID)
		if err != nil {
			continue
		}
		if event != uuid.Nil && len(variant.Topology.Devices) > 0 {
			if u.previewLabs == nil {
				continue
			}
			allocated, e := u.previewLabs.EventPreviewAllocated(ctx, event)
			if e != nil || !allocated {
				continue
			}
		}
		out = append(out, PreviewSelection{ID: selectionIdentity(item.ExerciseVersionID, item.VariantID), ExerciseVersionID: item.ExerciseVersionID, VariantID: item.VariantID, Name: entry.Name, VariantIndex: variant.Index, VariantPosition: previewVariantPosition(version, variant.ID)})
	}
	return out
}
func (u *EventUseCase) GetPreviewSettings(ctx context.Context, event, user uuid.UUID) (PreviewSettings, error) {
	if err := u.RequireManageEvent(ctx, event, user); err != nil {
		return PreviewSettings{}, err
	}
	config, err := participantPreviewRepo.New(u.repo).Configuration(ctx, event)
	if repositoryTools.IsObjectNotFoundError(err) {
		config = preview.Settings{Inherit: true}
	} else if err != nil {
		return PreviewSettings{}, previewFailure(err)
	}
	defaults, err := u.previewDefaults(ctx)
	if err != nil {
		return PreviewSettings{}, err
	}
	items := config.Selections
	if config.Inherit {
		items = defaults.Selections
	}
	effective := u.previewSelectionViews(ctx, event, items)
	selected := effective
	if config.Inherit {
		selected = []PreviewSelection{}
	}
	name := defaults.GroupName
	if config.GroupName != nil {
		name = *config.GroupName
	}
	allocated := false
	if u.previewLabs != nil {
		allocated, err = u.previewLabs.EventPreviewAllocated(ctx, event)
		if err != nil {
			return PreviewSettings{}, err
		}
	}
	return PreviewSettings{Inherit: config.Inherit, Selections: selected, EffectiveSelections: effective, GroupName: name, GroupNameOverride: config.GroupName, DefaultSelections: u.previewSelectionViews(ctx, event, defaults.Selections), DefaultGroupName: defaults.GroupName, DynamicAllocated: allocated}, nil
}
func (u *EventUseCase) PutPreviewSettings(ctx context.Context, event, user uuid.UUID, in PreviewSettingsInput) (PreviewSettings, error) {
	if err := u.RequireManageEvent(ctx, event, user); err != nil {
		return PreviewSettings{}, err
	}
	if err := u.RequireEventWritable(ctx, event); err != nil {
		return PreviewSettings{}, err
	}
	name, err := normalizePreviewGroupName(in.GroupName)
	if err != nil {
		return PreviewSettings{}, err
	}
	items := []preview.Selection{}
	if !in.Inherit {
		items, _, err = u.validatePreviewSelections(ctx, event, in.Selections)
		if err != nil {
			return PreviewSettings{}, err
		}
	}
	if err = participantPreviewRepo.New(u.repo).SaveConfiguration(ctx, event, items, in.Inherit, name, time.Now()); err != nil {
		return PreviewSettings{}, previewFailure(err)
	}
	return u.GetPreviewSettings(ctx, event, user)
}
func (u *EventUseCase) GetPreviewDefaults(ctx context.Context) (PreviewSettings, error) {
	defaults, err := u.previewDefaults(ctx)
	if err != nil {
		return PreviewSettings{}, err
	}
	views := u.previewSelectionViews(ctx, uuid.Nil, defaults.Selections)
	return PreviewSettings{GroupName: defaults.GroupName, Selections: views, EffectiveSelections: views, DefaultSelections: views, DefaultGroupName: defaults.GroupName, DynamicAllocated: true}, nil
}
func (u *EventUseCase) PutPreviewDefaults(ctx context.Context, in PreviewSettingsInput) (PreviewSettings, error) {
	name, err := normalizePreviewGroupName(in.GroupName)
	if err != nil {
		return PreviewSettings{}, err
	}
	items, views, err := u.validatePreviewSelections(ctx, uuid.Nil, in.Selections)
	if err != nil {
		return PreviewSettings{}, err
	}
	defaults := preview.Defaults{Selections: items}
	if name != nil {
		defaults.GroupName = *name
	}
	data, err := json.Marshal(defaults)
	if err != nil {
		return PreviewSettings{}, previewFailure(err)
	}
	_, err = platformSettingsRepo.New(u.repo).Upsert(ctx, settingsModel.NewPlatformSetting(preview.SettingKey, data, string(rbac.PermPlatformSettingsRead), time.Now()))
	if err != nil {
		return PreviewSettings{}, previewFailure(err)
	}
	return PreviewSettings{GroupName: defaults.GroupName, Selections: views, EffectiveSelections: views, DefaultSelections: views, DefaultGroupName: defaults.GroupName, DynamicAllocated: true}, nil
}

// Reasons a catalog task cannot be chosen for a preview.
const (
	PreviewReasonNotPublished           = "not_published"
	PreviewReasonRestrictedAccess       = "restricted_access"
	PreviewReasonArchived               = "archived"
	PreviewReasonNoPreviewVariant       = "no_preview_variant"
	PreviewReasonNoCapacity             = "no_capacity"
	previewCatalogPageSize        int32 = 100
)

type previewCandidate struct {
	exerciseID uuid.UUID
	name       string
	versionID  uuid.UUID
	reason     string
}

// listPreviewCatalogRows pages the catalog-scope tasks matching search; archived is "exclude" or "only".
// Users without exercises.read only see tasks they may already read.
func (u *EventUseCase) listPreviewCatalogRows(ctx context.Context, user uuid.UUID, search, archived string) ([]exerciseRepo.Listed, error) {
	visibility := exerciseRepo.Visibility{Scope: "catalog"}
	if !rbac.HasPermissionInContext(ctx, rbac.PermExercisesRead) {
		visibility.ViewerID = uuid.NullUUID{UUID: user, Valid: true}
	}
	var out []exerciseRepo.Listed
	// Keyset pages hold rows strictly before the cursor: start after everything.
	cursor, cursorID := cursorSentinelTime, maxUUID
	for {
		rows, err := u.exercises.ListCursor(ctx, exerciseRepo.ListParams{Search: search, Archived: archived, Visibility: visibility, CursorCreatedAt: cursor, CursorID: cursorID, Limit: previewCatalogPageSize})
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
		if len(rows) < int(previewCatalogPageSize) {
			return out, nil
		}
		last := rows[len(rows)-1]
		cursor, cursorID = last.CreatedAt, last.ID
	}
}

// ListPreviewCatalog returns every task matching the search: the ones that can be previewed first, then the others
// with the reason they cannot be chosen.
func (u *EventUseCase) ListPreviewCatalog(ctx context.Context, event, user uuid.UUID, search string) ([]PreviewCatalogItem, error) {
	search = strings.TrimSpace(search)
	if event != uuid.Nil {
		if err := u.RequireManageEvent(ctx, event, user); err != nil {
			return nil, err
		}
	}
	var eligible, blocked []previewCandidate
	seen := map[uuid.UUID]bool{}
	if event != uuid.Nil {
		rows, err := u.eventExercises.Catalog(ctx, event, search, "", nil)
		if err != nil {
			return nil, previewFailure(err)
		}
		for _, row := range rows {
			seen[row.ID] = true
			eligible = append(eligible, previewCandidate{exerciseID: row.ID, name: row.Name, versionID: row.PublishedVersionID})
		}
	}
	passes := []string{"exclude"}
	if rbac.HasPermissionInContext(ctx, rbac.PermExercisesRead) {
		// Archived tasks are listed for platform staff only.
		passes = append(passes, "only")
	}
	for _, archived := range passes {
		rows, err := u.listPreviewCatalogRows(ctx, user, search, archived)
		if err != nil {
			return nil, previewFailure(err)
		}
		for _, row := range rows {
			if seen[row.ID] {
				continue
			}
			c := previewCandidate{exerciseID: row.ID, name: row.Name}
			switch {
			case archived == "only":
				c.reason = PreviewReasonArchived
			case !row.PublishedVersionID.Valid:
				c.reason = PreviewReasonNotPublished
			case event != uuid.Nil || row.AccessLevel != exerciseModel.AccessAllEvents:
				// Published catalog tasks the event catalog did not offer are not available to the event.
				c.reason = PreviewReasonRestrictedAccess
			default:
				c.versionID = row.PublishedVersionID.UUID
				eligible = append(eligible, c)
				continue
			}
			blocked = append(blocked, c)
		}
	}
	out := []PreviewCatalogItem{}
	var rejected []PreviewCatalogItem
	for _, c := range eligible {
		item, reason, err := u.previewCatalogItem(ctx, event, c)
		if err != nil {
			return nil, err
		}
		if reason != "" {
			rejected = append(rejected, PreviewCatalogItem{ID: c.exerciseID, ExerciseVersionID: c.versionID, Name: c.name, Variants: []PreviewCatalogVariant{}, Reason: reason})
			continue
		}
		out = append(out, item)
	}
	for _, c := range blocked {
		rejected = append(rejected, PreviewCatalogItem{ID: c.exerciseID, ExerciseVersionID: c.versionID, Name: c.name, Variants: []PreviewCatalogVariant{}, Reason: c.reason})
	}
	return append(out, rejected...), nil
}

// previewCatalogItem builds the eligible item of a published task, or returns the reason it has no usable variant.
func (u *EventUseCase) previewCatalogItem(ctx context.Context, event uuid.UUID, c previewCandidate) (PreviewCatalogItem, string, error) {
	version, err := u.exercises.GetVersion(ctx, c.versionID)
	if err != nil {
		return PreviewCatalogItem{}, "", previewFailure(err)
	}
	item := PreviewCatalogItem{Eligible: true, ExerciseVersionID: c.versionID, Variants: []PreviewCatalogVariant{}}
	noCapacity := false
	for position, variant := range version.Variants {
		_, entry, _, e := u.previewVersion(ctx, event, c.versionID, variant.ID)
		if e != nil {
			continue
		}
		if event != uuid.Nil && len(variant.Topology.Devices) > 0 {
			if u.previewLabs == nil {
				noCapacity = true
				continue
			}
			canStart, e := u.previewLabs.CanStartEventPreview(ctx, event, c.versionID, variant.ID)
			if e != nil {
				return PreviewCatalogItem{}, "", e
			}
			if !canStart {
				noCapacity = true
				continue
			}
		}
		item.ID, item.Name = entry.ID, entry.Name
		item.Variants = append(item.Variants, PreviewCatalogVariant{ID: variant.ID, Index: variant.Index, Position: position, Infrastructure: len(variant.Topology.Devices) > 0})
		item.Infrastructure = item.Infrastructure || len(variant.Topology.Devices) > 0
	}
	if len(item.Variants) > 0 {
		return item, "", nil
	}
	if noCapacity {
		return PreviewCatalogItem{}, PreviewReasonNoCapacity, nil
	}
	return PreviewCatalogItem{}, PreviewReasonNoPreviewVariant, nil
}
func (u *EventUseCase) selectedPreview(ctx context.Context, event, user, selection uuid.UUID) (PreviewSelection, exerciseModel.Variant, error) {
	if err := u.RequireManageEvent(ctx, event, user); err != nil {
		return PreviewSelection{}, exerciseModel.Variant{}, err
	}
	items, _, err := u.previewSelections(ctx, event)
	if err != nil {
		return PreviewSelection{}, exerciseModel.Variant{}, previewFailure(err)
	}
	for _, item := range items {
		if selectionIdentity(item.ExerciseVersionID, item.VariantID) == selection {
			version, entry, variant, e := u.previewVersion(ctx, event, item.ExerciseVersionID, item.VariantID)
			return PreviewSelection{ID: selection, ExerciseVersionID: item.ExerciseVersionID, VariantID: item.VariantID, Name: entry.Name, VariantIndex: variant.Index, VariantPosition: previewVariantPosition(version, variant.ID)}, variant, e
		}
	}
	return PreviewSelection{}, exerciseModel.Variant{}, previewUnavailable()
}
func previewTask(variant exerciseModel.Variant, id uuid.UUID) (exerciseModel.Task, error) {
	for _, task := range variant.Tasks {
		if task.ID == id {
			return task, nil
		}
	}
	return exerciseModel.Task{}, previewUnavailable()
}

// withPreviewRun serializes one event/user's mutations across daemon replicas.
// The existing laboratory engine owns its independent lease even if this transaction fails.
func (u *EventUseCase) withPreviewRun(ctx context.Context, event, user, selection uuid.UUID, create bool, fn func(context.Context, *preview.Run, exerciseModel.Variant) error) (PreviewRunView, error) {
	item, variant, err := u.selectedPreview(ctx, event, user, selection)
	if err != nil {
		return PreviewRunView{}, err
	}
	if err = u.RequireEventWritable(ctx, event); err != nil {
		return PreviewRunView{}, err
	}
	txCtx, repo, tx, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return PreviewRunView{}, err
	}
	defer tx.Restore()
	store := participantPreviewRepo.New(repo)
	if err = store.Lock(txCtx, event, user); err != nil {
		return PreviewRunView{}, previewFailure(err)
	}
	// A selection may have been removed while this request waited for the owner lock.
	if _, _, err = u.selectedPreview(txCtx, event, user, selection); err != nil {
		return PreviewRunView{}, err
	}
	run, err := store.Get(txCtx, event, user, selection)
	missing := repositoryTools.IsObjectNotFoundError(err)
	if err != nil && !missing {
		return PreviewRunView{}, previewFailure(err)
	}
	now := time.Now()
	dynamic := len(variant.Topology.Devices) > 0
	if !missing && !run.ExpiresAt.After(now) {
		// Cleanup the old lease before reserving a replacement; cleanup remains owned.
		if run.DeployID.Valid && u.previewLabs != nil {
			if err = u.destroyOwnedPreviewLab(txCtx, user, run.DeployID.UUID); err != nil {
				return PreviewRunView{}, err
			}
		}
		missing = true
	}
	var createdDeploy uuid.UUID
	committed := false
	defer func() {
		if !committed && createdDeploy != uuid.Nil && u.previewLabs != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
			defer cancel()
			_ = u.destroyOwnedPreviewLab(cleanup, user, createdDeploy)
		}
	}()
	if missing {
		if dynamic && !create {
			return PreviewRunView{}, preview.ErrStartRequired.Err()
		}
		run = preview.NewRun(event, user, selection, now)
		for _, task := range variant.Tasks {
			if dynamic && task.LinkedDeviceID.Valid {
				continue
			}
			flag, e := flagpattern.Resolve(task.Flag, u.flagRandomBytes, rand.Reader)
			if e != nil {
				return PreviewRunView{}, previewFailure(e)
			}
			run.Flags[task.ID.String()] = flag
		}
		if dynamic {
			if u.previewLabs == nil {
				return PreviewRunView{}, infraUnavailable()
			}
			handle, e := u.previewLabs.DeployEventPreviewVariantTest(txCtx, event, user, item.ExerciseVersionID, item.VariantID)
			if e != nil {
				return PreviewRunView{}, e
			}
			id, e := uuid.FromString(handle.Group)
			if e != nil {
				return PreviewRunView{}, previewFailure(e)
			}
			createdDeploy = id
			run.DeployID = uuid.NullUUID{UUID: id, Valid: true}
			// Match the engine lease, including the event reservation's earlier end.
			if leases, ok := u.previewLabs.(interface {
				ListTestDeploys(context.Context, uuid.UUID, uuid.UUID) ([]exerciseModel.TestDeploy, error)
			}); ok {
				rows, e := leases.ListTestDeploys(txCtx, user, uuid.Nil)
				if e != nil {
					return PreviewRunView{}, e
				}
				found := false
				for _, deploy := range rows {
					if deploy.ID == id {
						found = true
						if deploy.ExpiresAt.Before(run.ExpiresAt) {
							run.ExpiresAt = deploy.ExpiresAt
						}
					}
				}
				if !found || !run.ExpiresAt.After(now) {
					return PreviewRunView{}, previewUnavailable()
				}
			}
		}
	}
	if fn != nil {
		if err = fn(txCtx, &run, variant); err != nil {
			return PreviewRunView{}, err
		}
	}
	if err = store.Save(txCtx, run); err != nil {
		return PreviewRunView{}, previewFailure(err)
	}
	if err = tx.Save(); err != nil {
		return PreviewRunView{}, previewFailure(err)
	}
	committed = true
	return PreviewRunView{ID: run.ID, SelectionID: selection, ExpiresAt: run.ExpiresAt, Dynamic: dynamic}, nil
}
func (u *EventUseCase) StartPreviewRun(ctx context.Context, event, user, selection uuid.UUID) (PreviewRunView, error) {
	return u.withPreviewRun(ctx, event, user, selection, true, nil)
}
func (u *EventUseCase) SubmitPreviewFlag(ctx context.Context, event, user, selection, taskID uuid.UUID, flag string) (SubmitChallengeResult, error) {
	var result SubmitChallengeResult
	_, err := u.withPreviewRun(ctx, event, user, selection, false, func(ctx context.Context, run *preview.Run, variant exerciseModel.Variant) error {
		task, e := previewTask(variant, taskID)
		if e != nil {
			return e
		}
		if run.DeployID.Valid && task.LinkedDeviceID.Valid {
			result.Correct, e = u.previewLabs.CheckTestFlag(ctx, user, run.DeployID.UUID, taskID, flag)
			if e != nil {
				return e
			}
			if result.Correct {
				result.FirstSolve = run.MarkSolved(taskID, time.Now())
			}
		} else {
			result.Correct, result.FirstSolve = run.Check(taskID, flag, time.Now())
		}
		run.RecordAttempt(taskID, result.Correct)
		return nil
	})
	return result, err
}
func previewHints(task exerciseModel.Task, run *preview.Run) []OwnHintView {
	out := make([]OwnHintView, 0, len(task.Hints))
	for _, hint := range task.Hints {
		view := OwnHintView{ID: hint.ID}
		if run != nil {
			if at, ok := run.Hints[task.ID.String()][hint.ID.String()]; ok {
				text := hint.Text
				view.Unlocked = true
				view.Content = &text
				view.UnlockedAt = &at
			}
		}
		out = append(out, view)
	}
	return out
}
func (u *EventUseCase) UnlockPreviewHint(ctx context.Context, event, user, selection, taskID, hintID uuid.UUID) (OwnHintView, error) {
	var result OwnHintView
	_, err := u.withPreviewRun(ctx, event, user, selection, false, func(ctx context.Context, run *preview.Run, variant exerciseModel.Variant) error {
		task, e := previewTask(variant, taskID)
		if e != nil {
			return e
		}
		found := false
		for _, hint := range task.Hints {
			if hint.ID == hintID {
				found = true
			}
		}
		if !found {
			return previewUnavailable()
		}
		run.UnlockHint(taskID, hintID, time.Now())
		for _, hint := range previewHints(task, run) {
			if hint.ID == hintID {
				result = hint
			}
		}
		return nil
	})
	return result, err
}
func (u *EventUseCase) PreviewBoard(ctx context.Context, event, user uuid.UUID) (PreviewBoard, error) {
	if err := u.RequireManageEvent(ctx, event, user); err != nil {
		return PreviewBoard{}, err
	}
	settings, err := u.GetPreviewSettings(ctx, event, user)
	if err != nil {
		return PreviewBoard{}, err
	}
	items := settings.EffectiveSelections
	groupID := uuid.NewV5(uuid.NamespaceURL, "cybericebox:participant-preview-group:"+event.String())
	out := PreviewBoard{Challenges: []PreviewChallenge{}, Stages: []BoardStageView{}, ServerNow: time.Now()}
	store := participantPreviewRepo.New(u.repo)
	for _, item := range items {
		_, _, variant, e := u.previewVersion(ctx, event, item.ExerciseVersionID, item.VariantID)
		if e != nil {
			continue
		}
		if len(variant.Topology.Devices) > 0 {
			if u.previewLabs == nil {
				continue
			}
			allocated, allocErr := u.previewLabs.EventPreviewAllocated(ctx, event)
			if allocErr != nil {
				return PreviewBoard{}, allocErr
			}
			if !allocated {
				continue
			}
		}
		selection := selectionIdentity(item.ExerciseVersionID, item.VariantID)
		run, e := store.Get(ctx, event, user, selection)
		var active *preview.Run
		if e == nil && run.ExpiresAt.After(out.ServerNow) {
			active = &run
		} else if e != nil && !repositoryTools.IsObjectNotFoundError(e) {
			return PreviewBoard{}, previewFailure(e)
		}
		for _, task := range variant.Tasks {
			// Explicit projection: never serialize the catalog Task, variant topology, flags or hint texts.
			snapshot, e := json.Marshal(struct {
				Name         string                      `json:"name"`
				Description  json.RawMessage             `json:"description"`
				Difficulty   exerciseModel.Difficulty    `json:"difficulty"`
				Placeholders []exerciseModel.Placeholder `json:"placeholders,omitempty"`
			}{task.Name, task.Description, task.Difficulty, task.Placeholders})
			if e != nil {
				return PreviewBoard{}, previewFailure(e)
			}
			canStart := len(variant.Topology.Devices) == 0
			if !canStart && u.previewLabs != nil {
				canStart, _ = u.previewLabs.CanStartEventPreview(ctx, event, item.ExerciseVersionID, item.VariantID)
			}
			challenge := PreviewChallenge{OwnChallengeView: OwnChallengeView{ID: task.ID, EventChallengeID: task.ID, EventExerciseID: selection, Snapshot: snapshot, Readiness: teamChallengeModel.ReadinessPublished, Points: 100, Order: int32(len(out.Challenges)), Infrastructure: len(variant.Topology.Devices) > 0, HintsEnabled: true, Files: []ChallengeFileView{}, Hints: previewHints(task, active), Prerequisites: []ChallengePrerequisiteView{}, BoardPublished: true, GroupName: settings.GroupName, GroupID: &groupID}, PreviewSelectionID: selection, PreviewDynamic: len(variant.Topology.Devices) > 0, PreviewCanStart: canStart}
			for _, file := range task.Attachments {
				challenge.Files = append(challenge.Files, ChallengeFileView{FileID: file.FileID, Name: file.Name})
			}
			if active != nil {
				challenge.PreviewAttempts = active.Attempts[task.ID.String()]
				challenge.PreviewCorrectAttempts = active.CorrectAttempts[task.ID.String()]
				challenge.PreviewRunID = &active.ID
				challenge.PreviewExpiresAt = &active.ExpiresAt
				if at, ok := active.Solved[task.ID.String()]; ok {
					challenge.SolvedAt = &at
					points := int32(100)
					zero := int32(0)
					challenge.AwardedPoints = &points
					challenge.HintPenalty = &zero
				}
			}
			out.Challenges = append(out.Challenges, challenge)
			out.Availability.Total++
			out.Availability.Ready++
			out.Availability.Available++
		}
	}
	return out, nil
}
func (u *EventUseCase) ownedPreviewRun(ctx context.Context, event, user, selection uuid.UUID) (preview.Run, error) {
	if _, _, err := u.selectedPreview(ctx, event, user, selection); err != nil {
		return preview.Run{}, err
	}
	run, err := participantPreviewRepo.New(u.repo).Get(ctx, event, user, selection)
	if repositoryTools.IsObjectNotFoundError(err) || err == nil && !run.ExpiresAt.After(time.Now()) {
		return preview.Run{}, previewUnavailable()
	}
	if err != nil {
		return preview.Run{}, previewFailure(err)
	}
	return run, nil
}
func (u *EventUseCase) PreviewLabStatus(ctx context.Context, event, user, selection uuid.UUID) (exerciseModel.LabDeployStatus, error) {
	run, err := u.ownedPreviewRun(ctx, event, user, selection)
	if err != nil {
		return exerciseModel.LabDeployStatus{}, err
	}
	if !run.DeployID.Valid || u.previewLabs == nil {
		return exerciseModel.LabDeployStatus{}, previewUnavailable()
	}
	return u.previewLabs.DeployTestStatus(ctx, user, run.DeployID.UUID)
}
func (u *EventUseCase) PreviewProxyLink(ctx context.Context, event, user, selection uuid.UUID, device string, port int32) (labaccess.Link, error) {
	run, err := u.ownedPreviewRun(ctx, event, user, selection)
	if err != nil {
		return labaccess.Link{}, err
	}
	if !run.DeployID.Valid || u.previewLabs == nil {
		return labaccess.Link{}, previewUnavailable()
	}
	return u.previewLabs.OpenTestDeployLink(ctx, user, run.DeployID.UUID, device, port)
}
func (u *EventUseCase) EndPreviewRun(ctx context.Context, event, user, selection uuid.UUID) error {
	// Selection authorization intentionally omitted: removing a selection must not prevent owned cleanup.
	if err := u.RequireManageEvent(ctx, event, user); err != nil {
		return err
	}
	txCtx, repo, tx, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer tx.Restore()
	store := participantPreviewRepo.New(repo)
	if err = store.Lock(txCtx, event, user); err != nil {
		return previewFailure(err)
	}
	run, err := store.Get(txCtx, event, user, selection)
	if repositoryTools.IsObjectNotFoundError(err) {
		return nil
	}
	if err != nil {
		return previewFailure(err)
	}
	if run.DeployID.Valid && u.previewLabs != nil {
		if err = u.destroyOwnedPreviewLab(txCtx, user, run.DeployID.UUID); err != nil {
			return err
		}
	}
	if err = store.Delete(txCtx, event, user, selection); err != nil {
		return previewFailure(err)
	}
	return tx.Save()
}
func (u *EventUseCase) PreviewSolves(ctx context.Context, event, user, selection, taskID uuid.UUID) (ChallengeSolvesPage, error) {
	_, variant, err := u.selectedPreview(ctx, event, user, selection)
	if err != nil {
		return ChallengeSolvesPage{}, err
	}
	if _, err = previewTask(variant, taskID); err != nil {
		return ChallengeSolvesPage{}, err
	}
	out := ChallengeSolvesPage{Items: []ChallengeSolveView{}}
	run, err := participantPreviewRepo.New(u.repo).Get(ctx, event, user, selection)
	if repositoryTools.IsObjectNotFoundError(err) {
		return out, nil
	}
	if err != nil {
		return out, previewFailure(err)
	}
	if at, ok := run.Solved[taskID.String()]; ok && run.ExpiresAt.After(time.Now()) {
		out.Items = append(out.Items, ChallengeSolveView{SolvedAt: at, Own: true})
		out.Total = 1
	}
	return out, nil
}
func (u *EventUseCase) StreamPreviewFile(ctx context.Context, event, user, selection, taskID, fileID uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	_, variant, err := u.selectedPreview(ctx, event, user, selection)
	if err != nil {
		return nil, mediaModel.File{}, err
	}
	task, err := previewTask(variant, taskID)
	if err != nil {
		return nil, mediaModel.File{}, err
	}
	found := false
	for _, file := range task.Attachments {
		if file.FileID == fileID {
			found = true
		}
	}
	if !found || u.brandMedia == nil {
		return nil, mediaModel.File{}, previewUnavailable()
	}
	return u.brandMedia.StreamFile(ctx, fileID)
}

// EndPreviewRuns also cleans removed selections; the owner lock prevents racing starts.
func (u *EventUseCase) EndPreviewRuns(ctx context.Context, event, user uuid.UUID) error {
	if err := u.RequireManageEvent(ctx, event, user); err != nil {
		return err
	}
	txCtx, repo, tx, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer tx.Restore()
	store := participantPreviewRepo.New(repo)
	if err = store.Lock(txCtx, event, user); err != nil {
		return previewFailure(err)
	}
	runs, err := store.List(txCtx, event, user)
	if err != nil {
		return previewFailure(err)
	}
	for _, run := range runs {
		if run.DeployID.Valid {
			if u.previewLabs == nil {
				return infraUnavailable()
			}
			if err = u.destroyOwnedPreviewLab(txCtx, user, run.DeployID.UUID); err != nil {
				return err
			}
		}
		if err = store.Delete(txCtx, event, user, run.SelectionID); err != nil {
			return previewFailure(err)
		}
	}
	return tx.Save()
}

func previewVariantPosition(version exerciseModel.ExerciseVersion, id uuid.UUID) int {
	for i, variant := range version.Variants {
		if variant.ID == id {
			return i
		}
	}
	return 0
}

// The engine's asynchronous expiry may already have removed this owned lease.
func (u *EventUseCase) destroyOwnedPreviewLab(ctx context.Context, user, id uuid.UUID) error {
	if u.previewLabs == nil {
		return infraUnavailable()
	}
	err := u.previewLabs.DestroyDeployTest(ctx, user, id)
	if errors.Is(err, exerciseModel.ErrTestDeployNotFound.Err()) {
		return nil
	}
	return err
}
