package eventself

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestPublicPageResponseContainsOnlyPublishedContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID := uuid.Must(uuid.NewV7())
	publishedAt := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	page := eventUseCase.EventPageView{
		ID: uuid.Must(uuid.NewV7()), Slug: "rules", Title: "Published rules",
		Document: eventContentModel.Document{Blocks: []eventContentModel.Block{
			{ID: "published", Type: eventContentModel.BlockText, Text: "Published page content"},
		}},
		Visibility: eventContentModel.PageVisibilityPublic, Navigation: eventContentModel.PageNavigationNavbar, NavigationOrder: 4,
		Draft: &eventContentModel.PageDraft{
			Slug: "secret-rules", Title: "Unpublished rules",
			Document: eventContentModel.Document{Blocks: []eventContentModel.Block{
				{ID: "draft", Type: eventContentModel.BlockText, Text: "Unpublished page content"},
			}},
			Visibility: eventContentModel.PageVisibilityManager, Navigation: eventContentModel.PageNavigationNone,
			NavigationAfter: "first",
		},
		PublishedAt: &publishedAt, CreatedAt: publishedAt.Add(-time.Hour), UpdatedAt: publishedAt.Add(time.Hour),
	}
	h := NewEventSelfAPIHandler(fakeUseCase{page: func(_ context.Context, gotEventID uuid.UUID, slug string, access eventUseCase.ContentAccess) (eventUseCase.PublicEventPageView, error) {
		require.Equal(t, eventID, gotEventID)
		require.Equal(t, "rules", slug)
		require.Nil(t, access.UserID)
		return eventUseCase.PublicEventPageView{Page: page, Variables: map[string]any{"event.title": "Event title"}}, nil
	}}, nil)
	router := gin.New()
	router.GET("/api/events/:id/content/pages/:slug", h.publicPage)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/content/pages/rules", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	var body struct{ Data json.RawMessage }
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.JSONEq(t, `{
		"Page": {
			"Slug": "rules",
			"Title": "Published rules",
			"Document": {"blocks": [{"id": "published", "type": "text", "text": "Published page content"}]}
		},
		"Variables": {"event.title": "Event title"}
	}`, string(body.Data))
}
