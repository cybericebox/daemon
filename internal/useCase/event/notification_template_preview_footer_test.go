package event_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
)

func testEventFooter() mailModel.Footer {
	return mailModel.Footer{HTML: `<div><a href="mailto:org@uni.edu">org@uni.edu</a></div>`, Text: "—\norg@uni.edu"}
}

type eventFooters struct{ asked *uuid.UUID }

func (f *eventFooters) Footer(_ context.Context, eventID *uuid.UUID) (mailModel.Footer, error) {
	f.asked = eventID
	return testEventFooter(), nil
}

func TestPreviewEventEmail_EndsWithEventFooter(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newTemplateUC(q, newTemplateMediaFake())
	footers := &eventFooters{}
	uc.SetEmailFooters(footers)
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, time.Now().UTC()), nil)
	q.EXPECT().ListEmailBlockPresets(gomock.Any()).Return(nil, nil)

	out, err := uc.PreviewEventEmail(context.Background(), eventID, emailUseCase.PreviewInput{
		NotificationType: "participant.approval_registration.approved",
		Subject:          "Hi",
		Body:             json.RawMessage(`[{"type":"divider"}]`),
	})
	require.NoError(t, err)
	require.Equal(t, &eventID, footers.asked)
	require.Contains(t, out.HTML, testEventFooter().HTML)
}
