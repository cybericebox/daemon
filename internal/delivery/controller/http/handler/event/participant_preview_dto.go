package event

import (
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/labview"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"time"
)

// previewBoardItemResponse reuses the safe normal board projection and adds
// owned preview identities; the catalog Task is never a response DTO.
type previewBoardItemResponse struct {
	moderatorsBoardItemResponse
	EventExerciseID        uuid.UUID                       `json:"EventExerciseID"`
	Lab                    *labview.ParticipantLabResponse `json:"Lab"`
	MaxAttempts            *int32                          `json:"MaxAttempts"`
	AttemptsLeft           *int32                          `json:"AttemptsLeft"`
	HintCostTotal          int32                           `json:"HintCostTotal"`
	StageID                *uuid.UUID                      `json:"StageID"`
	Closed                 bool                            `json:"Closed"`
	Practice               bool                            `json:"Practice"`
	PreviewSelectionID     uuid.UUID                       `json:"PreviewSelectionID"`
	PreviewRunID           *uuid.UUID                      `json:"PreviewRunID"`
	PreviewDynamic         bool                            `json:"PreviewDynamic"`
	PreviewExpiresAt       *time.Time                      `json:"PreviewExpiresAt"`
	PreviewAttempts        int                             `json:"PreviewAttempts"`
	PreviewCorrectAttempts int                             `json:"PreviewCorrectAttempts"`
	PreviewCanStart        bool                            `json:"PreviewCanStart"`
}
type previewAvailabilityResponse struct {
	Preparing int64 `json:"Preparing"`
	Ready     int64 `json:"Ready"`
	Available int64 `json:"Available"`
	Failed    int64 `json:"Failed"`
	Total     int64 `json:"Total"`
}
type previewBoardResponse struct {
	Challenges   []previewBoardItemResponse  `json:"Challenges"`
	Availability previewAvailabilityResponse `json:"Availability"`
	Stages       []struct{}                  `json:"Stages"`
	CurrentStage *struct{}                   `json:"CurrentStage"`
	ServerNow    time.Time                   `json:"ServerNow"`
}

func toPreviewBoardResponse(v eventUseCase.PreviewBoard) previewBoardResponse {
	out := previewBoardResponse{Challenges: []previewBoardItemResponse{}, Stages: []struct{}{}, ServerNow: v.ServerNow, Availability: previewAvailabilityResponse(v.Availability)}
	for _, task := range v.Challenges {
		out.Challenges = append(out.Challenges, previewBoardItemResponse{moderatorsBoardItemResponse: toModeratorsBoardItemResponse(task.OwnChallengeView), EventExerciseID: task.EventExerciseID, Lab: labview.ParticipantLab(task.Lab), MaxAttempts: task.MaxAttempts, AttemptsLeft: task.AttemptsLeft, HintCostTotal: task.HintCostTotal, StageID: task.StageID, Closed: task.Closed, Practice: task.Practice, PreviewSelectionID: task.PreviewSelectionID, PreviewRunID: task.PreviewRunID, PreviewDynamic: task.PreviewDynamic, PreviewExpiresAt: task.PreviewExpiresAt, PreviewAttempts: task.PreviewAttempts, PreviewCorrectAttempts: task.PreviewCorrectAttempts, PreviewCanStart: task.PreviewCanStart})
	}
	return out
}
