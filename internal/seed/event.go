package seed

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

var pointsByDifficulty = map[exerciseModel.Difficulty]int32{
	exerciseModel.DifficultyElementary: 50,
	exerciseModel.DifficultyTrivial:    100,
	exerciseModel.DifficultyEasy:       200,
	exerciseModel.DifficultyMedium:     300,
	exerciseModel.DifficultyHard:       500,
	exerciseModel.DifficultyInsane:     800,
}

var hintCostByLevel = map[exerciseModel.HintLevel]int32{
	exerciseModel.HintLevelNudge:        5,
	exerciseModel.HintLevelDirection:    10,
	exerciseModel.HintLevelSteps:        20,
	exerciseModel.HintLevelNearSolution: 40,
}

// seedEvent creates or updates the event: config, schedule, organizers, the board built from the
// published exercises, then registration and teams.
func (s *Seeder) seedEvent(ctx context.Context, opts Options, report Report, organizers []userModel.User, members [][]userModel.User, exercises []publishedExercise) (Report, error) {
	owner, manager := organizers[0], organizers[1]
	now := time.Now()

	event, err := s.events.GetLiveByTag(ctx, opts.EventTag, now)
	switch {
	case err == nil:
		if !strings.HasPrefix(event.InternalName, NamePrefix) {
			return report, fmt.Errorf("the event tag %q belongs to an event that is not seeded (%q); pick another with --event-tag", opts.EventTag, event.Name)
		}
		if event.InfrastructureAllowed != opts.Infrastructure {
			s.logf("event keeps its infrastructure setting (%t): it cannot change after creation", event.InfrastructureAllowed)
		}
	case repositoryTools.IsObjectNotFoundError(err):
		allowed := opts.Infrastructure
		created, createErr := s.eventUC.CreateEvent(ctx, eventUseCase.CreateEventInput{
			Tag: opts.EventTag, Name: NamePrefix + "Live Check", AvailableFrom: now.Add(-time.Minute), CreatedBy: owner.ID, InfrastructureAllowed: &allowed,
		})
		if createErr != nil {
			return report, fmt.Errorf("create event: %w", createErr)
		}
		report.EventCreated = true
		if event, err = s.events.GetByID(ctx, created.ID); err != nil {
			return report, fmt.Errorf("get event: %w", err)
		}
	default:
		return report, fmt.Errorf("find event %q: %w", opts.EventTag, err)
	}
	report.EventID, report.EventTag = event.ID, event.Tag
	s.logf("event %s (%s)", event.Tag, map[bool]string{true: "created", false: "kept"}[report.EventCreated])

	lifecycle, err := s.eventUC.GetEventLifecycle(ctx, event.ID)
	if err != nil {
		return report, fmt.Errorf("get lifecycle: %w", err)
	}
	published := lifecycle.Configured && lifecycle.Status != eventModel.LifecycleNotPublished
	started := lifecycle.Configured && lifecycle.Status >= eventModel.LifecycleStarted

	if err = s.configureEvent(ctx, event.ID, opts, owner.ID, published, started); err != nil {
		return report, err
	}
	if started {
		s.logf("event already started: schedule untouched")
		report.StartAt = lifecycle.StartAt
	} else {
		if report.StartAt, err = s.scheduleEvent(ctx, event.ID, opts, owner.ID, lifecycle, now); err != nil {
			return report, err
		}
		s.logf("event starts at %s", report.StartAt.Format(time.RFC3339))
	}

	if _, err = s.eventUC.SetEventManager(ctx, event.ID, eventUseCase.SetEventManagerInput{UserID: manager.ID, Role: int16(eventManagerModel.RoleManager)}); err != nil {
		return report, fmt.Errorf("set event manager: %w", err)
	}
	if err = s.buildBoard(ctx, event.ID, owner.ID, exercises); err != nil {
		return report, err
	}
	if report.Teams, report.TeamsCreated, err = s.seedTeams(ctx, event.ID, members); err != nil {
		return report, err
	}
	// The teams were written without a laboratory port, so ask for their lab access once here.
	if opts.Infrastructure {
		if err = s.eventUC.RequestEventLabAccessSyncs(ctx, event.ID); err != nil {
			return report, fmt.Errorf("request lab access: %w", err)
		}
	}
	return report, nil
}

