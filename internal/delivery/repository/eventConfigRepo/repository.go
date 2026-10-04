// Package eventConfigRepo is the repository for the 1:1 EventConfig
// aggregate: it accepts/returns whole domain entities and keeps all
// pgtype/sqlc mapping (nullable participation, timestamptz) out of the
// business layer.
package eventConfigRepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	CreateEventConfig(ctx context.Context, arg postgres.CreateEventConfigParams) (postgres.EventConfig, error)
	GetEventConfig(ctx context.Context, eventID uuid.UUID) (postgres.EventConfig, error)
	UpdateEventConfig(ctx context.Context, arg postgres.UpdateEventConfigParams) (int64, error)
	GetEventListColumns(ctx context.Context, arg postgres.GetEventListColumnsParams) (postgres.EventListColumn, error)
	UpsertEventListColumns(ctx context.Context, arg postgres.UpsertEventListColumnsParams) (postgres.EventListColumn, error)
}

// ListColumn is one extra-field column of a moderation list, in display order.
type ListColumn struct {
	Key     string `json:"key"`
	Visible bool   `json:"visible"`
}

// ListColumns is a deliberate narrow read/write next to the config aggregate:
// the shared column layout of the participants/teams lists is presentation
// state with no invariants beyond its own validation.
func (r *Repository) ListColumns(ctx context.Context, eventID uuid.UUID, list string) ([]ListColumn, error) {
	row, err := r.q.GetEventListColumns(ctx, postgres.GetEventListColumnsParams{EventID: eventID, List: list})
	if err != nil {
		return nil, err
	}
	var columns []ListColumn
	if err = json.Unmarshal(row.Columns, &columns); err != nil {
		return nil, err
	}
	return columns, nil
}

func (r *Repository) PutListColumns(ctx context.Context, eventID uuid.UUID, list string, columns []ListColumn, now time.Time, by uuid.UUID) error {
	encoded, err := json.Marshal(columns)
	if err != nil {
		return err
	}
	_, err = r.q.UpsertEventListColumns(ctx, postgres.UpsertEventListColumnsParams{
		EventID: eventID, List: list, Columns: encoded, UpdatedAt: now,
		UpdatedBy: uuid.NullUUID{UUID: by, Valid: by != uuid.Nil},
	})
	return err
}

type Repository struct {
	q Queries
}

func New(q Queries) *Repository {
	return &Repository{q: q}
}

func participationToDB(p *eventConfigModel.Participation) pgtype.Int2 {
	if p == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: int16(*p), Valid: true}
}

func participationFromDB(v pgtype.Int2) *eventConfigModel.Participation {
	if !v.Valid {
		return nil
	}
	p := eventConfigModel.Participation(v.Int16)
	return &p
}

func nullableInt32(value *int32) pgtype.Int4 {
	if value == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *value, Valid: true}
}

func int32FromDB(value pgtype.Int4) *int32 {
	if !value.Valid {
		return nil
	}
	copy := value.Int32
	return &copy
}

