package eventAnalyticsModel_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	m "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

var (
	igT0    = time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	igLater = igT0.Add(48 * time.Hour)
)

func igID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

func igAt(d time.Duration) *time.Time { v := igT0.Add(d); return &v }

// solve builds a solve of team (name) on task at igT0+d that was opened at
// igT0+d-openBefore and has no other evidence.
func igSolve(team uuid.UUID, name string, task uuid.UUID, level string, d, openBefore time.Duration) m.IntegritySolve {
	return m.IntegritySolve{
		TeamChallengeID: igID(), TeamID: team, TeamName: name, ChallengeID: task, ChallengeName: "Web",
		Level: level, SolvedAt: igT0.Add(d), FirstOpen: igAt(d - openBefore),
	}
}

func igDetect(facts m.IntegrityFacts) []m.FlaggedSolve {
	return m.DetectIntegrity(facts, m.DefaultIntegrityThresholds(), igT0.Add(-time.Hour), igLater, igLater)
}

func igKinds(f m.FlaggedSolve) []m.IntegrityKind {
	var out []m.IntegrityKind
	for _, s := range f.Signals {
		out = append(out, s.Kind)
	}
	return out
}

func TestNoAccess(t *testing.T) {
	team, task := igID(), igID()
	started := igAt(0)
	base := igSolve(team, "A", task, "medium", time.Hour, time.Hour)

	t.Run("flagged when nothing was opened, downloaded or unlocked", func(t *testing.T) {
		s := base
		s.FirstOpen = nil
		got := igDetect(m.IntegrityFacts{Solves: []m.IntegritySolve{s}, CollectorStart: started})
		require.Len(t, got, 1)
		require.Equal(t, []m.IntegrityKind{m.IntegrityNoAccess}, igKinds(got[0]))
	})
	t.Run("an attachment download counts as access", func(t *testing.T) {
		s := base
		s.FirstOpen, s.AttachmentCount, s.FirstFile = nil, 1, igAt(time.Minute)
		require.Empty(t, igDetect(m.IntegrityFacts{Solves: []m.IntegritySolve{s}, CollectorStart: started}))
	})
	t.Run("a hint unlock counts as access", func(t *testing.T) {
		s := base
		s.FirstOpen, s.FirstHint = nil, igAt(time.Minute)
		require.Empty(t, igDetect(m.IntegrityFacts{Solves: []m.IntegritySolve{s}, CollectorStart: started}))
	})
	t.Run("an open after the solve is no access", func(t *testing.T) {
		s := base
		s.FirstOpen = igAt(2 * time.Hour)
		require.Len(t, igDetect(m.IntegrityFacts{Solves: []m.IntegritySolve{s}, CollectorStart: started}), 1)
	})
	t.Run("not judged before the collector or without one", func(t *testing.T) {
		s := base
		s.FirstOpen = nil
		require.Empty(t, igDetect(m.IntegrityFacts{Solves: []m.IntegritySolve{s}}))
		require.Empty(t, igDetect(m.IntegrityFacts{Solves: []m.IntegritySolve{s}, CollectorStart: igAt(2 * time.Hour)}))
	})
}

