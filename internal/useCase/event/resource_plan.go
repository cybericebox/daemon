package event

import (
	"context"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/model"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
)

// ApprovalStore reads the approved resource elevations of exercises (satisfied by *exerciseRepo.Repository).
type ApprovalStore interface {
	ApprovedFor(ctx context.Context, exerciseIDs []uuid.UUID) (map[uuid.UUID][]resourcesModel.Approval, error)
}

// resourcePlanner is the optional capability of the infrastructure port that knows the agents' formulas and
// maxima (the fleet). It never names an agent.
type resourcePlanner interface {
	GroupSizes(plan infraModel.GroupPlan) (infraModel.GroupSizes, bool)
	NeedFit(need infraModel.PlacementNeed) *infraModel.FitViolation
}

// resourcePolicy is the platform's device resources settings (the owner's defaults when none were given).
func (u *EventUseCase) resourcePolicy() resourcesModel.Policy {
	if len(u.resources.Presets) == 0 {
		return resourcesModel.DefaultPolicy()
	}
	return u.resources
}

// PlanTask is one attached task in the event's resource plan.
type PlanTask struct {
	EventExerciseID uuid.UUID
	ExerciseID      uuid.UUID
	ExerciseName    string
	// Range is the least and the most the task needs over its variants.
	Range resourcesModel.Range
	// Reserved is what planning reserves per team: the largest variant (the pinned one for a fixed variant).
	Reserved resourcesModel.Totals
	// Heavy: an approved elevation holds a device of the task above the platform frame.
	Heavy bool
	// InternetLab: some variant of the task has the internet on, so its lab needs the group's gateway.
	InternetLab bool
	// NoAgentFits: no agent that is used has device maxima that hold the task (the readiness alarm's case).
	NoAgentFits bool

	// deviceMax and labDevices are what an agent must hold for the task: its largest device over the
	// variants and the most devices of one variant.
	deviceMax  resourcesModel.Amount
	labDevices int
}

// GroupOverhead is a team's lab group's own pods, computed with the agents' formula for the plan.
type GroupOverhead struct {
	// MaxUsers is the event's maximum team size (1 without teams); InternetLabs the tasks with an internet lab.
	MaxUsers     int
	InternetLabs int
	// VPN and Gateway are the pods rounded up to whole blocks, with their blocks.
	VPN           resourcesModel.Amount
	Gateway       resourcesModel.Amount
	VPNBlocks     int
	GatewayBlocks int
	// Known is false while no agent reported its sizing (the pods then add nothing to the plan).
	Known bool
	// TooLarge: the event's maximum team size (or its internet labs) is above what every agent that is used can
	// size a group for: a planning error.
	TooLarge bool
}

// EventResourcePlan is what the event reserves: per team the devices of its tasks plus the group's VPN and
// gateway, and the total for the teams.
type EventResourcePlan struct {
	Tasks []PlanTask
	// TeamTasks is the sum of Reserved over the tasks.
	TeamTasks resourcesModel.Totals
	Group     GroupOverhead
	// PerTeam is TeamTasks plus the group overhead (the overhead pods are not counted as devices).
	PerTeam resourcesModel.Totals
	// Teams is the number of teams reserved for: MaxTeams when set, else the teams there are now (at least 1);
	// TeamsBasis says which (max_teams | current).
	Teams      int
	TeamsBasis string
	Total      resourcesModel.Totals
	// NoAgentFits: some task cannot be placed on any agent that is used.
	NoAgentFits bool
}

// planInputs is what the plan is computed from.
type planInputs struct {
	policy      resourcesModel.Policy
	maxUsers    int
	teams       int
	teamsBasis  string
	tasks       []PlanTask
	maxDevice   resourcesModel.Amount
	maxLabSize  int
	internetLab int
}

// placementNeed is what a team's group asks of an agent.
func (p planInputs) placementNeed() infraModel.PlacementNeed {
	return infraModel.PlacementNeed{
		Device: p.maxDevice, LabDevices: p.maxLabSize,
		Plan: infraModel.GroupPlan{MaxUsers: p.maxUsers, InternetLabs: p.internetLab},
	}
}