func (r *Repository) Create(ctx context.Context, c eventConfigModel.EventConfig) (eventConfigModel.EventConfig, error) {
	row, err := r.q.CreateEventConfig(ctx, postgres.CreateEventConfigParams{
		EventID:                   c.EventID,
		Participation:             participationToDB(c.Participation),
		Registration:              int16(c.Registration),
		ScoreboardVisibility:      int16(c.ScoreboardVisibility),
		ParticipantsVisibility:    int16(c.ParticipantsVisibility),
		PreviewDescription:        c.PreviewDescription,
		PreviewPicture:            c.PreviewPicture,
		CreatedAt:                 c.CreatedAt,
		UpdatedAt:                 pgtype.Timestamptz{Time: c.UpdatedAt, Valid: true},
		UpdatedBy:                 c.UpdatedBy,
		MaxTeamSize:               c.MaxTeamSize,
		MinTeamSize:               nullableInt32(c.MinTeamSize),
		MaxTeams:                  nullableInt32(c.MaxTeams),
		BrandColor:                c.Theme.Brand,
		AccentColor:               c.Theme.Accent,
		AccentLight:               c.Theme.AccentLight,
		AccentDark:                c.Theme.AccentDark,
		AccentLive:                c.Theme.AccentLive,
		ThemeVersion:              c.Theme.Version,
		AllowPseudonyms:           c.AllowPseudonyms,
		StandDeployLeadMinutes:    c.StandTiming.DeployLeadMinutes,
		StandTeardownDelayMinutes: c.StandTiming.TeardownDelayMinutes,
		ShowDifficulty:            c.ShowDifficulty,
		HintsDisabled:             c.HintsDisabled,
		HintChargeMode:            int16(c.HintChargeMode),
		ResultsFreezeEnabled:      c.Results.FreezeEnabled,
		ResultsFreezeMinutes:      c.Results.FreezeMinutes,
		ResultsOpenedAt:           nullableTime(c.Results.OpenedAt),
		ResultsLiveFreeze:         c.Results.LiveFreeze,
		ResultsChartEnabled:       c.Results.ChartEnabled,
		ResultsChartTeams:         c.Results.ChartTeams,
		ResultsRowsLimit:          nullableInt32(c.Results.RowsLimit),
		ShowStartCountdown:        c.Countdown.ShowStart,
		ShowFinishCountdown:       c.Countdown.ShowFinish,
		FinishCountdownMinutes:    c.Countdown.FinishMinutes,
		TaskRevealMode:            string(revealModeOrDefault(c.TaskRevealMode)),
		MaxFlagAttempts:           nullableInt32(c.MaxFlagAttempts),
	})
	if err != nil {
		return eventConfigModel.EventConfig{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) Get(ctx context.Context, eventID uuid.UUID) (eventConfigModel.EventConfig, error) {
	row, err := r.q.GetEventConfig(ctx, eventID)
	if err != nil {
		return eventConfigModel.EventConfig{}, err
	}
	return ToDomain(row), nil
}

// Update writes the mutable columns in one statement guarded by the
// optimistic lock (event_id/created_at are immutable and excluded).
func (r *Repository) Update(ctx context.Context, c eventConfigModel.EventConfig, expectedUpdatedAt time.Time) (int64, error) {
	return r.q.UpdateEventConfig(ctx, postgres.UpdateEventConfigParams{
		EventID:                   c.EventID,
		Participation:             participationToDB(c.Participation),
		Registration:              int16(c.Registration),
		ScoreboardVisibility:      int16(c.ScoreboardVisibility),
		ParticipantsVisibility:    int16(c.ParticipantsVisibility),
		PreviewDescription:        c.PreviewDescription,
		PreviewPicture:            c.PreviewPicture,
		UpdatedAt:                 pgtype.Timestamptz{Time: c.UpdatedAt, Valid: true},
		UpdatedBy:                 c.UpdatedBy,
		ExpectedUpdatedAt:         pgtype.Timestamptz{Time: expectedUpdatedAt, Valid: true},
		MaxTeamSize:               c.MaxTeamSize,
		MinTeamSize:               nullableInt32(c.MinTeamSize),
		MaxTeams:                  nullableInt32(c.MaxTeams),
		BrandColor:                c.Theme.Brand,
		AccentColor:               c.Theme.Accent,
		AccentLight:               c.Theme.AccentLight,
		AccentDark:                c.Theme.AccentDark,
		AccentLive:                c.Theme.AccentLive,
		ThemeVersion:              c.Theme.Version,
		AllowPseudonyms:           c.AllowPseudonyms,
		StandDeployLeadMinutes:    c.StandTiming.DeployLeadMinutes,
		StandTeardownDelayMinutes: c.StandTiming.TeardownDelayMinutes,
		ShowDifficulty:            c.ShowDifficulty,
		HintsDisabled:             c.HintsDisabled,
		HintChargeMode:            int16(c.HintChargeMode),
		ResultsFreezeEnabled:      c.Results.FreezeEnabled,
		ResultsFreezeMinutes:      c.Results.FreezeMinutes,
		ResultsOpenedAt:           nullableTime(c.Results.OpenedAt),
		ResultsLiveFreeze:         c.Results.LiveFreeze,
		ResultsChartEnabled:       c.Results.ChartEnabled,
		ResultsChartTeams:         c.Results.ChartTeams,
		ResultsRowsLimit:          nullableInt32(c.Results.RowsLimit),
		ShowStartCountdown:        c.Countdown.ShowStart,
		ShowFinishCountdown:       c.Countdown.ShowFinish,
		FinishCountdownMinutes:    c.Countdown.FinishMinutes,
		TaskRevealMode:            string(revealModeOrDefault(c.TaskRevealMode)),
		MaxFlagAttempts:           nullableInt32(c.MaxFlagAttempts),
	})
}

// ToDomain maps a sqlc row to the domain entity. participation is nullable at
// the column level (set-once) and forgiven as nil; updated_at is nullable at
// the column level (pgtype.Timestamptz) but the domain field is a plain
// time.Time — every row this repository ever reads was written by Create/
// Update above, which always set it valid.
func ToDomain(row postgres.EventConfig) eventConfigModel.EventConfig {
	maxTeamSize := row.MaxTeamSize
	// Generated test rows and pre-0022 fixtures omit the newly added DB field.
	// Treat zero as the migration's safe default rather than letting a legacy
	// config update erase team-capacity policy.
	if maxTeamSize == 0 {
		maxTeamSize = eventConfigModel.DefaultMaxTeamSize()
	}
	theme := eventConfigModel.Theme{
		Brand: row.BrandColor, Accent: row.AccentColor,
		AccentLight: row.AccentLight, AccentDark: row.AccentDark,
		AccentLive: row.AccentLive, Version: row.ThemeVersion,
	}
	if theme.Brand == "" || theme.Version == 0 {
		// Older unit fixtures omit columns added by the theme migration.
		theme = eventConfigModel.DefaultTheme()
	}
	return eventConfigModel.EventConfig{
		EventID:                row.EventID,
		Participation:          participationFromDB(row.Participation),
		Registration:           eventConfigModel.Registration(row.Registration),
		ScoreboardVisibility:   eventConfigModel.Visibility(row.ScoreboardVisibility),
		ParticipantsVisibility: eventConfigModel.Visibility(row.ParticipantsVisibility),
		PreviewDescription:     row.PreviewDescription,
		PreviewPicture:         row.PreviewPicture,
		MaxTeamSize:            maxTeamSize,
		MinTeamSize:            int32FromDB(row.MinTeamSize),
		MaxTeams:               int32FromDB(row.MaxTeams),
		Theme:                  theme,
		AllowPseudonyms:        row.AllowPseudonyms,
		ShowDifficulty:         row.ShowDifficulty,
		HintsDisabled:          row.HintsDisabled,
		HintChargeMode:         eventConfigModel.HintChargeMode(row.HintChargeMode),
		MaxFlagAttempts:        int32FromDB(row.MaxFlagAttempts),
		StandTiming:            standTimingFromDB(row.StandDeployLeadMinutes, row.StandTeardownDelayMinutes),
		Results:                resultsFromDB(row),
		Countdown:              countdownFromDB(row),
		TaskRevealMode:         revealModeOrDefault(eventConfigModel.TaskRevealMode(row.TaskRevealMode)),
		CreatedAt:              row.CreatedAt,
		UpdatedAt:              row.UpdatedAt.Time,
		UpdatedBy:              row.UpdatedBy,
	}
}

// revealModeOrDefault forgives zero-valued rows and entities (generated test rows, configs built
// before the setting existed) with the default mode.
func revealModeOrDefault(m eventConfigModel.TaskRevealMode) eventConfigModel.TaskRevealMode {
	if m == "" {
		return eventConfigModel.RevealAllReady
	}
	return m
}

// standTimingFromDB forgives zero-valued generated test rows by using the
// migration defaults; real rows always carry the checked columns.
func standTimingFromDB(lead, delay int32) eventStandModel.Timing {
	if lead == 0 {
		return eventStandModel.DefaultTiming()
	}
	return eventStandModel.Timing{DeployLeadMinutes: lead, TeardownDelayMinutes: delay}
}

func nullableTime(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}

// resultsFromDB forgives zero-valued generated test rows (minutes and chart
// teams are checked to be positive in real rows) with the migration defaults.
func resultsFromDB(row postgres.EventConfig) eventConfigModel.ResultsSettings {
	defaults := eventConfigModel.DefaultResultsSettings()
	if row.ResultsFreezeMinutes == 0 || row.ResultsChartTeams == 0 {
		return defaults
	}
	out := eventConfigModel.ResultsSettings{
		FreezeEnabled: row.ResultsFreezeEnabled, FreezeMinutes: row.ResultsFreezeMinutes, LiveFreeze: row.ResultsLiveFreeze,
		ChartEnabled: row.ResultsChartEnabled, ChartTeams: row.ResultsChartTeams, RowsLimit: int32FromDB(row.ResultsRowsLimit),
	}
	if row.ResultsOpenedAt.Valid {
		at := row.ResultsOpenedAt.Time
		out.OpenedAt = &at
	}
	return out
}

// countdownFromDB forgives zero-valued generated test rows (minutes are
// checked to be positive in real rows) with the migration defaults.
func countdownFromDB(row postgres.EventConfig) eventConfigModel.CountdownSettings {
	if row.FinishCountdownMinutes == 0 {
		return eventConfigModel.DefaultCountdownSettings()
	}
	return eventConfigModel.CountdownSettings{ShowStart: row.ShowStartCountdown, ShowFinish: row.ShowFinishCountdown, FinishMinutes: row.FinishCountdownMinutes}
}
