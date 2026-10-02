package exercise

import (
	"context"
	"strconv"
	"strings"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
)

// ResourceTotals is what a topology or a task version needs: its container devices and their CPU and memory.
type ResourceTotals struct {
	Devices       int
	CPUMillicores int64
	MemoryBytes   int64
}

// ResourceRange is the least and the most a task needs over its variants (equal for a single variant). Event
// planning reserves the Max.
type ResourceRange struct {
	Min, Max ResourceTotals
}

// VariantResources is the total of one variant.
type VariantResources struct {
	VariantID uuid.UUID
	ResourceTotals
}

// DeviceOutside is a device that passes the platform frame. Covered: an approved elevation holds it (it is
// then allowed to publish). AboveCeiling: no approval can cover it.
type DeviceOutside struct {
	VariantID     uuid.UUID
	DeviceID      uuid.UUID
	Name          string
	CPUMillicores int64
	MemoryBytes   int64
	Covered       bool
	AboveCeiling  bool
}

// VersionResources is the resources view of one version: totals (over variants and per variant), the
// devices outside the frame, and whether the task is resource-heavy.
type VersionResources struct {
	ResourceRange
	Variants []VariantResources
	// SpreadPercent is how far the variants differ: the largest of (max-min)/max over CPU and memory, in
	// percent. The editor warns above 25.
	SpreadPercent int
	// Outside lists the devices that pass the frame (a draft always saves; publishing needs every one of them
	// Covered).
	Outside []DeviceOutside
	// Heavy: some device passes the frame and an approved elevation holds it.
	Heavy bool
}

// versionView is the version for the editor: secrets masked, its resources (totals, devices outside the
// frame) and the exercise's elevation request.
func (u *ExerciseUseCase) versionView(ctx context.Context, v exerciseModel.ExerciseVersion) VersionView {
	view := toVersionView(v)
	view.Resources = u.versionResources(ctx, v.ExerciseID, v.Variants)
	view.Elevation = u.elevationView(ctx, v.ExerciseID, "")
	view.AuthorName = u.authorNames(ctx, v.CreatedBy)[v.CreatedBy.UUID]
	return view
}

