package event

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/cybericebox/daemon/internal/model/eventContent/testutil"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	"github.com/cybericebox/daemon/internal/model/rbac"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

func TestResolveContentVariablesBuildsTypedEventSettingsAndStatistics(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	publishAt := now.Add(-2 * time.Hour)
	startAt := now.Add(-time.Hour)
	finishAt := now.Add(time.Hour)
	withdrawAt := now.Add(2 * time.Hour)
	lifecycle, err := eventModel.NewLifecycle(eventModel.JoinPolicyRolling, publishAt, startAt, &finishAt, &withdrawAt, nil)
	if err != nil {
		t.Fatal(err)
	}
	participation := eventConfigModel.ParticipationTeam
	minTeamSize, maxTeams := int32(2), int32(120)

	values := resolveContentVariables(
		eventModel.Event{
			Name:              "Cyber Games",
			Tag:               "cybergames",
			Lifecycle:         lifecycle,
			ScoringProfile:    eventModel.ScoringProfile{Mode: eventModel.ScoringPopularityCurve},
			ForceEventScoring: true,
		},
		eventConfigModel.EventConfig{
			Participation:          &participation,
			Registration:           eventConfigModel.RegistrationApproval,
			ScoreboardVisibility:   eventConfigModel.VisibilityPublic,
			ParticipantsVisibility: eventConfigModel.VisibilityPrivate,
			PreviewDescription:     "CTF",
			MaxTeamSize:            5,
			MinTeamSize:            &minTeamSize,
			MaxTeams:               &maxTeams,
		},
		eventContentModel.Statistics{
			TeamCount:                14,
			ApprovedTeamCount:        12,
			ParticipantCount:         39,
			ApprovedParticipantCount: 30,
			ChallengeCount:           18,
			PublishedChallengeCount:  15,
			SolvedChallengeCount:     9,
			SolveCount:               43,
		},
		now,
	)

	for name, want := range map[string]any{
		"event.name":                     "Cyber Games",
		"event.phase":                    "started",
		"event.isPublished":              true,
		"event.isStarted":                true,
		"event.runtimeOpen":              true,
		"event.registrationOpen":         true,
		"event.participation":            "team",
		"event.registration":             "approval",
		"event.joinPolicy":               "rolling",
		"event.maxTeamSize":              int64(5),
		"event.minTeamSize":              int64(2),
		"event.maxTeams":                 int64(120),
		"event.scoreboardVisibility":     "public",
		"event.participantsVisibility":   "private",
		"event.scoringProfile":           "popularity_curve",
		"event.forceEventScoring":        true,
		"event.teamCount":                int64(14),
		"event.approvedTeamCount":        int64(12),
		"event.participantCount":         int64(39),
		"event.approvedParticipantCount": int64(30),
		"event.registrationUnitCount":    int64(12),
		"event.challengeCount":           int64(18),
		"event.availableChallengeCount":  int64(18),
		"event.solvedChallengeCount":     int64(9),
		"event.solveCount":               int64(43),
	} {
		if got := values[name]; got != want {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
	if got := values["event.effectiveFinishAt"]; got != finishAt {
		t.Errorf("event.effectiveFinishAt = %#v, want %#v", got, finishAt)
	}
}

func TestResolveContentVariablesRegistrationClosedBySettings(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	startAt := now.Add(time.Hour)
	lifecycle, err := eventModel.NewLifecycle(eventModel.JoinPolicyLockedAtStart, now.Add(-time.Hour), startAt, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	values := resolveContentVariables(
		eventModel.Event{Lifecycle: lifecycle},
		eventConfigModel.EventConfig{Registration: eventConfigModel.RegistrationClose},
		eventContentModel.Statistics{}, now,
	)
	if got := values["event.registrationOpen"]; got != false {
		t.Fatalf("registrationOpen = %v, want false when registration setting is closed", got)
	}
}

func TestEventPageErrorsAreMappedToDomainResponses(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := NewEventUseCase(Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	pageID := uuid.Must(uuid.NewV7())
	input := EventPageInput{Slug: "rules", Title: "Rules", Document: eventContentModel.Document{Blocks: []eventContentModel.Block{{ID: "intro", Type: eventContentModel.BlockText, RichText: testutil.RichText("Rules")}}}}
	q.EXPECT().GetEventPageBySlug(gomock.Any(), gomock.Any()).Return(postgres.EventPage{}, pgx.ErrNoRows)
	q.EXPECT().ListEventPages(gomock.Any(), eventID).Return(nil, nil).Times(2)
	if _, err := uc.GetEventPage(context.Background(), eventID, "missing"); !errors.Is(err, eventContentModel.ErrPageNotFound.Err()) {
		t.Fatalf("missing page error = %v", err)
	}
	q.EXPECT().CreateEventPage(gomock.Any(), gomock.Any()).Return(postgres.EventPage{}, &pgconn.PgError{Code: pgerrcode.UniqueViolation})
	if _, err := uc.CreateEventPage(context.Background(), eventID, input); !errors.Is(err, eventContentModel.ErrPageExists.Err()) {
		t.Fatalf("duplicate page error = %v", err)
	}
	q.EXPECT().DeleteEventPage(gomock.Any(), postgres.DeleteEventPageParams{EventID: eventID, ID: pageID}).Return(int64(0), nil)
	if err := uc.DeleteEventPage(context.Background(), eventID, pageID); !errors.Is(err, eventContentModel.ErrPageNotFound.Err()) {
		t.Fatalf("deleted page error = %v", err)
	}
}

func TestPublicContentVariablesOnlyIncludeUsedAllowedValues(t *testing.T) {
	document := eventContentModel.Document{Blocks: []eventContentModel.Block{{
		ID: "intro", Type: eventContentModel.BlockText, RichText: testutil.RichText("{{event.name}}"),
		Variables: []eventContentModel.VariableBinding{{Name: "event.name", Format: eventContentModel.VariableFormatText}},
	}}}
	filtered := projectContentVariables(document, map[string]any{
		"event.name":                     "Games",
		"event.publishAt":                time.Now(),
		"event.withdrawAt":               time.Now(),
		"event.teamCount":                int64(20),
		"event.participantCount":         int64(50),
		"event.challengeCount":           int64(10),
		"event.approvedTeamCount":        int64(18),
		"event.approvedParticipantCount": int64(42),
		"event.availableChallengeCount":  int64(0),
		"event.solvedChallengeCount":     int64(4),
		"event.solveCount":               int64(12),
		"event.scoreboardVisibility":     "hidden",
	}, eventContentModel.PageVisibilityPublic)
	for _, forbidden := range []string{"event.publishAt", "event.withdrawAt", "event.teamCount", "event.participantCount", "event.challengeCount", "event.approvedTeamCount", "event.solvedChallengeCount", "event.solveCount"} {
		if _, found := filtered[forbidden]; found {
			t.Fatalf("public content leaked %s", forbidden)
		}
	}
	for _, allowed := range []string{"event.name"} {
		if _, found := filtered[allowed]; !found {
			t.Fatalf("public content omitted %s", allowed)
		}
	}
}

func TestPublicContentVariablesIncludeJoinActionState(t *testing.T) {
	document := eventContentModel.Document{Blocks: []eventContentModel.Block{{
		ID: "join", Type: eventContentModel.BlockCTA,
		Action: &eventContentModel.BlockAction{Label: "Join", Kind: "join_event"},
	}}}
	values := map[string]any{"event.registrationOpen": true, "event.startAt": time.Now(), "event.effectiveFinishAt": time.Now().Add(time.Hour), "event.joinPolicy": "locked_at_start", "event.tag": "games", "event.teamCount": int64(4)}
	projected := projectContentVariables(document, values, eventContentModel.PageVisibilityPublic)
	if projected["event.registrationOpen"] != true || projected["event.startAt"] == nil || projected["event.effectiveFinishAt"] == nil || projected["event.joinPolicy"] != "locked_at_start" || projected["event.tag"] != "games" {
		t.Fatalf("join action state missing: %#v", projected)
	}
	if _, found := projected["event.teamCount"]; found {
		t.Fatalf("unrelated variable leaked: %#v", projected)
	}
}

func TestPublicContentVariablesIncludeTimeBoundariesForLifecycleConditions(t *testing.T) {
	document := eventContentModel.Document{Blocks: []eventContentModel.Block{{
		ID: "phase", Type: eventContentModel.BlockSection,
		Variables: []eventContentModel.VariableBinding{{Name: "event.isStarted", Format: eventContentModel.VariableFormatBoolean}},
	}}}
	start := time.Now()
	finish := start.Add(time.Hour)
	projected := projectContentVariables(document, map[string]any{"event.isStarted": false, "event.startAt": start, "event.effectiveFinishAt": finish, "event.joinPolicy": "locked_at_start", "event.teamCount": int64(4)}, eventContentModel.PageVisibilityPublic)
	if projected["event.startAt"] != start || projected["event.effectiveFinishAt"] != finish || projected["event.joinPolicy"] != "locked_at_start" {
		t.Fatalf("lifecycle boundaries missing: %#v", projected)
	}
	if _, found := projected["event.teamCount"]; found {
		t.Fatalf("unrelated variable leaked: %#v", projected)
	}
}

func TestAvailableChallengeCountIsPubliclyZeroUntilTasksAreAccessible(t *testing.T) {
	document := eventContentModel.Document{Blocks: []eventContentModel.Block{{
		ID: "count", Type: eventContentModel.BlockText, RichText: testutil.RichText("{{event.availableChallengeCount}}"),
		Variables: []eventContentModel.VariableBinding{{Name: "event.availableChallengeCount", Format: eventContentModel.VariableFormatNumber}},
	}}}
	values := map[string]any{"event.availableChallengeCount": int64(12)}
	projected := projectContentVariables(document, values, eventContentModel.PageVisibilityPublic)
	if projected["event.availableChallengeCount"] != int64(0) {
		t.Fatalf("public challenge count = %#v, want zero", projected["event.availableChallengeCount"])
	}
}

func TestParticipantPageCountsOnlyOwnAccessibleChallenges(t *testing.T) {
	for _, tc := range []struct {
		name        string
		startOffset time.Duration
		want        int64
	}{
		{name: "before start", startOffset: time.Hour, want: 0},
		{name: "after start", startOffset: -time.Hour, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := postgresMocks.NewMockQuerier(ctrl)
			uc := NewEventUseCase(Dependencies{Repo: q})
			eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			now := time.Now().UTC()
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{
				ID: eventID, Tag: "games", Name: "Games", JoinPolicy: int16(eventModel.JoinPolicyRolling), LifecycleConfigured: true,
				PublishAt: now.Add(-2 * time.Hour), StartAt: now.Add(tc.startOffset),
			}, nil)
			q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, MaxTeamSize: 5}, nil)
			q.EXPECT().CountEventTeams(gomock.Any(), eventID).Return(int64(2), nil)
			q.EXPECT().CountApprovedEventTeams(gomock.Any(), eventID).Return(int64(2), nil)
			q.EXPECT().CountEventParticipants(gomock.Any(), gomock.Any()).Return(int64(3), nil).Times(2)
			q.EXPECT().GetEventContentStatistics(gomock.Any(), eventID).Return(postgres.GetEventContentStatisticsRow{ChallengeCount: 12, PublishedChallengeCount: 10}, nil)
			q.EXPECT().GetEventContentSettings(gomock.Any(), eventID).Return(postgres.GetEventContentSettingsRow{
				ID: eventID, LandingDocument: []byte(`{"blocks":[]}`), LiveLayout: testLiveLayoutJSON(t),
			}, nil)
			q.EXPECT().GetEventPageBySlug(gomock.Any(), gomock.Any()).Return(postgres.EventPage{
				ID: uuid.Must(uuid.NewV7()), EventID: eventID, Slug: "tasks", Title: "Tasks", Visibility: int16(eventContentModel.PageVisibilityParticipant),
				Document:    testutil.DocumentWithBinding("count", "{{event.availableChallengeCount}}", "event.availableChallengeCount", "number"),
				PublishedAt: pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true},
			}, nil)
			q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
				EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: teamID, Valid: true},
			}, nil)
			if tc.want > 0 {
				q.EXPECT().ListTeamBoardChallenges(gomock.Any(), postgres.ListTeamBoardChallengesParams{EventTeamID: teamID, PublishedOnly: true}).Return([]postgres.ListTeamBoardChallengesRow{
					{Readiness: int16(teamChallengeModel.ReadinessPublished)},
					{Readiness: int16(teamChallengeModel.ReadinessPreparing)},
				}, nil)
			}
			page, err := uc.GetPublicEventPage(context.Background(), eventID, "tasks", ContentAccess{UserID: &userID, Role: rbac.RoleUser})
			if err != nil {
				t.Fatal(err)
			}
			if got := page.Variables["event.availableChallengeCount"]; got != tc.want {
				t.Fatalf("available count = %#v, want %d", got, tc.want)
			}
		})
	}
}