// configureEvent sets team participation, open registration, public boards and the reveal mode;
// what a published or started event locks is left as it is.
func (s *Seeder) configureEvent(ctx context.Context, eventID uuid.UUID, opts Options, by uuid.UUID, published, started bool) error {
	current, err := s.eventUC.GetEventConfig(ctx, eventID)
	if err != nil {
		return fmt.Errorf("get event config: %w", err)
	}
	maxTeamSize := int32(opts.Profile.TeamSize)
	if published {
		maxTeamSize = current.MaxTeamSize
		if maxTeamSize < int32(opts.Profile.TeamSize) {
			return fmt.Errorf("the event is published with a team limit of %d, the profile needs %d", maxTeamSize, opts.Profile.TeamSize)
		}
	}
	in := eventUseCase.UpdateConfigInput{
		Registration:           eventConfigModel.RegistrationOpen,
		ScoreboardVisibility:   eventConfigModel.VisibilityPublic,
		ParticipantsVisibility: eventConfigModel.VisibilityPublic,
		PreviewDescription:     "Seeded event for live end-to-end checks.",
		PreviewPicture:         current.PreviewPicture,
		MaxTeamSize:            maxTeamSize,
		MinTeamSize:            current.MinTeamSize,
		MaxTeams:               current.MaxTeams,
	}
	if current.Participation == nil || *current.Participation != eventConfigModel.ParticipationTeam {
		if published {
			return fmt.Errorf("the event is published with another participation type, which can no longer change")
		}
		team := eventConfigModel.ParticipationTeam
		in.Participation = &team
	}
	if current.TaskRevealMode != opts.RevealMode {
		if started {
			s.logf("event started: the task reveal mode stays %s", current.TaskRevealMode)
		} else {
			mode := opts.RevealMode
			in.TaskRevealMode = &mode
		}
	}
	if _, err = s.eventUC.UpdateEventConfig(ctx, eventID, in, by); err != nil {
		return fmt.Errorf("update event config: %w", err)
	}
	return nil
}

// scheduleEvent publishes the event now (kept when it already is) and starts it StartIn from now.
func (s *Seeder) scheduleEvent(ctx context.Context, eventID uuid.UUID, opts Options, by uuid.UUID, current eventUseCase.EventLifecycleView, now time.Time) (time.Time, error) {
	publishAt := now
	if current.Configured && !now.Before(current.PublishAt) {
		publishAt = current.PublishAt
	}
	start := now.Add(opts.StartIn)
	finish := start.Add(opts.Duration)
	withdraw := finish.Add(24 * time.Hour)
	if _, err := s.eventUC.UpdateEventLifecycle(ctx, eventID, eventUseCase.UpdateLifecycleInput{
		JoinPolicy: eventModel.JoinPolicyLockedAtStart, PublishAt: publishAt, StartAt: start, FinishAt: &finish, WithdrawAt: &withdraw,
	}, by); err != nil {
		return time.Time{}, fmt.Errorf("update event schedule: %w", err)
	}
	return start, nil
}

// buildBoard attaches every published exercise, then sets the groups, points, hint costs and
// prerequisites of its tasks and shows the set on the board.
func (s *Seeder) buildBoard(ctx context.Context, eventID, by uuid.UUID, exercises []publishedExercise) error {
	groups, err := s.eventUC.ListChallengeGroups(ctx, eventID)
	if err != nil {
		return fmt.Errorf("list challenge groups: %w", err)
	}
	groupIDs := make(map[string]uuid.UUID, len(groups))
	for _, group := range groups {
		groupIDs[group.Name] = group.ID
	}
	for _, item := range exercises {
		if _, found := groupIDs[item.Spec.Group]; found {
			continue
		}
		group, createErr := s.eventUC.CreateChallengeGroup(ctx, eventID, eventUseCase.CreateChallengeGroupInput{Name: item.Spec.Group, Order: int32(len(groupIDs) + 1)})
		if createErr != nil {
			return fmt.Errorf("create challenge group %q: %w", item.Spec.Group, createErr)
		}
		groupIDs[group.Name] = group.ID
	}

	attached, err := s.eventUC.ListEventExercises(ctx, eventID)
	if err != nil {
		return fmt.Errorf("list event exercises: %w", err)
	}
	active := make(map[uuid.UUID]eventUseCase.EventExerciseView, len(attached))
	for _, link := range attached {
		if link.Status == eventExerciseModel.StatusActive && link.SupersededAt == nil && link.DetachedAt == nil {
			active[link.ExerciseID] = link
		}
	}

	for _, item := range exercises {
		link, found := active[item.ExerciseID]
		switch {
		case !found:
			if link, err = s.eventUC.AttachExercise(ctx, eventID, eventUseCase.AttachExerciseInput{ExerciseVersionID: item.VersionID, VariantMode: eventExerciseModel.VariantModePerTeam}, by); err != nil {
				return fmt.Errorf("attach %q: %w", item.Spec.Name, err)
			}
		case link.ExerciseVersionID != item.VersionID:
			if link, err = s.eventUC.UpdateEventExercise(ctx, eventID, link.ID, nil, by, false); err != nil {
				return fmt.Errorf("update attachment of %q: %w", item.Spec.Name, err)
			}
		}
		if err = s.configureChallenges(ctx, eventID, link.ID, item, groupIDs[item.Spec.Group]); err != nil {
			return err
		}
		if err = s.eventUC.SetEventExerciseVisibility(ctx, eventID, link.ID, true); err != nil {
			return fmt.Errorf("publish %q on the board: %w", item.Spec.Name, err)
		}
	}
	s.logf("board: %d exercises attached", len(exercises))
	return nil
}