func TestNoLab(t *testing.T) {
	team, task := igID(), igID()
	base := igSolve(team, "A", task, "medium", time.Hour, time.Hour)
	base.HasLab, base.Flag = true, m.FlagStatic

	require.Equal(t, []m.IntegrityKind{m.IntegrityNoLab}, igKinds(igDetect(m.IntegrityFacts{Solves: []m.IntegritySolve{base}})[0]))

	t.Run("a VPN session before the solve clears it", func(t *testing.T) {
		s := base
		s.FirstVPN = igAt(10 * time.Minute)
		require.Empty(t, igDetect(m.IntegrityFacts{Solves: []m.IntegritySolve{s}}))
	})
	t.Run("a VPN session only after the solve does not", func(t *testing.T) {
		s := base
		s.FirstVPN = igAt(3 * time.Hour)
		require.Len(t, igDetect(m.IntegrityFacts{Solves: []m.IntegritySolve{s}}), 1)
	})
	t.Run("every flag kind is checked", func(t *testing.T) {
		for _, flag := range []m.FlagKind{m.FlagDynamic, m.FlagUnknown, m.FlagStatic} {
			s := base
			s.Flag = flag
			require.Len(t, igDetect(m.IntegrityFacts{Solves: []m.IntegritySolve{s}}), 1)
		}
	})
	t.Run("a task without a lab is skipped", func(t *testing.T) {
		s := base
		s.HasLab = false
		require.Empty(t, igDetect(m.IntegrityFacts{Solves: []m.IntegritySolve{s}}))
	})
	t.Run("a fresh solve waits for the VPN rollup", func(t *testing.T) {
		got := m.DetectIntegrity(m.IntegrityFacts{Solves: []m.IntegritySolve{base}}, m.DefaultIntegrityThresholds(),
			igT0, igLater, base.SolvedAt.Add(5*time.Minute))
		require.Empty(t, got)
	})
}

func TestTooFastFloorsPerLevel(t *testing.T) {
	team, task := igID(), igID()
	cases := []struct {
		level   string
		elapsed time.Duration
		flagged bool
	}{
		{"elementary", 2 * time.Second, false},
		{"elementary", 0, false},
		{"trivial", 3 * time.Second, true},
		{"trivial", 6 * time.Second, false},
		{"easy", 19 * time.Second, true},
		{"medium", 59 * time.Second, true},
		{"medium", time.Minute, false},
		{"hard", 119 * time.Second, true},
		{"insane", 239 * time.Second, true},
		{"insane", 4 * time.Minute, false},
		{"", 30 * time.Second, true}, // unknown level counts as medium
		{"legacy", 90 * time.Second, false},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s/%s", c.level, c.elapsed), func(t *testing.T) {
			s := igSolve(team, "A", task, c.level, time.Hour, c.elapsed)
			got := igDetect(m.IntegrityFacts{Solves: []m.IntegritySolve{s}})
			if !c.flagged {
				require.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			sig := got[0].Signals[0]
			require.Equal(t, m.IntegrityTooFast, sig.Kind)
			require.Equal(t, int64(c.elapsed/time.Second), sig.Seconds)
			require.Equal(t, int64(m.DefaultIntegrityThresholds().Floors[got[0].Level]/time.Second), sig.Baseline)
		})
	}

	t.Run("floors are configurable and clamped", func(t *testing.T) {
		th := m.DefaultIntegrityThresholds()
		th.Floors["elementary"] = 10 * time.Second
		th.Floors["hard"] = 24 * time.Hour
		got := th.Clamped()
		require.Equal(t, 10*time.Second, got.Floors["elementary"])
		require.Equal(t, time.Hour, got.Floors["hard"])
		require.Equal(t, 5*time.Second, m.IntegrityThresholds{}.Clamped().Floors["trivial"])
		s := igSolve(team, "A", task, "elementary", time.Hour, 3*time.Second)
		require.Len(t, m.DetectIntegrity(m.IntegrityFacts{Solves: []m.IntegritySolve{s}}, th, igT0, igLater, igLater), 1)
	})
}

func igAttempt(s m.IntegritySolve, answer string, correct bool, d time.Duration) m.IntegrityAttempt {
	return m.IntegrityAttempt{TeamChallengeID: s.TeamChallengeID, TeamID: s.TeamID, ChallengeID: s.ChallengeID, Answer: answer, Correct: correct, At: igT0.Add(d)}
}

