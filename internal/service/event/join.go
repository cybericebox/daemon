package event

import (
	"context"
	"database/sql"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
)

type (
	IJoinRepository interface {
		GetEventJoinStatus(ctx context.Context, arg postgres.GetEventJoinStatusParams) (int32, error)
		CreateEventParticipant(ctx context.Context, arg postgres.CreateEventParticipantParams) error
	}
)

func (s *EventService) GetParticipantJoinEventStatus(ctx context.Context, eventID, userID uuid.UUID) (int32, error) {
	status, err := s.repository.GetEventJoinStatus(ctx, postgres.GetEventJoinStatusParams{
		EventID: eventID,
		UserID:  userID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.NoParticipationStatus, nil
		}

		return model.NoParticipationStatus, err
	}
	return status, nil
}

func (s *EventService) CreateJoinEventRequest(ctx context.Context, eventID, userID uuid.UUID, status int32) error {
	err := s.repository.CreateEventParticipant(ctx, postgres.CreateEventParticipantParams{
		EventID:        eventID,
		UserID:         userID,
		ApprovalStatus: status,
	})
	if err != nil {
		return err
	}
	return nil
}