func TestContentAudienceRejectsPrivateBindingsOnPublicLanding(t *testing.T) {
	for _, name := range []string{"event.publishAt", "event.withdrawAt", "event.runtimeOpen", "event.teamCount"} {
		document := eventContentModel.Document{Blocks: []eventContentModel.Block{{
			ID: "intro", Type: eventContentModel.BlockText, RichText: testutil.RichText("Private setting"),
			Variables: []eventContentModel.VariableBinding{{Name: name, Format: eventContentModel.EventContentVariables[name]}},
		}}}
		if err := validateContentAudience(document, eventContentModel.PageVisibilityPublic); err == nil {
			t.Fatalf("public landing accepted %s", name)
		}
	}
}

func TestContentVariableCatalogUsesResultPolicy(t *testing.T) {
	for _, tc := range []struct {
		visibility eventConfigModel.Visibility
		want       eventContentModel.PageVisibility
	}{
		{eventConfigModel.VisibilityHidden, eventContentModel.PageVisibilityManager},
		{eventConfigModel.VisibilityPrivate, eventContentModel.PageVisibilityParticipant},
		{eventConfigModel.VisibilityPublic, eventContentModel.PageVisibilityPublic},
	} {
		catalog := ContentVariableCatalog(tc.visibility)
		if len(catalog) != len(eventContentModel.EventContentVariables) {
			t.Fatalf("catalog has %d entries, provider declares %d", len(catalog), len(eventContentModel.EventContentVariables))
		}
		for _, item := range catalog {
			if item.Label == item.Name {
				t.Fatalf("catalog entry %s lacks a label", item.Name)
			}
			if item.Name == "event.solveCount" && item.Audience != tc.want {
				t.Fatalf("solve count audience = %d, want %d", item.Audience, tc.want)
			}
		}
	}
}