func TestFirstTryHard(t *testing.T) {
	task := igID()
	lucky := igSolve(igID(), "Lucky", task, "hard", 5*time.Hour, time.Hour)
	facts := m.IntegrityFacts{Solves: []m.IntegritySolve{lucky}, Attempts: []m.IntegrityAttempt{igAttempt(lucky, "ICE{x}", true, 5*time.Hour)}}
	var others []m.IntegritySolve
	for i := 0; i < 5; i++ {
		o := igSolve(igID(), fmt.Sprintf("T%d", i), task, "hard", time.Duration(i+1)*time.Hour, 30*time.Minute)
		others = append(others, o)
		for k := 0; k < 5; k++ {
			facts.Attempts = append(facts.Attempts, igAttempt(o, fmt.Sprintf("guess-%d-%d", i, k), k == 4, o.SolvedAt.Sub(igT0)-time.Duration(4-k)*time.Second))
		}
	}
	facts.Solves = append(facts.Solves, others...)

	got := igDetect(facts)
	require.Len(t, got, 1)
	require.Equal(t, "Lucky", got[0].Team.Name)
	require.Equal(t, m.IntegrityFirstTryHard, got[0].Signals[0].Kind)
	require.Equal(t, 5, got[0].Signals[0].Count)
	require.Equal(t, int64(5), got[0].Signals[0].Baseline)

	t.Run("too few solving teams", func(t *testing.T) {
		small := m.IntegrityFacts{Solves: facts.Solves[:4], Attempts: facts.Attempts}
		require.Empty(t, igDetect(small))
	})
	t.Run("an easy task is not judged", func(t *testing.T) {
		easy := facts
		easy.Solves = append([]m.IntegritySolve(nil), facts.Solves...)
		for i := range easy.Solves {
			easy.Solves[i].Level = "easy"
		}
		require.Empty(t, igDetect(easy))
	})
	t.Run("a second attempt is not first try", func(t *testing.T) {
		two := facts
		two.Attempts = append(append([]m.IntegrityAttempt(nil), facts.Attempts...), igAttempt(lucky, "nope-nope", false, 5*time.Hour-time.Second))
		require.Empty(t, igDetect(two))
	})
}

func TestSharedWrong(t *testing.T) {
	task := igID()
	a := igSolve(igID(), "Alpha", task, "easy", 3*time.Hour, time.Hour)
	b := igSolve(igID(), "Beta", task, "easy", 4*time.Hour, time.Hour)
	facts := m.IntegrityFacts{
		Solves: []m.IntegritySolve{a, b},
		Attempts: []m.IntegrityAttempt{
			igAttempt(a, "  ICE{Almost-Right} ", false, 2*time.Hour),
			igAttempt(a, "ICE{done}", true, 3*time.Hour),
			igAttempt(b, "ice{almost-right}", false, 3*time.Hour+30*time.Minute),
			igAttempt(b, "short", false, 3*time.Hour+31*time.Minute),
			igAttempt(a, "short", false, 2*time.Hour+time.Minute),
			igAttempt(b, "ICE{done2}", true, 4*time.Hour),
		},
	}
	got := igDetect(facts)
	require.Len(t, got, 2)
	for _, f := range got {
		require.Equal(t, []m.IntegrityKind{m.IntegritySharedWrong}, igKinds(f))
		require.Equal(t, 1, f.Signals[0].Count, "the short answer is too common to compare")
		require.Len(t, f.Signals[0].Teams, 1)
		require.NotEqual(t, f.Team.ID, f.Signals[0].Teams[0].ID)
	}

	t.Run("correct answers are never compared", func(t *testing.T) {
		same := facts
		same.Attempts = []m.IntegrityAttempt{igAttempt(a, "ICE{flag}", true, 3*time.Hour), igAttempt(b, "ICE{flag}", true, 4*time.Hour)}
		require.Empty(t, igDetect(same))
	})
	t.Run("one team repeating itself is not sharing", func(t *testing.T) {
		alone := m.IntegrityFacts{Solves: []m.IntegritySolve{a}, Attempts: []m.IntegrityAttempt{
			igAttempt(a, "ICE{repeat}", false, time.Hour), igAttempt(a, "ICE{repeat}", false, 2*time.Hour)}}
		require.Empty(t, igDetect(alone))
	})
	t.Run("wrong answers after the solve are ignored for that solve", func(t *testing.T) {
		late := m.IntegrityFacts{Solves: []m.IntegritySolve{a}, Attempts: []m.IntegrityAttempt{
			igAttempt(a, "ICE{wrong-late}", false, 5*time.Hour), igAttempt(b, "ICE{wrong-late}", false, 6*time.Hour)}}
		require.Empty(t, igDetect(late))
	})
}

