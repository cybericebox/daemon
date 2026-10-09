package labview

import eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"

// ParticipantLabResponse preserves the frozen PascalCase nullable wire shape.
type ParticipantLabResponse eventUseCase.ParticipantLabView

func ParticipantLab(v *eventUseCase.ParticipantLabView) *ParticipantLabResponse {
	if v == nil {
		return nil
	}
	out := ParticipantLabResponse(*v)
	return &out
}
