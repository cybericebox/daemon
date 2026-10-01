package eventContentModel

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
)

var linkNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func TestNewLiveScreenLinkStoresOnlyTheHash(t *testing.T) {
	eventID, by := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	link, token, err := NewLiveScreenLink(eventID, by, linkNow, LiveScreenExpiryDay, nil)
	if err != nil {
		t.Fatalf("NewLiveScreenLink: %v", err)
	}
	if !LiveScreenTokenWellFormed(token) || len(token) < 40 {
		t.Fatalf("token %q is not a 256-bit base64url value", token)
	}
	if !bytes.Equal(link.TokenHash, HashLiveScreenToken(token)) || bytes.Contains(link.TokenHash, []byte(token)) {
		t.Fatal("the link must keep the token hash only")
	}
	if link.EventID != eventID || link.CreatedBy != by || !link.CreatedAt.Equal(linkNow) || !link.ExpiresAt.Equal(linkNow.Add(24*time.Hour)) || !link.Active(linkNow) {
		t.Fatalf("link = %+v", link)
	}
	_, other, _ := NewLiveScreenLink(eventID, by, linkNow, LiveScreenExpiryWeek, nil)
	if other == token {
		t.Fatal("tokens must be random")
	}
}

func TestLiveScreenLinkExpiryChoices(t *testing.T) {
	finish := linkNow.Add(5 * time.Hour)
	far := linkNow.Add(LiveScreenLinkMaxTTL + 24*time.Hour)
	for _, tc := range []struct {
		expiry LiveScreenExpiry
		finish *time.Time
		want   time.Time
	}{
		{LiveScreenExpiryWeek, nil, linkNow.Add(7 * 24 * time.Hour)},
		{LiveScreenExpiryEventEnd, &finish, finish},
		{LiveScreenExpiryEventEnd, &far, linkNow.Add(LiveScreenLinkMaxTTL)},
	} {
		link, _, err := NewLiveScreenLink(uuid.Nil, uuid.Nil, linkNow, tc.expiry, tc.finish)
		if err != nil || link.ExpiresAt == nil || !link.ExpiresAt.Equal(tc.want) {
			t.Fatalf("%s: expires %v, want %v (%v)", tc.expiry, link.ExpiresAt, tc.want, err)
		}
	}
	unlimited, _, err := NewLiveScreenLink(uuid.Nil, uuid.Nil, linkNow, LiveScreenExpiryNone, nil)
	if err != nil || unlimited.ExpiresAt != nil || !unlimited.Active(linkNow.Add(365*24*time.Hour)) {
		t.Fatalf("no expiry = %+v, %v", unlimited, err)
	}
	past := linkNow.Add(-time.Minute)
	for _, tc := range []struct {
		expiry LiveScreenExpiry
		finish *time.Time
	}{{LiveScreenExpiryEventEnd, nil}, {LiveScreenExpiryEventEnd, &past}, {"month", nil}} {
		if _, _, err := NewLiveScreenLink(uuid.Nil, uuid.Nil, linkNow, tc.expiry, tc.finish); !errors.Is(err, ErrLiveScreenLinkExpiryInvalid.Err()) {
			t.Fatalf("%s accepted: %v", tc.expiry, err)
		}
	}
}

func TestLiveScreenLinkRegenerateKeepsTheExpiry(t *testing.T) {
	link, token, _ := NewLiveScreenLink(uuid.Must(uuid.NewV7()), uuid.Nil, linkNow, LiveScreenExpiryDay, nil)
	next, nextToken, err := link.Regenerate(uuid.Nil, linkNow.Add(time.Hour))
	if err != nil || next.ID == link.ID || nextToken == token || !next.ExpiresAt.Equal(*link.ExpiresAt) || next.EventID != link.EventID {
		t.Fatalf("regenerated = %+v, %v", next, err)
	}
}

func TestLiveScreenLinkExpiresAndRevokes(t *testing.T) {
	link, _, _ := NewLiveScreenLink(uuid.Nil, uuid.Nil, linkNow, LiveScreenExpiryDay, nil)
	if link.Active(linkNow.Add(24 * time.Hour)) {
		t.Fatal("an expired link must not open the screen")
	}
	link.Revoke(linkNow.Add(time.Minute))
	first := *link.RevokedAt
	link.Revoke(linkNow.Add(2 * time.Minute))
	if link.Active(linkNow) || !link.RevokedAt.Equal(first) {
		t.Fatalf("revoke = %v", link.RevokedAt)
	}
}

func TestLiveScreenTokenWellFormed(t *testing.T) {
	for _, bad := range []string{"", "short", "!!!!", string(make([]byte, 43))} {
		if LiveScreenTokenWellFormed(bad) {
			t.Fatalf("%q accepted", bad)
		}
	}
}
