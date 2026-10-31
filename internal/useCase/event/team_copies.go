package event

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventChallengeRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventExerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

// What a team copy can differ from the task in: the keys of the pinned snapshot, and "hints".
const (
	CopyDiffName         = "name"
	CopyDiffDescription  = "description"
	CopyDiffDifficulty   = "difficulty"
	CopyDiffAttachments  = "attachments"
	CopyDiffPlaceholders = "placeholders"
	CopyDiffHints        = "hints"
)

// TeamCopyDiffView is one team copy that differs from the task of the set's pinned version.
type TeamCopyDiffView struct {
	TeamID        uuid.UUID
	TeamName      string
	ChallengeID   uuid.UUID
	ChallengeName string
	// Differs lists what differs (CopyDiff*).
	Differs []string
	// Solved copies keep their pinned content and are never synced.
	Solved bool
}

// TeamCopiesView tells how many team copies of a set differ from its task now.
type TeamCopiesView struct {
	// Total counts the team copies of the set, Outdated those that differ.
	Total    int
	Outdated int
	Items    []TeamCopyDiffView
}

// GetTeamCopies compares every team copy of the set with the task of the version the set is pinned to, for the
// variant the team plays. A copy differs when the task changed after the copy was issued.
func (u *EventUseCase) GetTeamCopies(ctx context.Context, eventID, eventExerciseID uuid.UUID) (TeamCopiesView, error) {
	link, err := u.activeAttachment(ctx, eventID, eventExerciseID)
	if err != nil {
		return TeamCopiesView{}, err
	}
	version, err := u.exercises.GetVersion(ctx, link.ExerciseVersionID)
	if err != nil {
		return TeamCopiesView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get pinned version").Err()
	}
	board, err := u.eventChallenges.List(ctx, link.ID)
	if err != nil {
		return TeamCopiesView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list event challenges").Err()
	}
	copies, err := u.teamChallenges.Copies(ctx, link.ID)
	if err != nil {
		return TeamCopiesView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list team copies").Err()
	}
	return teamCopyDiffs(version, board, copies), nil
}

func teamCopyDiffs(version exerciseModel.ExerciseVersion, board []eventChallengeModel.EventChallenge, copies []teamChallengeRepo.CopyRow) TeamCopiesView {
	challenges := make(map[uuid.UUID]eventChallengeModel.EventChallenge, len(board))
	for _, challenge := range board {
		challenges[challenge.ID] = challenge
	}
	view := TeamCopiesView{Items: []TeamCopyDiffView{}}
	for _, row := range copies {
		view.Total++
		challenge, known := challenges[row.EventChallengeID]
		if !known {
			continue
		}
		task, found := findTask(version, row.VariantIndex, challenge.TaskID)
		if !found {
			continue
		}
		differs := copyDiffers(task, row.Snapshot, row.Hints)
		if len(differs) == 0 {
			continue
		}
		view.Outdated++
		view.Items = append(view.Items, TeamCopyDiffView{TeamID: row.EventTeamID, TeamName: row.TeamName, ChallengeID: challenge.ID, ChallengeName: snapshotName(challenge.Snapshot), Differs: differs, Solved: row.Solved})
	}
	return view
}

// copyDiffers names what a team copy (snapshot and hints) has different from the task.
func copyDiffers(task exerciseModel.Task, snapshot json.RawMessage, hints []teamChallengeModel.Hint) []string {
	want, err := eventChallengeModel.SnapshotForTask(task)
	if err != nil {
		return nil
	}
	var wantFields, haveFields map[string]json.RawMessage
	if json.Unmarshal(want, &wantFields) != nil || json.Unmarshal(snapshot, &haveFields) != nil {
		return nil
	}
	var out []string
	for _, key := range []string{CopyDiffName, CopyDiffDescription, CopyDiffDifficulty, CopyDiffAttachments, CopyDiffPlaceholders} {
		if !jsonFieldEqual(wantFields[key], haveFields[key]) {
			out = append(out, key)
		}
	}
	if !slices.Equal(teamHints(task), hints) {
		out = append(out, CopyDiffHints)
	}
	return out
}

// jsonFieldEqual treats a missing field and null as the same.
func jsonFieldEqual(a, b json.RawMessage) bool {
	if jsonAbsent(a) {
		return jsonAbsent(b)
	}
	return jsonEqual(a, b)
}

// SyncTeamCopies brings the team copies of the set that differ from the task up to date: text, attachments and
// hints are refreshed from the pinned version and the board shows «Оновлено». The variant and the flag of a copy
// stay, and a solved copy keeps its pinned content. It returns what was updated.
func (u *EventUseCase) SyncTeamCopies(ctx context.Context, eventID, eventExerciseID uuid.UUID) (TeamCopiesView, error) {
	if u.uow == nil {
		return TeamCopiesView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return TeamCopiesView{}, err
	}
	defer unit.Restore()
	link, err := eventExerciseRepo.New(txRepo).GetByID(txCtx, eventID, eventExerciseID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return TeamCopiesView{}, eventExerciseModel.ErrEventExerciseNotFound.Err()
		}
		return TeamCopiesView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event exercise").Err()
	}
	if err = link.EnsureActive(); err != nil {
		return TeamCopiesView{}, err
	}
	version, err := exerciseRepo.New(txRepo).GetVersion(txCtx, link.ExerciseVersionID)
	if err != nil {
		return TeamCopiesView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get pinned version").Err()
	}
	board, err := eventChallengeRepo.New(txRepo).List(txCtx, link.ID)
	if err != nil {
		return TeamCopiesView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list event challenges").Err()
	}
	taskOf := make(map[uuid.UUID]uuid.UUID, len(board))
	ids := make([]uuid.UUID, 0, len(board))
	for _, challenge := range board {
		taskOf[challenge.ID] = challenge.TaskID
		ids = append(ids, challenge.ID)
	}
	teamChallenges := teamChallengeRepo.New(txRepo)
	rows, err := teamChallenges.ForRefresh(txCtx, ids)
	if err != nil {
		return TeamCopiesView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list team challenges").Err()
	}
	now := time.Now()
	updated := TeamCopiesView{Items: []TeamCopyDiffView{}}
	for _, row := range rows {
		updated.Total++
		task, found := findTask(version, row.VariantIndex, taskOf[row.EventChallengeID])
		if !found || row.Solved || len(copyDiffers(task, row.Snapshot, row.Hints)) == 0 {
			continue
		}
		snapshot, snapErr := eventChallengeModel.SnapshotForTask(task)
		if snapErr != nil {
			return TeamCopiesView{}, model.ErrPlatform.WithError(snapErr).WithMessage("Failed to snapshot team challenge").Err()
		}
		differs := copyDiffers(task, row.Snapshot, row.Hints)
		row.Snapshot, row.Hints = snapshot, teamHints(task)
		if _, err = teamChallenges.UpdateContent(txCtx, row, true, now); err != nil {
			return TeamCopiesView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to refresh team challenge").Err()
		}
		updated.Outdated++
		updated.Items = append(updated.Items, TeamCopyDiffView{TeamID: row.EventTeamID, ChallengeID: row.EventChallengeID, Differs: differs})
	}
	if err = unit.Save(); err != nil {
		return TeamCopiesView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to sync team copies").Err()
	}
	return updated, nil
}

// snapshotName reads the task name out of a snapshot.
func snapshotName(snapshot json.RawMessage) string {
	var value struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(snapshot, &value) != nil {
		return ""
	}
	return value.Name
}

func jsonAbsent(value json.RawMessage) bool { return len(value) == 0 || string(value) == jsonNull }

const jsonNull = "null"