func TestCanReadContentPageHonorsPageVisibility(t *testing.T) {
	public := eventContentModel.Page{Visibility: eventContentModel.PageVisibilityPublic}
	participant := eventContentModel.Page{Visibility: eventContentModel.PageVisibilityParticipant}
	manager := eventContentModel.Page{Visibility: eventContentModel.PageVisibilityManager}
	if !canReadContentPage(public, ContentAccess{Role: rbac.RolePublic}, false) {
		t.Fatal("public page must be readable anonymously")
	}
	if canReadContentPage(participant, ContentAccess{Role: rbac.RolePublic}, false) || canReadContentPage(participant, ContentAccess{Role: rbac.RoleUser}, false) {
		t.Fatal("participant page must require approved membership")
	}
	if !canReadContentPage(participant, ContentAccess{Role: rbac.RoleUser}, true) {
		t.Fatal("participant page must be readable by an approved participant")
	}
	if canReadContentPage(manager, ContentAccess{Role: rbac.RoleUser}, true) || canReadContentPage(manager, ContentAccess{Role: rbac.RoleSuperAdmin}, false) {
		t.Fatal("manager pages must use the event-scoped management route")
	}
}

func TestNavigationPagesRevealOnlyVisibleEnabledEntries(t *testing.T) {
	published := &time.Time{}
	pages := []EventPageView{
		{Slug: "manager", Title: "Manager", Visibility: eventContentModel.PageVisibilityManager, Navigation: eventContentModel.PageNavigationNone, PublishedAt: published},
		{Slug: "faq", Title: "FAQ", Visibility: eventContentModel.PageVisibilityPublic, Navigation: eventContentModel.PageNavigationNavbar, NavigationOrder: 2, PublishedAt: published},
		{Slug: "guide", Title: "Guide", Visibility: eventContentModel.PageVisibilityPublic, Navigation: eventContentModel.PageNavigationNavbar, NavigationOrder: -1, PublishedAt: published},
		{Slug: "rules", Title: "Rules", Visibility: eventContentModel.PageVisibilityPublic, Navigation: eventContentModel.PageNavigationNavbar, NavigationOrder: 1, PublishedAt: published},
		{Slug: "private", Title: "Private", Visibility: eventContentModel.PageVisibilityParticipant, Navigation: eventContentModel.PageNavigationNavbar, NavigationOrder: 3, PublishedAt: published},
		{Slug: "draft", Title: "Draft", Visibility: eventContentModel.PageVisibilityPublic, Navigation: eventContentModel.PageNavigationNavbar, NavigationOrder: 0},
	}
	guest := projectNavigationPages(pages, false)
	if len(guest) != 3 || guest[0].Slug != "guide" || guest[0].NavigationOrder != -1 || guest[1].Slug != "rules" || guest[2].Slug != "faq" {
		t.Fatalf("guest navigation = %+v", guest)
	}
	participant := projectNavigationPages(pages, true)
	if len(participant) != 4 || participant[3].Slug != "private" {
		t.Fatalf("participant navigation = %+v", participant)
	}
}