func TestSolveBurst(t *testing.T) {
	team := igID()
	var solves []m.IntegritySolve
	// A slow history: one solve per hour, then three in 40 seconds.
	for i := 0; i < 6; i++ {
		solves = append(solves, igSolve(team, "Fast", igID(), "easy", time.Duration(i)*time.Hour, 30*time.Minute))
	}
	for i := 0; i < 3; i++ {
		solves = append(solves, igSolve(team, "Fast", igID(), "easy", 10*time.Hour+time.Duration(i)*20*time.Second, 30*time.Minute))
	}
	got := igDetect(m.IntegrityFacts{Solves: solves})
	require.Len(t, got, 3)
	sig := got[0].Signals[0]
	require.Equal(t, m.IntegrityBurst, sig.Kind)
	require.Equal(t, 3, sig.Count)
	require.Equal(t, int64(40), sig.Seconds)
	require.Equal(t, int64(3600), sig.Baseline, "the median gap of the team's own history")

	t.Run("a team that always solves quickly is not flagged", func(t *testing.T) {
		var quick []m.IntegritySolve
		for i := 0; i < 8; i++ {
			quick = append(quick, igSolve(team, "Quick", igID(), "easy", time.Duration(i)*20*time.Second, 30*time.Minute))
		}
		require.Empty(t, igDetect(m.IntegrityFacts{Solves: quick}))
	})
	t.Run("too little history", func(t *testing.T) {
		require.Empty(t, igDetect(m.IntegrityFacts{Solves: solves[4:]}))
	})
}

func TestBruteForce(t *testing.T) {
	team, task := igID(), igID()
	s := igSolve(team, "A", task, "medium", time.Hour, 50*time.Minute)
	facts := m.IntegrityFacts{Solves: []m.IntegritySolve{s}}
	for i := 0; i < 12; i++ {
		facts.Attempts = append(facts.Attempts, igAttempt(s, fmt.Sprintf("guess-%d", i), false, 30*time.Minute+time.Duration(i)*time.Second))
	}
	for i := 0; i < 4; i++ {
		facts.Rejections = append(facts.Rejections, m.IntegrityRejection{TeamID: team, ChallengeID: task, Reason: m.RateLimitReason, At: igT0.Add(30*time.Minute + 5*time.Second + time.Duration(i)*time.Second)})
	}
	facts.Rejections = append(facts.Rejections, m.IntegrityRejection{TeamID: team, ChallengeID: task, Reason: "locked", At: igT0.Add(30*time.Minute + 6*time.Second)})
	facts.Attempts = append(facts.Attempts, igAttempt(s, "ICE{ok}", true, time.Hour))

	got := igDetect(facts)
	require.Len(t, got, 1)
	sig := got[0].Signals[0]
	require.Equal(t, m.IntegrityBruteForce, sig.Kind)
	require.Equal(t, 12, sig.Count)
	require.Equal(t, 4, sig.Extra)

	t.Run("a burst after the solve is not this solve's", func(t *testing.T) {
		after := m.IntegrityFacts{Solves: []m.IntegritySolve{s}}
		for i := 0; i < 20; i++ {
			after.Attempts = append(after.Attempts, igAttempt(s, fmt.Sprintf("igLater-%d", i), false, 2*time.Hour+time.Duration(i)*time.Second))
		}
		require.Empty(t, igDetect(after))
	})
}

