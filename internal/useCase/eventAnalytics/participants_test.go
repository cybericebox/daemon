package eventAnalytics_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

// participantFake is the «Учасники» and «Комунікації» half of fakeStore.
type participantFake struct {
	funnel     eventAnalyticsRepo.ParticipantFunnel
	days       []eventAnalyticsRepo.RegistrationDay
	teams      []eventAnalyticsRepo.TeamFill
	versions   []eventAnalyticsRepo.FormVersion
	answers    []eventAnalyticsRepo.FormAnswer
	dropOffs   []eventAnalyticsRepo.DropOff
	dropTotal  int64
	dispatches []eventAnalyticsRepo.DispatchCount
	inApp      []eventAnalyticsRepo.InAppCount
	forms      []eventAnalyticsRepo.FormCompletion
	mailFunnel dispatchModel.MailFunnels
	windowFrom *time.Time
	windowTo   *time.Time
}

func (p *participantFake) ParticipantFunnel(context.Context, uuid.UUID) (eventAnalyticsRepo.ParticipantFunnel, error) {
	return p.funnel, nil
}
func (p *participantFake) RegistrationDays(_ context.Context, _ uuid.UUID, from, to *time.Time) ([]eventAnalyticsRepo.RegistrationDay, error) {
	p.windowFrom, p.windowTo = from, to
	return p.days, nil
}
func (p *participantFake) TeamFill(context.Context, uuid.UUID) ([]eventAnalyticsRepo.TeamFill, error) {
	return p.teams, nil
}
func (p *participantFake) RegistrationFormVersions(context.Context, uuid.UUID) ([]eventAnalyticsRepo.FormVersion, error) {
	return p.versions, nil
}
func (p *participantFake) RegistrationAnswers(context.Context, uuid.UUID) ([]eventAnalyticsRepo.FormAnswer, error) {
	return p.answers, nil
}
func (p *participantFake) DropOffs(context.Context, uuid.UUID, int32) ([]eventAnalyticsRepo.DropOff, int64, error) {
	return p.dropOffs, p.dropTotal, nil
}
func (p *participantFake) DispatchStats(context.Context, uuid.UUID, *time.Time, *time.Time) ([]eventAnalyticsRepo.DispatchCount, error) {
	return p.dispatches, nil
}
func (p *participantFake) InAppStats(context.Context, uuid.UUID, *time.Time, *time.Time) ([]eventAnalyticsRepo.InAppCount, error) {
	return p.inApp, nil
}
func (p *participantFake) FormCompletion(context.Context, uuid.UUID, *time.Time, *time.Time) ([]eventAnalyticsRepo.FormCompletion, error) {
	return p.forms, nil
}

func (p *participantFake) MailFunnels(context.Context, uuid.UUID, *time.Time, *time.Time) (dispatchModel.MailFunnels, error) {
	return p.mailFunnel, nil
}

func participationUC(store *fakeStore, mode eventConfigModel.Participation) (*eventAnalytics.EventAnalyticsUseCase, uuid.UUID) {
	event := runningEvent()
	clock := now
	config := eventConfigModel.EventConfig{Participation: &mode, MaxTeamSize: 4}
	return eventAnalytics.New(eventAnalytics.Dependencies{
		Store: store, Events: fakeEvents{event}, Configs: fakeConfigs{config}, Now: func() time.Time { return clock },
	}), event.ID
}

func field(key, input, label string, options ...string) eventContentModel.Block {
	return eventContentModel.Block{ID: key, Type: eventContentModel.BlockField, Key: key, Input: input, Label: label, Options: options}
}