func (s *Seeder) configureChallenges(ctx context.Context, eventID, linkID uuid.UUID, item publishedExercise, groupID uuid.UUID) error {
	board, err := s.eventUC.ListEventChallenges(ctx, eventID, linkID)
	if err != nil {
		return fmt.Errorf("list challenges of %q: %w", item.Spec.Name, err)
	}
	tasks := make(map[uuid.UUID]taskSpec, len(item.Spec.Variants[0].Tasks))
	for _, task := range item.Spec.Variants[0].Tasks {
		tasks[seedID(item.Spec.Key, "task", task.Key)] = task
	}
	var previous uuid.UUID
	for _, challenge := range board {
		task, known := tasks[challenge.TaskID]
		if !known {
			continue
		}
		if _, err = s.eventUC.UpdateEventChallenge(ctx, eventID, linkID, challenge.ID, eventUseCase.UpdateEventChallengeInput{Points: pointsByDifficulty[task.Difficulty], HintsEnabled: true}); err != nil {
			return fmt.Errorf("set points of %q: %w", task.Name, err)
		}
		costs := make([]eventUseCase.HintCostInput, 0, len(challenge.Hints))
		for _, hint := range challenge.Hints {
			cost := hintCostByLevel[exerciseModel.HintLevel(hint.Level)]
			costs = append(costs, eventUseCase.HintCostInput{HintID: hint.ID, Cost: &cost})
		}
		if len(costs) > 0 {
			if _, err = s.eventUC.UpdateChallengeHintCosts(ctx, eventID, linkID, challenge.ID, costs); err != nil {
				return fmt.Errorf("set hint costs of %q: %w", task.Name, err)
			}
		}
		relations := eventUseCase.UpdateEventChallengeRelationsInput{GroupID: &groupID, PrerequisiteIDs: []uuid.UUID{}}
		if item.Spec.Chain && previous != uuid.Nil {
			relations.PrerequisiteIDs = []uuid.UUID{previous}
		}
		if err = s.eventUC.UpdateEventChallengeRelations(ctx, eventID, linkID, challenge.ID, relations); err != nil {
			return fmt.Errorf("set relations of %q: %w", task.Name, err)
		}
		previous = challenge.ID
	}
	return nil
}

// seedTeams registers every member and builds the teams: the first member captains, the rest are
// assigned by the organizer. Existing teams and memberships are left as they are.
func (s *Seeder) seedTeams(ctx context.Context, eventID uuid.UUID, members [][]userModel.User) (total, created int, err error) {
	for index, people := range members {
		name := teamName(index + 1)
		for _, person := range people {
			if _, getErr := s.participants.Get(ctx, eventID, person.ID); getErr == nil {
				continue
			} else if !repositoryTools.IsObjectNotFoundError(getErr) {
				return total, created, fmt.Errorf("get participant %s: %w", person.Email, getErr)
			}
			if _, err = s.eventUC.JoinEvent(ctx, eventID, person.ID); err != nil {
				return total, created, fmt.Errorf("join %s: %w", person.Email, err)
			}
		}

		var teamID uuid.UUID
		team, getErr := s.teams.GetByName(ctx, eventID, name)
		switch {
		case getErr == nil:
			teamID = team.ID
		case repositoryTools.IsObjectNotFoundError(getErr):
			view, createErr := s.eventUC.CreateManagedTeam(ctx, eventID, eventUseCase.CreateManagedTeamInput{Name: name, CaptainID: people[0].ID})
			if createErr != nil {
				return total, created, fmt.Errorf("create team %q: %w", name, createErr)
			}
			teamID = view.ID
			created++
		default:
			return total, created, fmt.Errorf("get team %q: %w", name, getErr)
		}
		total++

		for _, person := range people[1:] {
			participant, getErr := s.participants.Get(ctx, eventID, person.ID)
			if getErr != nil {
				return total, created, fmt.Errorf("get participant %s: %w", person.Email, getErr)
			}
			if participant.TeamID != nil {
				if *participant.TeamID != teamID {
					return total, created, fmt.Errorf("%s is on another team than %q", person.Email, name)
				}
				continue
			}
			if err = s.eventUC.AssignParticipantToTeam(ctx, eventID, teamID, person.ID); err != nil {
				return total, created, fmt.Errorf("assign %s to %q: %w", person.Email, name, err)
			}
		}
	}
	s.logf("teams: %d (%d new)", total, created)
	return total, created, nil
}