// authorNames resolves the first and last names of the given authors (never an email). Best effort: a failed
// lookup leaves the names empty rather than failing the read they decorate.
func (u *ExerciseUseCase) authorNames(ctx context.Context, authors ...uuid.NullUUID) map[uuid.UUID]string {
	ids := make([]uuid.UUID, 0, len(authors))
	for _, author := range authors {
		if author.Valid {
			ids = append(ids, author.UUID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	names, err := u.exercises.UserNames(ctx, ids)
	if err != nil {
		log.Warn().Err(err).Msg("exercise author names unavailable")
		return nil
	}
	return names
}

// SpreadWarnPercent is the variant difference above which the editor warns.
const SpreadWarnPercent = 25

func totalsOf(t resourcesModel.Totals) ResourceTotals {
	return ResourceTotals{Devices: t.Devices, CPUMillicores: t.CPUMillicores, MemoryBytes: t.MemoryBytes}
}

func rangeOf(r resourcesModel.Range) ResourceRange {
	return ResourceRange{Min: totalsOf(r.Min), Max: totalsOf(r.Max)}
}

// Policy is the platform's device resources settings (the owner's defaults when none were given).
func (u *ExerciseUseCase) Policy() resourcesModel.Policy {
	if len(u.resources.Presets) == 0 {
		return resourcesModel.DefaultPolicy()
	}
	return u.resources
}

// spreadPercent is the largest relative difference between the least and the most a variant needs.
func spreadPercent(r resourcesModel.Range) int {
	spread := func(lo, hi int64) int {
		if hi <= 0 {
			return 0
		}
		return int((hi - lo) * 100 / hi)
	}
	return max(spread(r.Min.CPUMillicores, r.Max.CPUMillicores), spread(r.Min.MemoryBytes, r.Max.MemoryBytes))
}

// approvals are the approved values of every elevation of the exercise. Without a store there are none.
func (u *ExerciseUseCase) approvals(ctx context.Context, exerciseID uuid.UUID) ([]resourcesModel.Approval, error) {
	if u.elevations == nil || exerciseID == uuid.Nil {
		return nil, nil
	}
	byExercise, err := u.elevations.ApprovedFor(ctx, []uuid.UUID{exerciseID})
	if err != nil {
		return nil, err
	}
	return byExercise[exerciseID], nil
}

// versionResources reads the resources of the variants of one exercise version. A failed read of the
// approvals only loses the "covered" marks: it never fails the read it decorates.
func (u *ExerciseUseCase) versionResources(ctx context.Context, exerciseID uuid.UUID, variants []exerciseModel.Variant) VersionResources {
	policy := u.Policy()
	r := policy.VariantRange(variants)
	out := VersionResources{ResourceRange: rangeOf(r), Variants: make([]VariantResources, 0, len(variants)), SpreadPercent: spreadPercent(r)}
	for _, v := range variants {
		out.Variants = append(out.Variants, VariantResources{VariantID: v.ID, ResourceTotals: totalsOf(policy.Total(v.Topology))})
	}
	outside := policy.OutsideFrame(variants)
	if len(outside) == 0 {
		out.Outside = []DeviceOutside{}
		return out
	}
	approved, err := u.approvals(ctx, exerciseID)
	if err != nil {
		log.Warn().Err(err).Str("exercise_id", exerciseID.String()).Msg("Exercise resource elevations unavailable")
	}
	out.Outside = make([]DeviceOutside, 0, len(outside))
	for _, o := range outside {
		covered := !o.AboveCeiling && resourcesModel.Covered(o, approved)
		out.Heavy = out.Heavy || covered
		out.Outside = append(out.Outside, DeviceOutside{
			VariantID: o.VariantID, DeviceID: o.DeviceID, Name: o.Name, CPUMillicores: o.CPUMillicores, MemoryBytes: o.MemoryBytes,
			Covered: covered, AboveCeiling: o.AboveCeiling,
		})
	}
	return out
}

// requireResourcesAllowed is the publish gate of the resources model: every device names a preset the
// platform offers, none is above the elevation ceiling, and each one passing the frame is covered by an
// approved elevation. It needs no agent.
func (u *ExerciseUseCase) requireResourcesAllowed(ctx context.Context, exerciseID uuid.UUID, variants []exerciseModel.Variant) error {
	policy := u.Policy()
	for _, v := range variants {
		for _, d := range v.Topology.Devices {
			if d.Type == exerciseModel.DeviceTypeContainer && policy.InvalidPreset(d) {
				return exerciseModel.ErrDeviceResourcePresetInvalid.WithContext("variant", v.ID.String()).
					WithContext("device", d.Name).WithContext("preset", d.ResourcePreset).Err()
			}
		}
	}
	outside := policy.OutsideFrame(variants)
	if len(outside) == 0 {
		return nil
	}
	var above []DeviceOutside
	for _, o := range outside {
		if o.AboveCeiling {
			above = append(above, DeviceOutside{VariantID: o.VariantID, DeviceID: o.DeviceID, Name: o.Name, CPUMillicores: o.CPUMillicores, MemoryBytes: o.MemoryBytes, AboveCeiling: true})
		}
	}
	if len(above) > 0 {
		return exerciseModel.ErrDeviceResourcesAboveCeiling.WithContext("devices", describeOutside(above)).
			WithContext("ceilingCpuMillicores", policy.Ceiling.CPUMillicores).WithContext("ceilingMemoryBytes", policy.Ceiling.MemoryBytes).Err()
	}
	approved, err := u.approvals(ctx, exerciseID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to read the resource elevations").Err()
	}
	var uncovered []DeviceOutside
	for _, o := range outside {
		if !resourcesModel.Covered(o, approved) {
			uncovered = append(uncovered, DeviceOutside{VariantID: o.VariantID, DeviceID: o.DeviceID, Name: o.Name, CPUMillicores: o.CPUMillicores, MemoryBytes: o.MemoryBytes})
		}
	}
	if len(uncovered) > 0 {
		return exerciseModel.ErrDevicesNeedElevation.WithContext("devices", describeOutside(uncovered)).
			WithContext("frameCpuMillicores", policy.Frame.CPUMillicores).WithContext("frameMemoryBytes", policy.Frame.MemoryBytes).Err()
	}
	return nil
}

// describeOutside lists devices as "name: cpu m / memory bytes" for an error context.
func describeOutside(devices []DeviceOutside) string {
	parts := make([]string, 0, len(devices))
	for _, d := range devices {
		parts = append(parts, d.Name+": "+strconv.FormatInt(d.CPUMillicores, 10)+"m / "+strconv.FormatInt(d.MemoryBytes, 10))
	}
	return strings.Join(parts, ", ")
}

// publishedResource is the resources of one exercise's published version: the range over the variants, and
// whether an approved elevation holds a device above the frame (resource-heavy).
type publishedResource struct {
	Range ResourceRange
	Heavy bool
}

// publishedResources reads it for a page of exercises; exercises without a published version are absent.
func (u *ExerciseUseCase) publishedResources(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]publishedResource, error) {
	variants, err := u.exercises.PublishedVariants(ctx, ids)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to read the exercise resources").Err()
	}
	policy := u.Policy()
	out := make(map[uuid.UUID]publishedResource, len(variants))
	var needApprovals []uuid.UUID
	for id, vs := range variants {
		out[id] = publishedResource{Range: rangeOf(policy.VariantRange(vs))}
		if len(policy.OutsideFrame(vs)) > 0 {
			needApprovals = append(needApprovals, id)
		}
	}
	if len(needApprovals) == 0 || u.elevations == nil {
		return out, nil
	}
	approved, err := u.elevations.ApprovedFor(ctx, needApprovals)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to read the resource elevations").Err()
	}
	for _, id := range needApprovals {
		for _, o := range policy.OutsideFrame(variants[id]) {
			if !o.AboveCeiling && resourcesModel.Covered(o, approved[id]) {
				entry := out[id]
				entry.Heavy = true
				out[id] = entry
				break
			}
		}
	}
	return out, nil
}