func TestParticipants_FunnelSkipsTheTeamStageForIndividualEvents(t *testing.T) {
	store := &fakeStore{participantFake: participantFake{funnel: eventAnalyticsRepo.ParticipantFunnel{Invited: 5, Registered: 20, Approved: 18, InTeam: 15, Attempted: 9, Solved: 4}}}
	uc, id := participationUC(store, eventConfigModel.ParticipationIndividual)
	v, err := uc.GetEventAnalyticsParticipants(context.Background(), id, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var stages []string
	for _, s := range v.Funnel {
		stages = append(stages, s.Stage)
	}
	want := []string{"invited", "registered", "approved", "attempted", "solved"}
	if v.TeamMode || len(stages) != len(want) || v.Funnel[4].Count != 4 {
		t.Fatalf("individual funnel: %v %+v", stages, v.Funnel)
	}
	for i := range want {
		if stages[i] != want[i] {
			t.Fatalf("stages: %v", stages)
		}
	}

	uc, id = participationUC(store, eventConfigModel.ParticipationTeam)
	v, err = uc.GetEventAnalyticsParticipants(context.Background(), id, nil, nil)
	if err != nil || !v.TeamMode || len(v.Funnel) != 6 || v.Funnel[3].Stage != "in_team" || v.Funnel[3].Count != 15 {
		t.Fatalf("team funnel: %+v %v", v.Funnel, err)
	}
	if v.Teams.WithoutTeam != 3 {
		t.Fatalf("approved without a team: %d", v.Teams.WithoutTeam)
	}
}

func TestParticipants_RegistrationDaysAreDenseAndSplitByChannel(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2026, 9, day, 0, 0, 0, 0, time.UTC) }
	store := &fakeStore{participantFake: participantFake{days: []eventAnalyticsRepo.RegistrationDay{
		{Day: d(26), Channel: "open", Registrations: 3},
		{Day: d(26), Channel: "invitation", Registrations: 1},
		{Day: d(28), Channel: "approval", Registrations: 2},
	}}}
	uc, id := participationUC(store, eventConfigModel.ParticipationIndividual)
	v, err := uc.GetEventAnalyticsParticipants(context.Background(), id, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	days := v.Registrations.Days
	// 26th .. today (the 29th).
	if len(days) != 4 || days[0].Open != 3 || days[0].Invitation != 1 || days[1].Open != 0 || days[2].Approval != 2 || !days[3].Day.Equal(d(29)) || v.Registrations.Total != 6 {
		t.Fatalf("days: %+v total %d", days, v.Registrations.Total)
	}

	from, to := d(27), d(29)
	v, err = uc.GetEventAnalyticsParticipants(context.Background(), id, &from, &to)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Registrations.Days; len(got) != 2 || !got[0].Day.Equal(d(27)) || !got[1].Day.Equal(d(28)) {
		t.Fatalf("windowed days: %+v", got)
	}
	if store.windowFrom == nil || !store.windowFrom.Equal(from) {
		t.Fatal("the window must reach the query")
	}

	bad := d(30)
	if _, err := uc.GetEventAnalyticsParticipants(context.Background(), id, &bad, &from); !errors.Is(err, eventAnalyticsModel.ErrEventAnalyticsPeriodInvalid.Err()) {
		t.Fatalf("reversed window: %v", err)
	}
}

