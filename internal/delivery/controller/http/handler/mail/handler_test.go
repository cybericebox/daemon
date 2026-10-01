package mail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/cybericebox/daemon/internal/model/rbac"
	mailUseCase "github.com/cybericebox/daemon/internal/useCase/mail"
)

type passThrough struct{}

func (passThrough) RequirePermission(rbac.Permission) gin.HandlerFunc {
	return func(ctx *gin.Context) { ctx.Next() }
}

type fakeUseCase struct {
	testInput    *mailModel.SMTPInput
	testCalled   bool
	journalEvent uuid.UUID
	journalF     dispatchModel.ListDispatchesFilter
	manageDenied bool
	identity     mailModel.Identity
	sendingDom   string
	footer       json.RawMessage
	provider     mailModel.ProviderInput
	providerID   uuid.UUID
	enabled      bool
	order        []uuid.UUID
	testID       *uuid.UUID
}

func (f *fakeUseCase) GetPlatformMailSettings(context.Context) (mailUseCase.PlatformSettingsView, error) {
	return mailUseCase.PlatformSettingsView{}, nil
}
func (f *fakeUseCase) UpdatePlatformIdentity(_ context.Context, in mailModel.Identity, sendingDomain string, _ uuid.UUID) (mailUseCase.PlatformSettingsView, error) {
	f.identity, f.sendingDom = in, sendingDomain
	return mailUseCase.PlatformSettingsView{}, nil
}
func (f *fakeUseCase) UpdatePlatformFooter(_ context.Context, content json.RawMessage, _ uuid.UUID) (mailUseCase.PlatformSettingsView, error) {
	f.footer = content
	return mailUseCase.PlatformSettingsView{}, nil
}
func (f *fakeUseCase) PreviewPlatformFooter(_ context.Context, content json.RawMessage) (mailUseCase.FooterPreview, error) {
	f.footer = content
	return mailUseCase.FooterPreview{HTML: "<div>preview</div>", Text: "preview"}, nil
}
func (f *fakeUseCase) CreatePlatformProvider(_ context.Context, in mailModel.ProviderInput, _ uuid.UUID) (mailUseCase.PlatformSettingsView, error) {
	f.provider = in
	return mailUseCase.PlatformSettingsView{}, nil
}
func (f *fakeUseCase) UpdatePlatformProvider(_ context.Context, id uuid.UUID, in mailModel.ProviderInput, _ uuid.UUID) (mailUseCase.PlatformSettingsView, error) {
	f.providerID, f.provider = id, in
	return mailUseCase.PlatformSettingsView{}, nil
}
func (f *fakeUseCase) SetPlatformProviderEnabled(_ context.Context, id uuid.UUID, enabled bool, _ uuid.UUID) (mailUseCase.PlatformSettingsView, error) {
	f.providerID, f.enabled = id, enabled
	return mailUseCase.PlatformSettingsView{}, nil
}
func (f *fakeUseCase) DeletePlatformProvider(_ context.Context, id uuid.UUID) (mailUseCase.PlatformSettingsView, error) {
	f.providerID = id
	return mailUseCase.PlatformSettingsView{}, nil
}
func (f *fakeUseCase) ReorderPlatformProviders(_ context.Context, ids []uuid.UUID) (mailUseCase.PlatformSettingsView, error) {
	f.order = ids
	return mailUseCase.PlatformSettingsView{}, nil
}
func (f *fakeUseCase) TestPlatformProvider(_ context.Context, id *uuid.UUID, in *mailModel.SMTPInput, _ uuid.UUID) (mailUseCase.TestResult, error) {
	f.testCalled, f.testInput, f.testID = true, in, id
	return mailUseCase.TestResult{Sent: true, Recipient: "me@example.org"}, nil
}
func (f *fakeUseCase) GetEventMailSettings(context.Context, uuid.UUID) (mailUseCase.EventSettingsView, error) {
	return mailUseCase.EventSettingsView{}, nil
}
func (f *fakeUseCase) UpdateEventIdentity(_ context.Context, _ uuid.UUID, in mailModel.Identity, _ uuid.UUID) (mailUseCase.EventSettingsView, error) {
	f.identity = in
	return mailUseCase.EventSettingsView{}, nil
}
func (f *fakeUseCase) UpdateEventSMTP(context.Context, uuid.UUID, mailModel.SMTPInput, uuid.UUID) (mailUseCase.EventSettingsView, error) {
	return mailUseCase.EventSettingsView{}, nil
}
func (f *fakeUseCase) DeleteEventSMTP(context.Context, uuid.UUID) (mailUseCase.EventSettingsView, error) {
	return mailUseCase.EventSettingsView{}, nil
}
func (f *fakeUseCase) TestEventSMTP(context.Context, uuid.UUID, *mailModel.SMTPInput, uuid.UUID) (mailUseCase.TestResult, error) {
	return mailUseCase.TestResult{}, nil
}
func (f *fakeUseCase) ListEventMailJournal(_ context.Context, eventID uuid.UUID, filter dispatchModel.ListDispatchesFilter) ([]dispatchModel.DispatchDetail, int64, error) {
	f.journalEvent, f.journalF = eventID, filter
	return nil, 0, nil
}
func (f *fakeUseCase) RequireManageEvent(context.Context, uuid.UUID, uuid.UUID) error {
	if f.manageDenied {
		return authModel.ErrInsufficientPermission.Err()
	}
	return nil
}
func (f *fakeUseCase) RequireReadEvent(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func serve(uc *fakeUseCase, method, path, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(response.WithErrorHandler, func(c *gin.Context) {
		c.Request = c.Request.WithContext(rbac.ContextWithCurrentUserSession(c.Request.Context(), rbac.Claims{UserID: uuid.Must(uuid.NewV7()), Role: rbac.RoleUser}))
	})
	NewMailAPIHandler(uc, passThrough{}).Init(router.Group("/api"))
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestTestProvider_EmptyBodyTestsTheTransportInUse(t *testing.T) {
	uc := &fakeUseCase{}
	w := serve(uc, http.MethodPost, "/api/mail/settings/providers/test", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.True(t, uc.testCalled)
	require.Nil(t, uc.testInput)
	require.Nil(t, uc.testID)

	w = serve(uc, http.MethodPost, "/api/mail/settings/providers/test", `{"Host":"smtp.example.com","Port":465,"TLSMode":"tls"}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, uc.testInput)
	require.Equal(t, mailModel.TLSImplicit, uc.testInput.TLSMode)
}

func TestTestProvider_FormWithIDReusesTheStoredPassword(t *testing.T) {
	uc := &fakeUseCase{}
	id := uuid.Must(uuid.NewV7())
	w := serve(uc, http.MethodPost, "/api/mail/settings/providers/test", `{"ID":"`+id.String()+`","Host":"h.example","Port":587}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, uc.testID)
	require.Equal(t, id, *uc.testID)
	require.NotNil(t, uc.testInput)

	w = serve(uc, http.MethodPost, "/api/mail/settings/providers/"+id.String()+"/test", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, id, *uc.testID)
	require.Nil(t, uc.testInput, "a stored provider is tested as saved")
}

func TestProviderRequest_MapsAllFields(t *testing.T) {
	uc := &fakeUseCase{}
	w := serve(uc, http.MethodPost, "/api/mail/settings/providers", `{
		"Name":"Brevo","Host":"smtp-relay.brevo.com","Port":587,"TLSMode":"starttls","Username":"u","Password":"p",
		"DailyQuota":300,"MaxPerSecond":2.5,"Enabled":false,
		"Sender":{"Name":"CIB","Address":"n@mail.example"},"ReplyTo":{"Name":"Help","Address":"h@example.org"}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	in := uc.provider
	require.Equal(t, "Brevo", in.Name)
	require.Equal(t, "smtp-relay.brevo.com", in.Host)
	require.Equal(t, "p", in.Password)
	require.NotNil(t, in.DailyQuota)
	require.Equal(t, 300, *in.DailyQuota)
	require.NotNil(t, in.Enabled)
	require.False(t, *in.Enabled)
	require.Equal(t, mailModel.Identity{FromName: "CIB", FromAddress: "n@mail.example", ReplyToName: "Help", ReplyToAddress: "h@example.org"}, in.Identity)

	// Enabled omitted keeps the stored state.
	id := uuid.Must(uuid.NewV7())
	w = serve(uc, http.MethodPut, "/api/mail/settings/providers/"+id.String(), `{"Name":"Brevo","Host":"h","Port":587}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, id, uc.providerID)
	require.Nil(t, uc.provider.Enabled)
}

func TestProviderRoutes_EnableDeleteReorder(t *testing.T) {
	uc := &fakeUseCase{}
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	w := serve(uc, http.MethodPatch, "/api/mail/settings/providers/"+a.String()+"/enabled", `{"Enabled":true}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, a, uc.providerID)
	require.True(t, uc.enabled)

	w = serve(uc, http.MethodDelete, "/api/mail/settings/providers/"+b.String(), "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, b, uc.providerID)

	w = serve(uc, http.MethodPut, "/api/mail/settings/providers/order", `{"IDs":["`+b.String()+`","`+a.String()+`"]}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, []uuid.UUID{b, a}, uc.order)

	w = serve(uc, http.MethodPut, "/api/mail/settings/providers/order", `{"IDs":["nope"]}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	w = serve(uc, http.MethodDelete, "/api/mail/settings/providers/not-a-uuid", "")
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestEventJournal_ForcesEventAndKeepsFilters(t *testing.T) {
	uc := &fakeUseCase{}
	eventID := uuid.Must(uuid.NewV7())
	w := serve(uc, http.MethodGet, "/api/events/"+eventID.String()+"/manage/mail/journal?channel=email&result=error&transport=event", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, eventID, uc.journalEvent)
	require.Equal(t, "email", uc.journalF.Channel)
	require.Equal(t, "error", uc.journalF.Result)
	require.Equal(t, "event", uc.journalF.Transport)
}

func TestEventSMTP_WriteNeedsManageAccess(t *testing.T) {
	uc := &fakeUseCase{manageDenied: true}
	w := serve(uc, http.MethodPut, "/api/events/"+uuid.Must(uuid.NewV7()).String()+"/manage/mail/smtp", `{"Host":"h","Port":587}`)
	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestIdentity_PlatformAndEventMapBothParties(t *testing.T) {
	body := `{"Sender":{"Name":"Org","Address":"o@x.io"},"ReplyTo":{"Name":"Help","Address":"h@x.io"}}`
	want := mailModel.Identity{FromName: "Org", FromAddress: "o@x.io", ReplyToName: "Help", ReplyToAddress: "h@x.io"}

	uc := &fakeUseCase{}
	require.Equal(t, http.StatusOK, serve(uc, http.MethodPut, "/api/mail/settings/identity", body).Code)
	require.Equal(t, want, uc.identity)

	uc = &fakeUseCase{}
	path := "/api/events/" + uuid.Must(uuid.NewV7()).String() + "/manage/mail/identity"
	require.Equal(t, http.StatusOK, serve(uc, http.MethodPut, path, body).Code)
	require.Equal(t, want, uc.identity)

	uc = &fakeUseCase{manageDenied: true}
	require.Equal(t, http.StatusForbidden, serve(uc, http.MethodPut, path, body).Code)
}

func TestPlatformIdentity_PassesTheSendingDomain(t *testing.T) {
	uc := &fakeUseCase{}
	body := `{"Sender":{"Name":"","Address":""},"ReplyTo":{"Name":"","Address":""},"SendingDomain":"mail.cybericebox.com"}`
	require.Equal(t, http.StatusOK, serve(uc, http.MethodPut, "/api/mail/settings/identity", body).Code)
	require.Equal(t, "mail.cybericebox.com", uc.sendingDom)

	uc = &fakeUseCase{}
	path := "/api/events/" + uuid.Must(uuid.NewV7()).String() + "/manage/mail/identity"
	require.Equal(t, http.StatusOK, serve(uc, http.MethodPut, path, body).Code)
	require.Empty(t, uc.sendingDom, "an Event has no sending domain of its own")
}

func TestPlatformFooter_SaveAndPreviewPassTheDocument(t *testing.T) {
	doc := `{"root":{"type":"root","children":[]}}`
	uc := &fakeUseCase{}
	require.Equal(t, http.StatusOK, serve(uc, http.MethodPut, "/api/mail/settings/footer", `{"Content":`+doc+`}`).Code)
	require.JSONEq(t, doc, string(uc.footer))

	uc = &fakeUseCase{}
	w := serve(uc, http.MethodPost, "/api/mail/settings/footer/preview", `{"Content":`+doc+`}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "preview")
	require.JSONEq(t, doc, string(uc.footer))

	uc = &fakeUseCase{}
	require.Equal(t, http.StatusOK, serve(uc, http.MethodPut, "/api/mail/settings/footer", `{"Content":null}`).Code)
	require.Equal(t, "null", string(uc.footer), "null restores the default")
}