func TestFollowsSolve(t *testing.T) {
	task := igID()
	first := igSolve(igID(), "First", task, "hard", 3*time.Hour, 2*time.Hour)
	second := igSolve(igID(), "Second", task, "hard", 3*time.Hour+40*time.Second, 30*time.Minute)
	third := igSolve(igID(), "Third", task, "hard", 5*time.Hour, 30*time.Minute)
	got := igDetect(m.IntegrityFacts{
		Solves:   []m.IntegritySolve{first, second, third},
		Attempts: []m.IntegrityAttempt{igAttempt(second, "ICE{x}", true, 3*time.Hour+40*time.Second)},
	})
	require.Len(t, got, 1)
	require.Equal(t, "Second", got[0].Team.Name)
	sig := got[0].Signals[0]
	require.Equal(t, m.IntegrityFollowsSolve, sig.Kind)
	require.Equal(t, int64(40), sig.Seconds)
	require.Equal(t, 1, sig.Count)
	require.Equal(t, "First", sig.Teams[0].Name)
}

func TestOrderingAndPeriod(t *testing.T) {
	team, task := igID(), igID()
	one := igSolve(team, "One", task, "medium", time.Hour, 5*time.Second)
	one.FirstOpen = igAt(time.Hour - 5*time.Second)
	older := igSolve(igID(), "Older", igID(), "medium", 2*time.Hour, 5*time.Second)
	newer := igSolve(igID(), "Newer", igID(), "medium", 3*time.Hour, 5*time.Second)
	two := igSolve(igID(), "Two", igID(), "medium", time.Hour, 5*time.Second)
	two.HasLab, two.Flag = true, m.FlagStatic

	got := igDetect(m.IntegrityFacts{Solves: []m.IntegritySolve{one, older, newer, two}})
	require.Len(t, got, 4)
	require.Equal(t, "Two", got[0].Team.Name, "most signals first")
	require.Equal(t, 2, len(got[0].Signals))
	require.Equal(t, []string{"Newer", "Older", "One"}, []string{got[1].Team.Name, got[2].Team.Name, got[3].Team.Name})

	inPeriod := m.DetectIntegrity(m.IntegrityFacts{Solves: []m.IntegritySolve{one, older, newer, two}}, m.DefaultIntegrityThresholds(),
		igT0.Add(90*time.Minute), igT0.Add(3*time.Hour), igLater)
	require.Len(t, inPeriod, 1)
	require.Equal(t, "Older", inPeriod[0].Team.Name)
}

func TestValidIntegrityKind(t *testing.T) {
	for _, k := range m.IntegrityKinds {
		require.True(t, m.ValidIntegrityKind(string(k)))
	}
	require.False(t, m.ValidIntegrityKind("same_answer"))
}

func TestSharedWrongOrderAndWeight(t *testing.T) {
	task := igID()
	a := igSolve(igID(), "Alpha", task, "easy", 3*time.Hour, time.Hour)
	b := igSolve(igID(), "Beta", task, "easy", 4*time.Hour, time.Hour)
	facts := m.IntegrityFacts{
		Solves: []m.IntegritySolve{a, b},
		Attempts: []m.IntegrityAttempt{
			igAttempt(b, "ICE{decoy}", false, time.Hour),
			igAttempt(a, "ICE{decoy}", false, 2*time.Hour),
		},
	}
	got := igDetect(facts)
	require.Len(t, got, 2)
	for _, f := range got {
		sig := f.Signals[0]
		require.False(t, sig.Info, "an unknown or dynamic flag is a normal signal")
		require.Equal(t, 1, sig.Count)
		require.Equal(t, "ice{decoy}", sig.Answers[0].Value)
		require.Equal(t, []string{"Beta", "Alpha"}, []string{sig.Answers[0].Order[0].Team.Name, sig.Answers[0].Order[1].Team.Name}, "who sent it first, second")
		require.True(t, sig.Answers[0].Order[0].At.Before(sig.Answers[0].Order[1].At))
	}

	t.Run("a static flag makes it informational and lighter", func(t *testing.T) {
		static := facts
		static.Solves = []m.IntegritySolve{a, b}
		static.Solves[0].Flag, static.Solves[1].Flag = m.FlagStatic, m.FlagStatic
		got := igDetect(static)
		require.Len(t, got, 2)
		require.True(t, got[0].Signals[0].Info)
		require.Equal(t, 1, got[0].Score())
	})
}

