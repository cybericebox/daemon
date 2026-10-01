package eventTeamModel

import (
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
)

func TestNewMakesCreatorCaptainAndFirstMember(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	eventID := uuid.Must(uuid.NewV7())
	captainID := uuid.Must(uuid.NewV7())
	team, err := New(eventID, captainID, "  Blue Team  ", "secure-join-code", now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if team.ID == uuid.Nil || team.Name != "Blue Team" || team.CaptainID != captainID || team.MemberCount != 1 {
		t.Fatalf("unexpected team: %+v", team)
	}
}

func TestCaptainActionsRejectNonCaptain(t *testing.T) {
	now := time.Now()
	captainID := uuid.Must(uuid.NewV7())
	team, err := New(uuid.Must(uuid.NewV7()), captainID, "Blue Team", "secure-join-code", now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	other := uuid.Must(uuid.NewV7())
	if err := team.Rename("Red Team", other, now); !errors.Is(err, ErrEventTeamCaptainRequired.Err()) {
		t.Fatalf("want captain error, got %v", err)
	}
	if err := team.RegenerateJoinCode("new-secure-join-code", nil, other, now); !errors.Is(err, ErrEventTeamCaptainRequired.Err()) {
		t.Fatalf("want captain error, got %v", err)
	}
	if err := team.TransferCaptain(other, other, now); !errors.Is(err, ErrEventTeamCaptainRequired.Err()) {
		t.Fatalf("want captain error, got %v", err)
	}
}

func TestCanAcceptMemberHonoursConfiguredLimit(t *testing.T) {
	team := EventTeam{MemberCount: 3}
	if err := team.CanAcceptMember(3); !errors.Is(err, ErrEventTeamFull.Err()) {
		t.Fatalf("want full error, got %v", err)
	}
	if err := team.CanAcceptMember(0); err != nil {
		t.Fatalf("unbounded team should accept: %v", err)
	}
}

func TestAdmission(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	team := EventTeam{MemberCount: 1}
	if team.Admitted(2) {
		t.Fatal("a team below the minimum must not be admitted")
	}
	team.SetAdmittedManually(true, now)
	if !team.Admitted(2) {
		t.Fatal("manual admission must admit the team")
	}
	solo := EventTeam{Individual: true, MemberCount: 1}
	if !solo.Admitted(5) {
		t.Fatal("an individual team is always admitted")
	}
	full := EventTeam{MemberCount: 2}
	if full.LockAdmissionBeforeShrink(2, false, now) {
		t.Fatal("admission must not lock before start")
	}
	if !full.LockAdmissionBeforeShrink(2, true, now) || !full.AdmissionLocked {
		t.Fatal("an admitted team must keep admission when shrunk after start")
	}
	full.MemberCount = 1
	if !full.Admitted(2) {
		t.Fatal("locked admission must survive a smaller roster")
	}
}

func TestNewManagedStartsWithEmptyRoster(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	captainID := uuid.Must(uuid.NewV7())
	team, err := NewManaged(uuid.Must(uuid.NewV7()), captainID, " Red ", "secure-join-code", now)
	if err != nil {
		t.Fatalf("NewManaged: %v", err)
	}
	if team.MemberCount != 0 || team.CaptainID != captainID || team.Name != "Red" {
		t.Fatalf("unexpected team: %+v", team)
	}
	if team.Admitted(2) {
		t.Fatal("an empty roster must not be admitted")
	}
	if _, err := NewManaged(uuid.Must(uuid.NewV7()), captainID, "x", "secure-join-code", now); !errors.Is(err, ErrEventTeamNameInvalid.Err()) {
		t.Fatalf("want name error, got %v", err)
	}
}

func TestRegenerateJoinCodeReplacesCodeAndExpiry(t *testing.T) {
	now := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	captainID := uuid.Must(uuid.NewV7())
	team, _ := New(uuid.Must(uuid.NewV7()), captainID, "Blue Team", "secure-join-code", now)
	expires := now.Add(time.Hour)
	if err := team.RegenerateJoinCode("another-secure-code", &expires, captainID, now); err != nil {
		t.Fatalf("RegenerateJoinCode: %v", err)
	}
	if team.JoinCode != "another-secure-code" || team.JoinCodeExpiresAt == nil || !team.JoinCodeExpiresAt.Equal(expires) {
		t.Fatalf("unexpected team: %+v", team)
	}
	if err := team.RegenerateJoinCode("short", nil, captainID, now); !errors.Is(err, ErrEventTeamJoinCodeInvalid.Err()) {
		t.Fatalf("want invalid code, got %v", err)
	}
	if err := team.RegenerateJoinCode("third-secure-code", nil, captainID, now); err != nil || team.JoinCodeExpiresAt != nil {
		t.Fatalf("regenerating without expiry must clear it: %v %+v", err, team.JoinCodeExpiresAt)
	}
}

func TestCheckJoinCodeActive(t *testing.T) {
	now := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	team := EventTeam{}
	if err := team.CheckJoinCodeActive(now); err != nil {
		t.Fatalf("a link without expiry never expires: %v", err)
	}
	expires := now.Add(time.Hour)
	team.JoinCodeExpiresAt = &expires
	if err := team.CheckJoinCodeActive(expires.Add(-time.Nanosecond)); err != nil {
		t.Fatalf("link must work before the expiry: %v", err)
	}
	if err := team.CheckJoinCodeActive(expires); !errors.Is(err, ErrEventTeamJoinCodeExpired.Err()) {
		t.Fatalf("link must expire at the expiry, got %v", err)
	}
}

func TestJoinCodeExpiryResolution(t *testing.T) {
	now := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	startAt := now.Add(48 * time.Hour)
	cases := map[JoinCodeExpiry]*time.Time{
		"": nil, JoinCodeExpiryNone: nil,
		JoinCodeExpiryDay:   ptr(now.Add(24 * time.Hour)),
		JoinCodeExpiryWeek:  ptr(now.Add(7 * 24 * time.Hour)),
		JoinCodeExpiryStart: ptr(startAt),
	}
	for expiry, want := range cases {
		got, err := expiry.ExpiresAt(now, startAt)
		if err != nil || (got == nil) != (want == nil) || (got != nil && !got.Equal(*want)) {
			t.Fatalf("%q: got %v, %v want %v", expiry, got, err, want)
		}
	}
	if _, err := JoinCodeExpiryStart.ExpiresAt(startAt, startAt); !errors.Is(err, ErrEventTeamJoinCodeExpiryInvalid.Err()) {
		t.Fatalf("start expiry after the start must be rejected, got %v", err)
	}
	if _, err := JoinCodeExpiry("month").ExpiresAt(now, startAt); !errors.Is(err, ErrEventTeamJoinCodeExpiryInvalid.Err()) {
		t.Fatalf("unknown expiry must be rejected, got %v", err)
	}
}

func ptr(t time.Time) *time.Time { return &t }
