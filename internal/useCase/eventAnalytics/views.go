package eventAnalytics

import (
	"time"

	"github.com/gofrs/uuid"
)

// Read models of the analytics reports.

type (
	// PeriodView is the report window [From, To).
	PeriodView struct {
		From time.Time
		To   time.Time
	}

	// OverviewView is the «Огляд» dashboard (§6.1).
	OverviewView struct {
		Participants ParticipantCountsView
		Teams        TeamCountsView
		Attempts     int64
		// Correct counts effectively correct attempts (after decisions).
		Correct     int64
		Solves      int64
		HintsOpened int64
		HintPoints  int64
		Stands      StandCountsView
		// Series is the event-wide activity per 5-minute bucket over Period,
		// every bucket present (zero when idle).
		Series []SeriesPointView
		// Feed is the newest notable moments, newest first: first bloods,
		// stand failures, new teams and the freeze start.
		Feed []FeedItemView
		// Leaders, Tasks, Engagement and Comms are the overview's snapshot
		// cards. Comms is for the sensitive access only: the handler drops
		// it for other viewers.
		OverviewSnapshots
		Markers MarkersView
		Period  PeriodView
		// RefreshedAt is when the series was last rebuilt (nil: not yet);
		// Final reports that the event's rollup is final.
		RefreshedAt *time.Time
		Final       bool
	}

	// OverviewSnapshots are the overview's cards built from the scoreboard,
	// the task table and the mail dispatches.
	OverviewSnapshots struct {
		// Leaders is the top of the scoreboard; RankedTeams the whole board.
		Leaders     []LeaderView
		RankedTeams int64
		Tasks       TasksSnapshotView
		Engagement  EngagementView
		Comms       CommsSnapshotView
	}

	// LeaderView is a team on the mini leaderboard; Gap is the points behind
	// the first place.
	LeaderView struct {
		TeamID uuid.UUID
		Name   string
		Rank   int64
		Points int64
		Solved int64
		Gap    int64
	}

	// TasksSnapshotView: Unsolved tasks have no solve, FirstBloods is how
	// many tasks have one taken. The most and least solved are among the
	// solved tasks (nil when none is solved).
	TasksSnapshotView struct {
		Total       int64
		Unsolved    int64
		FirstBloods int64
		MostSolved  *TaskSolvesView
		LeastSolved *TaskSolvesView
	}

	TaskSolvesView struct {
		ChallengeID uuid.UUID
		Name        string
		Solves      int64
	}

	// EngagementView: TeamsSolving of Teams admitted teams have at least one
	// solve; AvgSolves is the mean solves per admitted team.
	EngagementView struct {
		Teams        int64
		TeamsSolving int64
		AvgSolves    float64
	}

	// CommsSnapshotView is the mail of the last 24 hours (since Since).
	CommsSnapshotView struct {
		EmailSent   int64
		EmailFailed int64
		Since       time.Time
	}

	ParticipantCountsView struct {
		// Registered excludes invitations not accepted yet.
		Registered int64
		Approved   int64
		// Pending are registrations awaiting approval.
		Pending int64
		// Invited are invitations not accepted yet.
		Invited int64
		// Active acted (an attempt, a task open or download) in the last
		// 15 minutes.
		Active int64
	}

	TeamCountsView struct {
		Total      int64
		Admitted   int64
		Incomplete int64
	}

	StandCountsView struct {
		Creating int64
		Ready    int64
		Failed   int64
	}

	SeriesPointView struct {
		At       time.Time
		Attempts int64
		Correct  int64
		Solves   int64
		Opens    int64
	}

	// FeedItemView is one moment of the feed. Kind is first_blood,
	// stand_failed, team_created or freeze_started; Challenge fields are empty
	// for kinds without a task, Detail is the failure reason of a stand.
	FeedItemView struct {
		Kind          string
		At            time.Time
		TeamID        *uuid.UUID
		TeamName      string
		ChallengeName string
		Detail        string
	}

	// MarkersView are the lifecycle moments drawn on the charts.
	MarkersView struct {
		StartAt  time.Time
		FreezeAt *time.Time
		FinishAt *time.Time
	}
)