func igCross(s m.IntegritySolve, owner string, ownerTask uuid.UUID, at time.Duration) m.IntegrityCrossSubmission {
	return m.IntegrityCrossSubmission{
		TeamChallengeID: s.TeamChallengeID, TeamID: s.TeamID, TeamName: s.TeamName, ChallengeID: s.ChallengeID,
		ChallengeName: s.ChallengeName, Level: s.Level, At: igT0.Add(at),
		OwnerTeam: m.IntegrityTeam{ID: igID(), Name: owner}, OwnerChallenge: ownerTask, OwnerName: "Other task",
	}
}

func TestCrossFlag(t *testing.T) {
	task, other := igID(), igID()
	solved := igSolve(igID(), "Thief", task, "hard", 3*time.Hour, time.Hour)
	unsolved := igSolve(igID(), "Peeker", task, "hard", 0, 0)
	facts := m.IntegrityFacts{
		Solves: []m.IntegritySolve{solved},
		CrossFlags: []m.IntegrityCrossSubmission{
			igCross(solved, "Victim", other, 2*time.Hour),
			igCross(solved, "Victim", other, 2*time.Hour+time.Minute),
			igCross(unsolved, "Victim", task, time.Hour),
		},
	}
	got := igDetect(facts)
	require.Len(t, got, 2)
	require.Equal(t, "Thief", got[0].Team.Name, "the solved item carries cross_flag and the later time")
	require.True(t, got[0].Solved)
	sig := got[0].Signals[0]
	require.Equal(t, m.IntegrityCrossFlag, sig.Kind, "the strongest signal goes first")
	require.Equal(t, 2, sig.Count)
	require.Equal(t, "Victim", sig.Owner.Team.Name)
	require.Equal(t, other, sig.Owner.ChallengeID)
	require.False(t, sig.Owner.SameTask)
	require.Equal(t, igT0.Add(2*time.Hour+time.Minute), sig.At)

	require.Equal(t, "Peeker", got[1].Team.Name)
	require.False(t, got[1].Solved, "flagged before any solve")
	require.True(t, got[1].Signals[0].Owner.SameTask)
	require.Equal(t, igT0.Add(time.Hour), got[1].At)

	t.Run("only submissions in the period", func(t *testing.T) {
		in := m.DetectIntegrity(facts, m.DefaultIntegrityThresholds(), igT0.Add(90*time.Minute), igT0.Add(150*time.Minute), igLater)
		require.Len(t, in, 1)
		require.Equal(t, 2, in[0].Signals[0].Count)
	})
}