func TestParticipants_TeamFillCountsSizesAndListsIncompleteTeams(t *testing.T) {
	teamA, teamB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	store := &fakeStore{participantFake: participantFake{teams: []eventAnalyticsRepo.TeamFill{
		{ID: teamA, Name: "Alpha", Members: 1, Admitted: false, PendingInvitees: 2},
		{ID: teamB, Name: "Beta", Members: 4, Admitted: true},
		{Name: "Solo", Members: 1, Individual: true, Admitted: true},
	}}}
	uc, id := participationUC(store, eventConfigModel.ParticipationTeam)
	v, err := uc.GetEventAnalyticsParticipants(context.Background(), id, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := v.Teams
	if f.MaxSize != 4 || f.MinSize != 2 || f.Total != 3 || len(f.Histogram) != 5 || f.Histogram[1].Teams != 2 || f.Histogram[4].Teams != 1 || f.PendingInvitees != 2 {
		t.Fatalf("fill: %+v", f)
	}
	if len(f.Incomplete) != 1 || f.Incomplete[0].ID != teamA || f.Incomplete[0].PendingInvitees != 2 {
		t.Fatalf("incomplete: %+v", f.Incomplete)
	}

	uc, id = participationUC(store, eventConfigModel.ParticipationIndividual)
	v, _ = uc.GetEventAnalyticsParticipants(context.Background(), id, nil, nil)
	if v.Teams.Total != 0 || v.Teams.Histogram != nil {
		t.Fatalf("individual events have no team fill: %+v", v.Teams)
	}
}

func TestParticipants_AnswerDistributionsPerQuestionType(t *testing.T) {
	v1, v2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	store := &fakeStore{participantFake: participantFake{
		versions: []eventAnalyticsRepo.FormVersion{
			{ID: v1, Version: 1, Blocks: []eventContentModel.Block{field("city", "select", "City", "Kyiv", "Lviv"), field("old", "text", "Old")}},
			{ID: v2, Version: 2, Blocks: []eventContentModel.Block{
				{ID: "h", Type: eventContentModel.BlockText, Title: "Intro"},
				field("city", "select", "City", "Kyiv", "Lviv", "Odesa"), field("age", "number", "Age"), field("langs", "multi_select", "Languages", "Go", "Py"),
				field("nick", "text", "Nick"), field("cv", "file", "CV"), field("student", "checkbox", "Student"), field("born", "date", "Born"),
			}},
		},
		answers: []eventAnalyticsRepo.FormAnswer{
			{VersionID: v1, Answers: map[string]any{"city": "Kyiv", "old": "x"}},
			{VersionID: v2, Answers: map[string]any{"city": "Kyiv", "age": 20.0, "langs": []any{"Go", "Py"}, "nick": " Ace ", "cv": map[string]any{"id": "f1"}, "student": true, "born": "2005-05-05"}},
			{VersionID: v2, Answers: map[string]any{"city": "Odesa", "age": 22.0, "langs": []any{"Go"}, "nick": "ace", "cv": "", "born": "2005-06-01T00:00:00Z"}},
			{VersionID: v2, Answers: map[string]any{"city": "Lviv", "age": 40.0, "nick": "zed"}},
		},
	}}
	uc, id := participationUC(store, eventConfigModel.ParticipationIndividual)
	v, err := uc.GetEventAnalyticsParticipants(context.Background(), id, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.Answers.Respondents != 4 {
		t.Fatalf("respondents: %d", v.Answers.Respondents)
	}
	byKey := map[string]eventAnalytics.QuestionView{}
	var keys []string
	for _, q := range v.Answers.Questions {
		byKey[q.Key] = q
		keys = append(keys, q.Key)
	}
	// The newest version's order first, the dropped question last.
	if keys[0] != "city" || keys[len(keys)-1] != "old" {
		t.Fatalf("order: %v", keys)
	}
	city := byKey["city"]
	if city.Asked != 4 || city.Answered != 4 || len(city.Buckets) != 3 || city.Buckets[0].Count != 2 || city.Buckets[1].Count != 1 || city.Buckets[2].Label != "Odesa" {
		t.Fatalf("city: %+v", city)
	}
	age := byKey["age"]
	if age.Answered != 3 || age.Min == nil || *age.Min != 20 || *age.Max != 40 || len(age.Buckets) != 10 {
		t.Fatalf("age: %+v", age)
	}
	var total int64
	for _, b := range age.Buckets {
		total += b.Count
	}
	if total != 3 {
		t.Fatalf("age histogram loses answers: %+v", age.Buckets)
	}
	if langs := byKey["langs"]; langs.Buckets[0].Count != 2 || langs.Buckets[1].Count != 1 || langs.Answered != 2 {
		t.Fatalf("langs: %+v", langs)
	}
	nick := byKey["nick"]
	if nick.Distinct != 2 || len(nick.Buckets) != 1 || nick.Buckets[0].Label != "ace" || nick.Buckets[0].Count != 2 {
		t.Fatalf("nick: only repeated values are listed: %+v", nick)
	}
	if cv := byKey["cv"]; cv.Buckets[0].Count != 1 || cv.Buckets[1].Count != 2 {
		t.Fatalf("cv has/none: %+v", cv)
	}
	if st := byKey["student"]; st.Buckets[0].Label != "yes" || st.Buckets[0].Count != 1 || st.Buckets[1].Count != 2 {
		t.Fatalf("student: %+v", st)
	}
	if born := byKey["born"]; len(born.Buckets) != 2 || born.Buckets[0].Label != "2005-05-05" {
		t.Fatalf("born: %+v", born)
	}
	if old := byKey["old"]; old.Asked != 1 || old.Answered != 1 {
		t.Fatalf("old: %+v", old)
	}
}

func TestCommunications_GroupsTypesAndComputesRates(t *testing.T) {
	store := &fakeStore{participantFake: participantFake{
		dispatches: []eventAnalyticsRepo.DispatchCount{
			{Type: "event.start", Channel: "email", Status: "done", Targets: 10},
			{Type: "event.start", Channel: "email", Status: "error", Targets: 2},
			{Type: "event.start", Channel: "in_app", Status: "done", Targets: 12},
			{Type: "team.invite", Channel: "email", Status: "done", Targets: 30},
			{Type: "team.invite", Channel: "email", Status: "pending", Targets: 5},
		},
		inApp: []eventAnalyticsRepo.InAppCount{{Type: "event.start", Total: 12, Read: 9}, {Type: "inbox.note", Total: 4, Read: 0}},
		forms: []eventAnalyticsRepo.FormCompletion{
			{ID: uuid.Must(uuid.NewV7()), Title: "Registration", Purpose: "registration", Enabled: true, Answers: 40},
			{ID: uuid.Must(uuid.NewV7()), Title: "Feedback", Purpose: "other", Enabled: true, Assigned: 20, Completed: 5, Answers: 5},
		},
	}}
	uc, id := participationUC(store, eventConfigModel.ParticipationIndividual)
	v, err := uc.GetEventAnalyticsCommunications(context.Background(), id, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Types) != 3 || v.Types[0].Type != "team.invite" || v.Types[1].Type != "event.start" {
		t.Fatalf("types are ordered by volume: %+v", v.Types)
	}
	start := v.Types[1]
	if start.EmailSent != 10 || start.EmailErrors != 2 || start.InAppSent != 12 || start.ReadRate == nil || *start.ReadRate != 0.75 {
		t.Fatalf("event.start: %+v", start)
	}
	if v.Types[0].ReadRate != nil {
		t.Fatal("no in-app items, no read rate")
	}
	if v.Totals.EmailSent != 40 || v.Totals.InAppCreated != 16 || v.Totals.InAppRead != 9 || v.Totals.ReadRate == nil || *v.Totals.ReadRate != 9.0/16 {
		t.Fatalf("totals: %+v", v.Totals)
	}
	if len(v.Forms) != 2 || !v.Forms[0].Registration || v.Forms[0].CompletionRate != nil || v.Forms[1].CompletionRate == nil || *v.Forms[1].CompletionRate != 0.25 {
		t.Fatalf("forms: %+v", v.Forms)
	}
}

func TestCommunications_FunnelCards(t *testing.T) {
	store := &fakeStore{participantFake: participantFake{mailFunnel: dispatchModel.MailFunnels{
		InvitationsSent: 10, InvitationsAccepted: 4, InvitationAcceptSamples: 4, InvitationAcceptMedianSeconds: 5400.4,
		RegistrationsStarted: 8, RegistrationsCompleted: 6,
		ApplicationsSubmitted: 20, ApplicationsApproved: 9, ApplicationsRejected: 3, ApplicationDecisionSamples: 12, ApplicationDecisionMedianSeconds: 90,
	}}}
	uc, id := participationUC(store, eventConfigModel.ParticipationIndividual)
	v, err := uc.GetEventAnalyticsCommunications(context.Background(), id, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	inv, reg, app := v.Funnels.Invitations, v.Funnels.Registration, v.Funnels.Applications
	if inv.Sent != 10 || inv.Accepted != 4 || inv.AcceptRate == nil || *inv.AcceptRate != 0.4 || inv.MedianAcceptSeconds == nil || *inv.MedianAcceptSeconds != 5400 {
		t.Fatalf("invitations: %+v", inv)
	}
	if reg.Started != 8 || reg.Completed != 6 || reg.CompletionRate == nil || *reg.CompletionRate != 0.75 {
		t.Fatalf("registration: %+v", reg)
	}
	if app.Submitted != 20 || app.Decided != 12 || *app.DecidedRate != 0.6 || *app.ApprovedRate != 0.75 || *app.RejectedRate != 0.25 || *app.MedianDecisionSeconds != 90 {
		t.Fatalf("applications: %+v", app)
	}
}

func TestCommunications_EmptyFunnelsHaveNoRatesOrMedians(t *testing.T) {
	uc, id := participationUC(&fakeStore{}, eventConfigModel.ParticipationIndividual)
	v, err := uc.GetEventAnalyticsCommunications(context.Background(), id, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := v.Funnels
	if f.Invitations.AcceptRate != nil || f.Invitations.MedianAcceptSeconds != nil || f.Registration.CompletionRate != nil ||
		f.Applications.DecidedRate != nil || f.Applications.ApprovedRate != nil || f.Applications.MedianDecisionSeconds != nil {
		t.Fatalf("nothing to divide by: %+v", f)
	}
}
