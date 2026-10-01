package eventAnalytics

import (
	"time"

	"github.com/gofrs/uuid"

	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
)

// Read models of «Учасники» (§6.2) and «Комунікації» (§6.7).

// Funnel stage keys, in order.
const (
	StageInvited    = "invited"
	StageRegistered = "registered"
	StageApproved   = "approved"
	StageInTeam     = "in_team"
	StageAttempted  = "attempted"
	StageSolved     = "solved"
)

// Registration channels.
const (
	ChannelOpen       = "open"
	ChannelApproval   = "approval"
	ChannelInvitation = "invitation"
)

type (
	// ParticipantsView is the «Учасники» report.
	ParticipantsView struct {
		// TeamMode is false for an individual event: no team stage, no fill.
		TeamMode      bool
		Funnel        []FunnelStageView
		Registrations RegistrationsView
		Teams         TeamFillView
		Answers       AnswersView
		DropOff       DropOffView
		// Period bounds the registrations per day (zero: open bound).
		Period OptionalPeriodView
	}

	// OptionalPeriodView is a window whose bounds may be open.
	OptionalPeriodView struct {
		From *time.Time
		To   *time.Time
	}

	FunnelStageView struct {
		Stage string
		Count int64
	}

	// RegistrationsView is the registrations per UTC day, every day present.
	RegistrationsView struct {
		Days  []RegistrationDayView
		Total int64
	}

	RegistrationDayView struct {
		Day                        time.Time
		Open, Approval, Invitation int64
	}

	// TeamFillView is the team size against the event's limits.
	TeamFillView struct {
		MinSize int32
		MaxSize int32
		Total   int64
		// Histogram has an entry per size from 0 to MaxSize.
		Histogram  []FillBucketView
		Incomplete []IncompleteTeamView
		// PendingInvitees are invited to a team and not joined yet.
		PendingInvitees int64
		// WithoutTeam are approved participants in no team.
		WithoutTeam int64
	}

	FillBucketView struct {
		Members int32
		Teams   int64
	}

	IncompleteTeamView struct {
		ID              uuid.UUID
		Name            string
		Members         int32
		PendingInvitees int64
	}

	// AnswersView is the distribution of the registration form answers.
	AnswersView struct {
		Respondents int64
		Questions   []QuestionView
	}

	// QuestionView is one registration question. Input picks the chart:
	// select, multi_select, checkbox: bars over Buckets; number: histogram
	// with Min/Max/Avg; date: timeline; text, long_text: top repeated values
	// (Distinct counts all different ones); file: has/none.
	QuestionView struct {
		Key      string
		Label    string
		Input    string
		Asked    int64
		Answered int64
		Distinct int64
		Buckets  []BucketView
		Min      *float64
		Max      *float64
		Avg      *float64
	}

	BucketView struct {
		Label string
		Count int64
	}

	DropOffView struct {
		// Total may exceed len(Rows): the list is cut.
		Total int64
		Rows  []DropOffRowView
	}

	DropOffRowView struct {
		UserID       uuid.UUID
		Name         string
		Email        string
		TeamName     string
		RegisteredAt time.Time
		ApprovedAt   *time.Time
		OpenedTasks  int64
	}

	// CommunicationsView is the «Комунікації» report.
	CommunicationsView struct {
		Totals CommsTypeView
		Types  []CommsTypeView
		Forms  []FormCompletionView
		// Funnels are the outcomes of invitations, registrations and applications.
		Funnels dispatchModel.FunnelsSummary
		Period  OptionalPeriodView
	}

	// CommsTypeView is the figures of one notification type (Type is empty in
	// the totals). Sent is handed to the transport; Errors failed.
	CommsTypeView struct {
		Type         string
		EmailSent    int64
		EmailErrors  int64
		InAppSent    int64
		InAppErrors  int64
		InAppCreated int64
		InAppRead    int64
		// ReadRate is InAppRead / InAppCreated, nil without in-app items.
		ReadRate *float64
	}

	FormCompletionView struct {
		ID           uuid.UUID
		Title        string
		Registration bool
		Enabled      bool
		Assigned     int64
		Completed    int64
		Answers      int64
		// CompletionRate is Completed / Assigned, nil when nothing was assigned.
		CompletionRate *float64
	}
)