func TestGetEventContentCombinesStoredDocumentsAndLiveValues(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := NewEventUseCase(Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	finishAt := now.Add(time.Hour)
	withdrawAt := now.Add(2 * time.Hour)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{
		ID: eventID, Tag: "games", Name: "Games", AvailableFrom: now.Add(-time.Hour), ArchiveAt: pgtype.Timestamptz{Time: withdrawAt, Valid: true},
		CreatedAt: now.Add(-time.Hour), JoinPolicy: int16(eventModel.JoinPolicyRolling), PublishAt: now.Add(-time.Hour), StartAt: now.Add(-30 * time.Minute),
		FinishAt: pgtype.Timestamptz{Time: finishAt, Valid: true}, WithdrawAt: pgtype.Timestamptz{Time: withdrawAt, Valid: true},
	}, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true}, MaxTeamSize: 5,
	}, nil)
	q.EXPECT().CountEventTeams(gomock.Any(), eventID).Return(int64(4), nil)
	q.EXPECT().CountApprovedEventTeams(gomock.Any(), eventID).Return(int64(3), nil)
	q.EXPECT().CountEventParticipants(gomock.Any(), gomock.Any()).Return(int64(7), nil).Times(2)
	q.EXPECT().GetEventContentStatistics(gomock.Any(), eventID).Return(postgres.GetEventContentStatisticsRow{
		ChallengeCount: 5, PublishedChallengeCount: 4, SolvedChallengeCount: 2, SolveCount: 6,
	}, nil)
	q.EXPECT().GetEventContentSettings(gomock.Any(), eventID).Return(postgres.GetEventContentSettingsRow{
		ID: eventID, LandingDocument: testutil.DocumentWithBinding("welcome", "Welcome {{event.teamCount}}", "event.teamCount", "number"),
		LiveLayout: testLiveLayoutJSON(t),
	}, nil)

	content, err := uc.GetEventContent(context.Background(), eventID)
	if err != nil {
		t.Fatalf("get event content: %v", err)
	}
	if content.Landing.Blocks[0].ID != "welcome" || content.Variables["event.teamCount"] != int64(4) || content.Live.Grid.Cols != 12 {
		t.Fatalf("unexpected event content: %+v", content)
	}
}

