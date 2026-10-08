// lifecyclefixture exports actual immutable Lab/objective pins for END browser
// work without exporting credentials, expected flags, topology or variable data.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type inputTeam struct {
	TeamID                      uuid.UUID
	SessionHandle               string
	SharedLabID, UnrelatedLabID uuid.UUID
}
type config struct {
	DSNFile                                        string
	EventID                                        uuid.UUID
	EventTag, APIURL, EventURL, AdminSessionHandle string
	Teams                                          []inputTeam
}
type labPin struct {
	ID                uuid.UUID
	Revision          string
	EventExerciseID   uuid.UUID
	EventChallengeIDs []uuid.UUID
}
type teamPin struct {
	TeamID                  uuid.UUID
	SessionHandle, URL      string
	SharedLab, UnrelatedLab labPin
}
type fixture struct {
	EventID                              uuid.UUID
	EventTag                             string
	Teams                                []teamPin
	AdminSessionHandle, APIURL, EventURL string
}

func private(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("private fixture file must exist with mode0600")
	}
	return os.ReadFile(path)
}
func run() error {
	source := flag.String("config", "", "mode0600 config with actual IDs and opaque session handles")
	output := flag.String("output", "", "new JSON outside tracked repositories")
	flag.Parse()
	raw, err := private(*source)
	if err != nil {
		return err
	}
	var cfg config
	if json.Unmarshal(raw, &cfg) != nil || cfg.EventID == uuid.Nil || len(cfg.Teams) != 2 || cfg.AdminSessionHandle == "" {
		return fmt.Errorf("actual event/two-team/admin fixture identity is incomplete")
	}
	if !filepath.IsAbs(*output) {
		return fmt.Errorf("output must be an absolute owned path")
	}
	dsn, err := private(cfg.DSNFile)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, strings.TrimSpace(string(dsn)))
	if err != nil {
		return fmt.Errorf("fixture database connection failed")
	}
	defer pool.Close()
	repo := eventLabRepo.New(postgres.New(pool))
	pin := func(id, team uuid.UUID) (labPin, error) {
		lab, err := repo.Get(ctx, id)
		if err != nil || lab.EventID != cfg.EventID || lab.TeamID != team {
			return labPin{}, fmt.Errorf("actual Lab pin does not belong to requested event/team")
		}
		out := labPin{ID: lab.ID, Revision: fmt.Sprint(lab.Revision), EventExerciseID: lab.EventExerciseID}
		rows, err := pool.Query(ctx, `SELECT event_challenge_id FROM event_lab_objectives WHERE lab_id=$1 ORDER BY event_challenge_id`, id)
		if err != nil {
			return labPin{}, fmt.Errorf("objective pins unavailable")
		}
		defer rows.Close()
		for rows.Next() {
			var question uuid.UUID
			if rows.Scan(&question) != nil {
				return labPin{}, fmt.Errorf("objective pin invalid")
			}
			out.EventChallengeIDs = append(out.EventChallengeIDs, question)
		}
		if rows.Err() != nil {
			return labPin{}, fmt.Errorf("objective pins failed")
		}
		return out, nil
	}
	out := fixture{EventID: cfg.EventID, EventTag: cfg.EventTag, AdminSessionHandle: cfg.AdminSessionHandle, APIURL: cfg.APIURL, EventURL: cfg.EventURL}
	for _, team := range cfg.Teams {
		if team.TeamID == uuid.Nil || team.SessionHandle == "" || team.SharedLabID == team.UnrelatedLabID {
			return fmt.Errorf("team fixture identity incomplete")
		}
		shared, err := pin(team.SharedLabID, team.TeamID)
		if err != nil {
			return err
		}
		unrelated, err := pin(team.UnrelatedLabID, team.TeamID)
		if err != nil {
			return err
		}
		if len(shared.EventChallengeIDs) != 3 || len(unrelated.EventChallengeIDs) == 0 {
			return fmt.Errorf("fixture must contain three pinned shared questions and unrelated Lab")
		}
		out.Teams = append(out.Teams, teamPin{TeamID: team.TeamID, SessionHandle: team.SessionHandle, URL: cfg.EventURL, SharedLab: shared, UnrelatedLab: unrelated})
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("fixture export failed")
	}
	file, err := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("fixture output must be new and owned")
	}
	defer file.Close()
	if _, err = file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("fixture write failed")
	}
	if file.Sync() != nil {
		return fmt.Errorf("fixture flush failed")
	}
	fmt.Println("actual two-team browser fixture pins written; no credentials or flags exported")
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