func TestApplyDismissals(t *testing.T) {
	exercise, task := igID(), igID()
	a := igSolve(igID(), "Alpha", task, "easy", 3*time.Hour, 5*time.Second)
	a.ExerciseID, a.TaskID = exercise, task
	b := igSolve(igID(), "Beta", task, "easy", 4*time.Hour, time.Hour)
	b.ExerciseID, b.TaskID = exercise, task
	facts := m.IntegrityFacts{
		Solves: []m.IntegritySolve{a, b},
		Attempts: []m.IntegrityAttempt{
			igAttempt(a, "ICE{decoy-1}", false, time.Hour), igAttempt(b, "ICE{decoy-1}", false, 2*time.Hour),
			igAttempt(a, "ICE{decoy-2}", false, time.Hour+time.Minute), igAttempt(b, "ICE{decoy-2}", false, 2*time.Hour+time.Minute),
		},
	}
	all := igDetect(facts)
	require.Len(t, all, 2)

	t.Run("a dismissed value stops raising shared_wrong; the other stays", func(t *testing.T) {
		got := m.ApplyDismissals(all, []m.Dismissal{{ExerciseID: exercise, TaskID: task, Kind: m.IntegritySharedWrong, Key: "ice{decoy-1}"}})
		for _, f := range got {
			for _, s := range f.Signals {
				if s.Kind == m.IntegritySharedWrong {
					require.Len(t, s.Answers, 1)
					require.Equal(t, "ice{decoy-2}", s.Answers[0].Value)
					require.Equal(t, 1, s.Count)
				}
			}
		}
	})
	t.Run("all values dismissed drops the signal and the solve", func(t *testing.T) {
		got := m.ApplyDismissals(all, []m.Dismissal{
			{ExerciseID: exercise, TaskID: task, Kind: m.IntegritySharedWrong, Key: "ice{decoy-1}"},
			{ExerciseID: exercise, TaskID: task, Kind: m.IntegritySharedWrong, Key: "ice{decoy-2}"},
			{ExerciseID: exercise, TaskID: task, Kind: m.IntegrityTooFast},
		})
		require.Empty(t, got)
	})
	t.Run("a pattern of another task or kind does not apply", func(t *testing.T) {
		got := m.ApplyDismissals(all, []m.Dismissal{
			{ExerciseID: exercise, TaskID: igID(), Kind: m.IntegrityTooFast},
			{ExerciseID: exercise, TaskID: task, Kind: m.IntegrityNoAccess},
		})
		require.Equal(t, all, got)
	})
	t.Run("burst and cross_flag cannot be dismissed", func(t *testing.T) {
		require.False(t, m.IntegrityBurst.Dismissible())
		require.False(t, m.IntegrityCrossFlag.Dismissible())
		require.True(t, m.IntegritySharedWrong.Dismissible())
	})
}

func TestNoLabUsesTheLabTrafficVerdict(t *testing.T) {
	team, task := igID(), igID()
	base := igSolve(team, "A", task, "medium", time.Hour, time.Hour)
	base.HasLab = true
	knock := igAt(10 * time.Minute)

	t.Run("untouched is a fact, even with a VPN session", func(t *testing.T) {
		s := base
		s.Lab, s.FirstVPN = m.LabUntouched, igAt(time.Minute)
		got := igDetect(m.IntegrityFacts{Solves: []m.IntegritySolve{s}})
		require.Len(t, got, 1)
		require.Equal(t, m.IntegrityNoLab, got[0].Signals[0].Kind)
		require.Zero(t, got[0].Signals[0].Extra)
	})
	t.Run("a knock that the lab never answered is evidence", func(t *testing.T) {
		s := base
		s.Lab, s.LabAttempted, s.LabFirstSeen = m.LabUntouched, true, knock
		got := igDetect(m.IntegrityFacts{Solves: []m.IntegritySolve{s}})
		require.Equal(t, 1, got[0].Signals[0].Extra)
		require.True(t, got[0].Signals[0].At.Equal(*knock))
	})
	t.Run("touched clears it even without a VPN session", func(t *testing.T) {
		s := base
		s.Lab = m.LabTouched
		require.Empty(t, igDetect(m.IntegrityFacts{Solves: []m.IntegritySolve{s}}))
	})
	t.Run("an unknown answer falls back on the VPN sessions", func(t *testing.T) {
		s := base
		s.Lab = ""
		require.Len(t, igDetect(m.IntegrityFacts{Solves: []m.IntegritySolve{s}}), 1)
		s.FirstVPN = igAt(time.Minute)
		require.Empty(t, igDetect(m.IntegrityFacts{Solves: []m.IntegritySolve{s}}))
	})
}
