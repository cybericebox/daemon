package event

import (
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func challengeNamed(name string, task uuid.UUID) eventChallengeModel.EventChallenge {
	snapshot, _ := json.Marshal(map[string]string{"name": name})
	return eventChallengeModel.EventChallenge{ID: uuid.Must(uuid.NewV7()), TaskID: task, Snapshot: snapshot}
}

func TestMatchBoardTasks(t *testing.T) {
	a, b, c := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	board := []eventChallengeModel.EventChallenge{challengeNamed("Alpha", a), challengeNamed("Beta", b), challengeNamed("Gamma", c)}

	t.Run("same ids", func(t *testing.T) {
		m := matchBoardTasks(board, []exerciseModel.Task{{ID: a, Name: "Alpha"}, {ID: b, Name: "Beta"}, {ID: c, Name: "Gamma"}})
		require.Len(t, m.Tasks, 3)
		require.Empty(t, m.Repointed)
		require.Empty(t, m.Removed)
		require.Empty(t, m.Added)
	})
	t.Run("re-issued ids are matched by name", func(t *testing.T) {
		a2, b2, c2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
		m := matchBoardTasks(board, []exerciseModel.Task{{ID: c2, Name: " gamma "}, {ID: a2, Name: "ALPHA"}, {ID: b2, Name: "Beta"}})
		require.Len(t, m.Tasks, 3)
		require.Equal(t, a2, m.Tasks[board[0].ID].ID)
		require.Equal(t, a, m.Repointed[board[0].ID])
		require.Empty(t, m.Removed)
		require.Empty(t, m.Added)
	})
	t.Run("a renamed task is removed and a new one added", func(t *testing.T) {
		d := uuid.Must(uuid.NewV7())
		m := matchBoardTasks(board, []exerciseModel.Task{{ID: a, Name: "Alpha"}, {ID: b, Name: "Beta"}, {ID: d, Name: "Delta"}})
		require.Equal(t, []uuid.UUID{board[2].ID}, m.Removed)
		require.Len(t, m.Added, 1)
		require.Equal(t, d, m.Added[0].ID)
	})
	t.Run("two tasks of one name pair one to one", func(t *testing.T) {
		x, y := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
		x2, y2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
		twins := []eventChallengeModel.EventChallenge{challengeNamed("Twin", x), challengeNamed("Twin", y)}
		m := matchBoardTasks(twins, []exerciseModel.Task{{ID: x2, Name: "Twin"}, {ID: y2, Name: "Twin"}})
		require.Equal(t, x2, m.Tasks[twins[0].ID].ID)
		require.Equal(t, y2, m.Tasks[twins[1].ID].ID)
	})
}