// resourcePlanInputs reads the event's active attachments and config into what planning needs.
func (u *EventUseCase) resourcePlanInputs(ctx context.Context, eventID uuid.UUID) (planInputs, error) {
	policy := u.resourcePolicy()
	in := planInputs{policy: policy, maxUsers: 1, teams: 1, teamsBasis: "current"}
	config, err := u.configs.Get(ctx, eventID)
	if err != nil {
		return in, model.ErrPlatform.WithError(err).WithMessage("Failed to get event config for the resource plan").Err()
	}
	if config.IsTeamMode() {
		in.maxUsers = int(max(config.MaxTeamSize, 1))
		if config.MaxTeams != nil {
			in.teams, in.teamsBasis = int(*config.MaxTeams), "max_teams"
		} else if count, countErr := u.teams.Count(ctx, eventID); countErr == nil {
			in.teams = int(max(count, 1))
		}
	}
	all, err := u.eventExercises.List(ctx, eventID)
	if err != nil {
		return in, model.ErrPlatform.WithError(err).WithMessage("Failed to list event exercises for the resource plan").Err()
	}
	links := activeAttachments(all)
	versionIDs, exerciseIDs := make([]uuid.UUID, 0, len(links)), make([]uuid.UUID, 0, len(links))
	for _, link := range links {
		versionIDs, exerciseIDs = append(versionIDs, link.ExerciseVersionID), append(exerciseIDs, link.ExerciseID)
	}
	variants, err := u.exercises.VersionVariants(ctx, versionIDs)
	if err != nil {
		return in, model.ErrPlatform.WithError(err).WithMessage("Failed to read the task resources").Err()
	}
	approvals := u.approvals(ctx, exerciseIDs)
	names := map[uuid.UUID]string{}
	if details, detailsErr := u.eventExercises.Details(ctx, eventID); detailsErr == nil {
		for _, d := range details {
			names[d.EventExercise.ID] = d.ExerciseName
		}
	}
	for _, link := range links {
		vs := variants[link.ExerciseVersionID]
		task := planTask(policy, link, vs, approvals[link.ExerciseID])
		task.ExerciseName = names[link.ID]
		in.tasks = append(in.tasks, task)
		in.maxLabSize = max(in.maxLabSize, task.labDevices)
		in.maxDevice = in.maxDevice.Max(task.deviceMax)
		if task.InternetLab {
			in.internetLab++
		}
	}
	return in, nil
}

// planTask folds one attachment: the range over its variants, what is reserved, whether it is heavy.
func planTask(policy resourcesModel.Policy, link eventExerciseModel.EventExercise, variants []exerciseModel.Variant, approved []resourcesModel.Approval) PlanTask {
	task := PlanTask{EventExerciseID: link.ID, ExerciseID: link.ExerciseID}
	r := policy.VariantRange(variants)
	task.Range, task.Reserved = r, r.Max
	if link.VariantMode == eventExerciseModel.VariantModeFixed && link.FixedVariantIndex != nil && int(*link.FixedVariantIndex) < len(variants) {
		task.Reserved = policy.Total(variants[*link.FixedVariantIndex].Topology)
	}
	for _, o := range policy.OutsideFrame(variants) {
		if !o.AboveCeiling && resourcesModel.Covered(o, approved) {
			task.Heavy = true
			break
		}
	}
	for _, v := range variants {
		if v.Topology.Internet.Enabled {
			task.InternetLab = true
		}
		devices := policy.Devices(v.Topology)
		task.labDevices = max(task.labDevices, len(devices))
		for _, d := range devices {
			task.deviceMax = task.deviceMax.Max(d.Amount)
		}
	}
	return task
}

// approvals reads the approved elevations of the exercises; a failed read only loses the heavy marks.
func (u *EventUseCase) approvals(ctx context.Context, exerciseIDs []uuid.UUID) map[uuid.UUID][]resourcesModel.Approval {
	if u.elevations == nil || len(exerciseIDs) == 0 {
		return nil
	}
	approved, err := u.elevations.ApprovedFor(ctx, exerciseIDs)
	if err != nil {
		log.Warn().Err(err).Msg("Exercise resource elevations unavailable")
		return nil
	}
	return approved
}

// GetResourcePlan computes the event's resource plan: the devices of its tasks (the largest variant of each),
// the group's VPN sized by the maximum team size and gateway sized by its internet labs, with the agents'
// formula, and the total for the teams. It reads no agent state beyond what agents reported.
func (u *EventUseCase) GetResourcePlan(ctx context.Context, eventID uuid.UUID) (EventResourcePlan, error) {
	in, err := u.resourcePlanInputs(ctx, eventID)
	if err != nil {
		return EventResourcePlan{}, err
	}
	plan := EventResourcePlan{Tasks: in.tasks, Teams: in.teams, TeamsBasis: in.teamsBasis}
	if plan.Tasks == nil {
		plan.Tasks = []PlanTask{}
	}
	planner, _ := u.infra.(resourcePlanner)
	for i := range plan.Tasks {
		plan.TeamTasks = plan.TeamTasks.Add(plan.Tasks[i].Reserved)
		if planner != nil {
			need := infraModel.PlacementNeed{Device: plan.Tasks[i].deviceMax, LabDevices: plan.Tasks[i].labDevices}
			if need.Known() && planner.NeedFit(need) != nil {
				plan.Tasks[i].NoAgentFits, plan.NoAgentFits = true, true
			}
		}
	}
	plan.Group = GroupOverhead{MaxUsers: in.maxUsers, InternetLabs: in.internetLab}
	if planner != nil {
		if sizes, known := planner.GroupSizes(infraModel.GroupPlan{MaxUsers: in.maxUsers, InternetLabs: in.internetLab}); known {
			plan.Group.VPN, plan.Group.Gateway, plan.Group.Known = sizes.VPN, sizes.Gateway, true
			plan.Group.VPNBlocks, plan.Group.GatewayBlocks = u.resourcePolicy().BlocksOf(sizes.VPN), u.resourcePolicy().BlocksOf(sizes.Gateway)
		}
	}
	if planner != nil && planner.NeedFit(infraModel.PlacementNeed{Plan: infraModel.GroupPlan{MaxUsers: in.maxUsers, InternetLabs: in.internetLab}}) != nil {
		plan.Group.TooLarge, plan.NoAgentFits = true, true
	}
	plan.PerTeam = plan.TeamTasks
	plan.PerTeam.Amount = plan.PerTeam.Amount.Add(plan.Group.VPN).Add(plan.Group.Gateway)
	plan.PerTeam.Blocks += plan.Group.VPNBlocks + plan.Group.GatewayBlocks
	for i := 0; i < plan.Teams; i++ {
		plan.Total = plan.Total.Add(plan.PerTeam)
	}
	return plan, nil
}