func TestSaveLandingDraftValidatesAndWritesDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := NewEventUseCase(Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	document := eventContentModel.Document{Blocks: []eventContentModel.Block{{ID: "intro", Type: eventContentModel.BlockText, RichText: testutil.RichText("Welcome")}}}
	q.EXPECT().SaveEventLandingDraft(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.SaveEventLandingDraftParams) (int64, error) {
			if arg.EventID != eventID {
				t.Fatalf("event ID = %s, want %s", arg.EventID, eventID)
			}
			return 1, nil
		})
	if err := uc.SaveLandingDraft(context.Background(), eventID, document); err != nil {
		t.Fatalf("save landing draft: %v", err)
	}
	if err := uc.SaveLandingDraft(context.Background(), eventID, eventContentModel.Document{Blocks: []eventContentModel.Block{{ID: "field", Type: eventContentModel.BlockField, Key: "x", Input: "text", Label: "X"}}}); err == nil {
		t.Fatal("landing must reject form fields")
	}
}

func TestSaveLandingDraftRejectsResultCountWhenResultsAreHidden(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := NewEventUseCase(Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	document := eventContentModel.Document{Blocks: []eventContentModel.Block{{
		ID: "score", Type: eventContentModel.BlockText, RichText: testutil.RichText("{{event.solveCount}}"),
		Variables: []eventContentModel.VariableBinding{{Name: "event.solveCount", Format: eventContentModel.VariableFormatNumber}},
	}}}
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(eventConfigModel.VisibilityHidden)}, nil)
	if err := uc.SaveLandingDraft(context.Background(), eventID, document); err == nil {
		t.Fatal("hidden result count must not be saved on the public landing")
	}
}

