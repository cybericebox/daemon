package labview

import (
	"github.com/gofrs/uuid"

	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type (
	// StandDetailResponse is one team stand with the live state of each Lab.
	StandDetailResponse struct {
		TeamID                uuid.UUID                     `json:"TeamID"`
		TeamName              string                        `json:"TeamName"`
		Moderators            bool                          `json:"Moderators"`
		Status                string                        `json:"Status"`
		Reason                string                        `json:"Reason"`
		Generation            int32                         `json:"Generation"`
		LaboratoriesAvailable bool                          `json:"LaboratoriesAvailable"`
		Group                 eventUseCase.ManagedGroupView `json:"Group"`
		Labs                  []StandLabDetailResponse      `json:"Labs"`
	}

	StandLabDetailResponse struct {
		Lab           *eventUseCase.ManagedLabView        `json:"Lab"`
		Questions     []eventUseCase.StandLabQuestionView `json:"Questions"`
		ChallengeID   uuid.UUID                           `json:"ChallengeID"`
		ChallengeName string                              `json:"ChallengeName"`
		// Status is pending, ready, failed or removed.
		Status string `json:"Status"`
		Reason string `json:"Reason"`
		// Live is null while the Lab is not deployed yet or the agent did not answer (LiveUnavailable).
		Live            *StatusResponse `json:"Live"`
		LiveUnavailable bool            `json:"LiveUnavailable"`
	}
)

// StandDetail maps one team stand with the live state of each Lab.
func StandDetail(v eventUseCase.StandDetailView) StandDetailResponse {
	out := StandDetailResponse{
		TeamID: v.TeamID, TeamName: v.TeamName, Moderators: v.Moderators, Status: v.Status.String(), Reason: v.Reason,
		Group: v.Group, Generation: v.Generation, LaboratoriesAvailable: v.LaboratoriesAvailable, Labs: make([]StandLabDetailResponse, 0, len(v.Labs)),
	}
	for _, lab := range v.Labs {
		item := StandLabDetailResponse{Lab: lab.Lab, Questions: lab.Questions, ChallengeID: lab.ChallengeID, ChallengeName: lab.ChallengeName, Status: labReadinessString(lab.Readiness), Reason: lab.Reason, LiveUnavailable: lab.LiveUnavailable}
		if lab.Live != nil {
			live := Status(*lab.Live)
			item.Live = &live
		}
		out.Labs = append(out.Labs, item)
	}
	return out
}

func labReadinessString(r labBindingModel.Readiness) string {
	switch r {
	case labBindingModel.ReadinessReady:
		return "ready"
	case labBindingModel.ReadinessFailed:
		return "failed"
	case labBindingModel.ReadinessDestroyed:
		return "removed"
	default:
		return "pending"
	}
}
