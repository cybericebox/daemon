package event

import (
	"encoding/json"
	"testing"
	"time"

	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestTaskBoardResponsesOmitContentUpdateMetadata(t *testing.T) {
	updatedAt := time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC)
	for _, at := range []*time.Time{nil, &updatedAt} {
		view := eventUseCase.OwnChallengeView{ContentUpdatedAt: at}
		preview := toPreviewBoardResponse(eventUseCase.PreviewBoard{Challenges: []eventUseCase.PreviewChallenge{{OwnChallengeView: view}}})
		for name, value := range map[string]any{"moderators": toModeratorsBoardItemResponse(view), "preview": preview.Challenges[0]} {
			t.Run(name, func(t *testing.T) {
				body, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(body, &fields); err != nil {
					t.Fatal(err)
				}
				if _, exists := fields["ContentUpdatedAt"]; exists {
					t.Fatalf("%s task exposes content update metadata: %s", name, body)
				}
			})
		}
	}
}