func testLiveLayoutJSON(t *testing.T) []byte {
	t.Helper()
	value, err := json.Marshal(eventContentModel.DefaultLiveLayout())
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestSaveLiveLayoutDraftValidatesAndWritesEventSettings(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := NewEventUseCase(Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	layout := eventContentModel.DefaultLiveLayout()
	q.EXPECT().SaveEventLiveLayoutDraft(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.SaveEventLiveLayoutDraftParams) (int64, error) {
			if arg.EventID != eventID {
				t.Fatalf("event ID = %s, want %s", arg.EventID, eventID)
			}
			if !json.Valid(arg.LiveLayoutDraft) {
				t.Fatalf("draft is not JSON")
			}
			return 1, nil
		})
	if err := uc.SaveLiveLayoutDraft(context.Background(), eventID, layout); err != nil {
		t.Fatalf("save live layout: %v", err)
	}
	if err := uc.SaveLiveLayoutDraft(context.Background(), eventID, eventContentModel.LiveLayout{}); err == nil {
		t.Fatal("invalid live canvas must be rejected")
	}
}

func TestLiveLayoutEditorReadsDraftAndPublishIncrementsVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := NewEventUseCase(Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	published := eventContentModel.DefaultLiveLayout()
	draft := eventContentModel.DefaultLiveLayout()
	draft.Theme = "light"
	draftJSON, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	q.EXPECT().GetEventContentSettings(gomock.Any(), eventID).Return(postgres.GetEventContentSettingsRow{
		ID: eventID, LandingDocument: []byte(`{"blocks":[]}`), LiveLayout: testLiveLayoutJSON(t), LiveLayoutDraft: draftJSON,
	}, nil)
	view, err := uc.GetLiveLayoutEditor(context.Background(), eventID)
	if err != nil || view.Draft == nil || view.Draft.Theme != "light" || view.Published.Version != 1 {
		t.Fatalf("editor view: %+v %v", view, err)
	}
	published.Version = 2
	published.Theme = "light"
	publishedJSON, err := json.Marshal(published)
	if err != nil {
		t.Fatal(err)
	}
	q.EXPECT().GetEventContentSettings(gomock.Any(), eventID).Return(postgres.GetEventContentSettingsRow{
		ID: eventID, LandingDocument: []byte(`{"blocks":[]}`), LiveLayout: testLiveLayoutJSON(t), LiveLayoutDraft: draftJSON,
	}, nil)
	q.EXPECT().PublishEventLiveLayout(gomock.Any(), eventID).Return(publishedJSON, nil)
	result, err := uc.PublishLiveLayout(context.Background(), eventID)
	if err != nil || result.Version != 2 || result.Theme != "light" {
		t.Fatalf("published layout: %+v %v", result, err)
	}
}

func TestResolveContentVariablesUsesApprovedParticipantsForIndividualEvents(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	lifecycle, err := eventModel.NewLifecycle(eventModel.JoinPolicyLockedAtStart, now, now.Add(time.Hour), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	participation := eventConfigModel.ParticipationIndividual
	values := resolveContentVariables(
		eventModel.Event{Lifecycle: lifecycle},
		eventConfigModel.EventConfig{Participation: &participation},
		eventContentModel.Statistics{ApprovedTeamCount: 5, ApprovedParticipantCount: 8},
		now,
	)
	if got := values["event.registrationUnitCount"]; got != int64(8) {
		t.Fatalf("registration unit count = %#v, want approved individual participants", got)
	}
	if values["event.finishAt"] != nil || values["event.effectiveFinishAt"] != nil {
		t.Fatalf("permanent event must expose nil finish values: %#v", values)
	}
}

// A legacy layout stored before the 3x3 editor minimum must keep loading, but
// it cannot be published again until the manager saves a valid draft.
func TestLegacyTinyLiveLayoutLoadsButDoesNotPublish(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := NewEventUseCase(Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	legacy := eventContentModel.DefaultLiveLayout()
	legacy.Grid = eventContentModel.LiveGrid{Cols: 1, Rows: 1}
	legacy.Widgets = []eventContentModel.LiveWidget{{ID: "qr", Type: "qr", X: 1, Y: 1, W: 1, H: 1, Props: map[string]json.RawMessage{}}}
	legacyJSON, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	row := postgres.GetEventContentSettingsRow{ID: eventID, LandingDocument: []byte(`{"blocks":[]}`), LiveLayout: legacyJSON, LiveLayoutDraft: legacyJSON}
	q.EXPECT().GetEventContentSettings(gomock.Any(), eventID).Return(row, nil).Times(2)

	view, err := uc.GetLiveLayoutEditor(context.Background(), eventID)
	if err != nil || view.Published.Grid.Cols != 1 || view.Draft == nil {
		t.Fatalf("legacy layout did not load: %+v %v", view, err)
	}
	if _, err = uc.PublishLiveLayout(context.Background(), eventID); !errors.Is(err, eventContentModel.ErrLiveDraftInvalid.Err()) {
		t.Fatalf("publish err = %v, want ErrLiveDraftInvalid", err)
	}
	if err = uc.SaveLiveLayoutDraft(context.Background(), eventID, legacy); !errors.Is(err, eventContentModel.ErrLiveLayoutInvalid.Err()) {
		t.Fatalf("save err = %v, want ErrLiveLayoutInvalid", err)
	}
}