// taskResources is the resources view of one pinned task version.
type taskResources struct {
	Range       resourcesModel.Range
	Heavy       bool
	NoAgentFits bool
}

// versionsResources reads, for pinned versions (version id to exercise id), their resources: the range over
// the variants, whether an approved elevation holds a device above the frame, and whether no agent that is
// used can run them. A failed read of the approvals only loses the heavy marks.
func (u *EventUseCase) versionsResources(ctx context.Context, versions map[uuid.UUID]uuid.UUID) (map[uuid.UUID]taskResources, error) {
	out := make(map[uuid.UUID]taskResources, len(versions))
	if len(versions) == 0 {
		return out, nil
	}
	versionIDs, exerciseIDs := make([]uuid.UUID, 0, len(versions)), make([]uuid.UUID, 0, len(versions))
	for version, exercise := range versions {
		versionIDs, exerciseIDs = append(versionIDs, version), append(exerciseIDs, exercise)
	}
	variants, err := u.exercises.VersionVariants(ctx, versionIDs)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to read the task resources").Err()
	}
	policy := u.resourcePolicy()
	planner, _ := u.infra.(resourcePlanner)
	approvals := u.approvals(ctx, exerciseIDs)
	for version, exercise := range versions {
		vs := variants[version]
		task := planTask(policy, eventExerciseModel.EventExercise{ExerciseID: exercise}, vs, approvals[exercise])
		res := taskResources{Range: task.Range, Heavy: task.Heavy}
		if planner != nil {
			need := infraModel.PlacementNeed{Device: task.deviceMax, LabDevices: task.labDevices}
			res.NoAgentFits = need.Known() && planner.NeedFit(need) != nil
		}
		out[version] = res
	}
	return out, nil
}

// applyVersionResources fills the resources of one attached version into its view.
func (u *EventUseCase) applyVersionResources(ctx context.Context, view *EventExerciseView, version exerciseModel.ExerciseVersion) {
	policy := u.resourcePolicy()
	task := planTask(policy, eventExerciseModel.EventExercise{ExerciseID: version.ExerciseID}, version.Variants, u.approvals(ctx, []uuid.UUID{version.ExerciseID})[version.ExerciseID])
	view.Resources, view.ResourceHeavy = task.Range, task.Heavy
	if planner, ok := u.infra.(resourcePlanner); ok {
		need := infraModel.PlacementNeed{Device: task.deviceMax, LabDevices: task.labDevices}
		view.NoAgentFits = need.Known() && planner.NeedFit(need) != nil
	}
}

// decorateExerciseResources fills the resources of the attachments of a list (one query for all versions).
func (u *EventUseCase) decorateExerciseResources(ctx context.Context, items []EventExerciseView) error {
	versions := make(map[uuid.UUID]uuid.UUID, len(items))
	for _, item := range items {
		versions[item.ExerciseVersionID] = item.ExerciseID
	}
	res, err := u.versionsResources(ctx, versions)
	if err != nil {
		return err
	}
	for i := range items {
		r := res[items[i].ExerciseVersionID]
		items[i].Resources, items[i].ResourceHeavy, items[i].NoAgentFits = r.Range, r.Heavy, r.NoAgentFits
	}
	return nil
}

// decorateCatalogResources fills the resources of the published versions of the attachable exercises.
func (u *EventUseCase) decorateCatalogResources(ctx context.Context, items []EventCatalogItem) error {
	versions := make(map[uuid.UUID]uuid.UUID, len(items))
	for _, item := range items {
		versions[item.PublishedVersionID] = item.ID
	}
	res, err := u.versionsResources(ctx, versions)
	if err != nil {
		return err
	}
	for i := range items {
		r := res[items[i].PublishedVersionID]
		items[i].Resources, items[i].ResourceHeavy = r.Range, r.Heavy
	}
	return nil
}
