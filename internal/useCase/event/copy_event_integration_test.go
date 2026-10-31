package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

// An event created as a copy gets the settings, pages, managers, schedule and task sets of the source (unpublished,
// with groups, order, points and prerequisites), and nothing about people, teams, stands or results.
func TestCreateEventAsACopyBringsTheContentAndNoPeople(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	set := f.attachSharedSet(t)
	f.pass(t)
	_, err := f.db.Pool.Exec(
		ctx,
		`UPDATE events SET available_from = publish_at - interval '1 hour', landing_document = '{"blocks":[1]}'::jsonb WHERE id = $1`,
		f.eventID,
	)
	require.NoError(t, err)
	_, err = f.db.Pool.Exec(
		ctx,
		`UPDATE event_configs SET max_team_size = 3, hints_disabled = true WHERE event_id = $1`,
		f.eventID,
	)
	require.NoError(t, err)
	_, err = f.db.Pool.Exec(
		ctx,
		`INSERT INTO event_pages (id, event_id, slug, title, document, visibility, navigation, navigation_order, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, 'rules', 'Rules', '{"blocks":[]}'::jsonb, 0, 0, 0, now(), now())`,
		f.eventID,
	)
	require.NoError(t, err)
	group := uuid.Must(uuid.NewV7())
	_, err = f.db.Pool.Exec(
		ctx,
		`INSERT INTO event_challenge_groups (id, event_id, name, order_index, created_at) VALUES ($1, $2, 'Web', 0, now())`,
		group,
		f.eventID,
	)
	require.NoError(t, err)
	_, err = f.db.Pool.Exec(
		ctx,
		`UPDATE event_challenges SET group_id = $2, points = 250 WHERE id = $1`,
		set.challenges[0],
		group,
	)
	require.NoError(t, err)
	_, err = f.db.Pool.Exec(
		ctx,
		`INSERT INTO event_challenge_prerequisites (challenge_id, prerequisite_challenge_id) VALUES ($1, $2)`,
		set.challenges[1],
		set.challenges[0],
	)
	require.NoError(t, err)
	helper, err := userRepo.New(f.db.Queries).
		Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "helper@copy.test", time.Now()))
	require.NoError(t, err)
	_, err = f.db.Pool.Exec(
		ctx,
		`INSERT INTO event_managers (event_id, user_id, role, created_at) VALUES ($1, $2, 1, now())`,
		f.eventID,
		helper.ID,
	)
	require.NoError(t, err)
	published := f.count(
		t,
		`SELECT count(*) FROM event_challenges ec JOIN event_exercises ee ON ee.id = ec.event_exercise_id WHERE ee.event_id = $1 AND ec.published`,
		f.eventID,
	)
	require.NotZero(t, published, "the source has published tasks")

	admin := uuid.Must(uuid.NewV7())
	_, err = userRepo.New(f.db.Queries).
		Create(ctx, userModel.NewIncompleteUser(admin, "admin@copy.test", time.Now()))
	require.NoError(t, err)
	infra := true
	view, err := f.uc.CreateEvent(ctx, event.CreateEventInput{
		Tag:                   "copy",
		Name:                  "Copy",
		AvailableFrom:         time.Now(),
		CreatedBy:             admin,
		InfrastructureAllowed: &infra,
		CopyFromEventID:       f.eventID,
	})
	require.NoError(t, err)
	copyID := view.ID

	require.Equal(
		t,
		3,
		f.count(t, `SELECT max_team_size FROM event_configs WHERE event_id = $1`, copyID),
		"settings come along",
	)
	require.Equal(
		t,
		1,
		f.count(
			t,
			`SELECT count(*) FROM event_configs WHERE event_id = $1 AND hints_disabled`,
			copyID,
		),
	)
	require.Equal(
		t,
		1,
		f.count(
			t,
			`SELECT count(*) FROM event_pages WHERE event_id = $1 AND slug = 'rules'`,
			copyID,
		),
		"pages come along",
	)
	require.Equal(
		t,
		1,
		f.count(
			t,
			`SELECT count(*) FROM events WHERE id = $1 AND landing_document = '{"blocks":[1]}'::jsonb`,
			copyID,
		),
	)
	require.Equal(
		t,
		1,
		f.count(t, `SELECT count(*) FROM events WHERE id = $1 AND lifecycle_configured`, copyID),
		"the schedule is copied",
	)
	require.Equal(
		t,
		1,
		f.count(
			t,
			`SELECT count(*) FROM event_managers WHERE event_id = $1 AND user_id = $2 AND role = 1`,
			copyID,
			helper.ID,
		),
		"organizers come along",
	)
	require.Equal(
		t,
		1,
		f.count(
			t,
			`SELECT count(*) FROM event_managers WHERE event_id = $1 AND user_id = $2 AND role = 0`,
			copyID,
			admin,
		),
		"the creator owns the copy",
	)

	sets := f.count(
		t,
		`SELECT count(*) FROM event_exercises WHERE event_id = $1 AND status = 0`,
		f.eventID,
	)
	require.Equal(
		t,
		sets,
		f.count(
			t,
			`SELECT count(*) FROM event_exercises WHERE event_id = $1 AND status = 0`,
			copyID,
		),
	)
	require.Equal(
		t,
		0,
		f.count(
			t,
			`SELECT count(*) FROM event_challenges ec JOIN event_exercises ee ON ee.id = ec.event_exercise_id WHERE ee.event_id = $1 AND ec.published`,
			copyID,
		),
		"not published",
	)
	require.Equal(
		t,
		f.count(
			t,
			`SELECT count(*) FROM event_challenges ec JOIN event_exercises ee ON ee.id = ec.event_exercise_id WHERE ee.event_id = $1`,
			f.eventID,
		),
		f.count(
			t,
			`SELECT count(*) FROM event_challenges ec JOIN event_exercises ee ON ee.id = ec.event_exercise_id WHERE ee.event_id = $1`,
			copyID,
		),
	)
	require.Equal(
		t,
		1,
		f.count(
			t,
			`SELECT count(*) FROM event_challenge_groups WHERE event_id = $1 AND name = 'Web'`,
			copyID,
		),
	)
	require.Equal(
		t,
		1,
		f.count(
			t,
			`SELECT count(*) FROM event_challenges ec JOIN event_exercises ee ON ee.id = ec.event_exercise_id
		JOIN event_challenge_groups g ON g.id = ec.group_id WHERE ee.event_id = $1 AND ec.points = 250 AND g.event_id = $1`,
			copyID,
		),
		"the task is in the copied group, not the source's",
	)
	require.Equal(
		t,
		1,
		f.count(
			t,
			`SELECT count(*) FROM event_challenge_prerequisites p JOIN event_challenges c ON c.id = p.challenge_id
		JOIN event_exercises ee ON ee.id = c.event_exercise_id WHERE ee.event_id = $1`,
			copyID,
		),
		"prerequisites are remapped",
	)

	for table, column := range map[string]string{"event_participants": "event_id", "event_team_stands": "event_id", "team_challenges": "event_id", "challenge_attempts": "event_id"} {
		require.Zero(
			t,
			f.count(t, `SELECT count(*) FROM `+table+` WHERE `+column+` = $1`, copyID),
			table,
		)
	}
	require.Zero(
		t,
		f.count(
			t,
			`SELECT count(*) FROM event_teams WHERE event_id = $1 AND NOT moderators`,
			copyID,
		),
		"no teams",
	)
	require.NotZero(
		t,
		f.count(t, `SELECT count(*) FROM event_teams WHERE event_id = $1`, f.eventID),
		"the source keeps its teams",
	)
}

func TestCreateEventAsACopyOfAMissingEventFails(t *testing.T) {
	f := newStandFixture(t)
	_, err := f.uc.CreateEvent(context.Background(), event.CreateEventInput{
		Tag:             "ghost",
		Name:            "Ghost",
		AvailableFrom:   time.Now(),
		CreatedBy:       f.ownerID,
		CopyFromEventID: uuid.Must(uuid.NewV7()),
	})
	require.Error(t, err)
}
