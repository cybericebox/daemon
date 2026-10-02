// Package event is the HTTP delivery layer of the platform event tenancy:
// admin-only routes gated per-permission (events.*), plus the single public
// GET /events/upcoming read used by the platform landing page.
package event

import (
	"context"
	"errors"
	"fmt"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"io"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	utils "github.com/cybericebox/daemon/internal/delivery/controller/http/utils"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	inAppModel "github.com/cybericebox/daemon/internal/model/notification/inapp"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
	"github.com/cybericebox/daemon/pkg/labaccess"
	"github.com/cybericebox/daemon/pkg/pagination"
)

type (
	Handler struct {
		useCase IUseCase
		prot    IProtection
	}

	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}

	IUseCase interface {
		GetEventStands(ctx context.Context, eventID uuid.UUID) (eventUseCase.StandsView, error)
		UpdateEventStandSettings(ctx context.Context, eventID uuid.UUID, timing eventStandModel.Timing, by uuid.UUID) (eventUseCase.StandsView, error)
		RecreateTeamStand(ctx context.Context, eventID, teamID, by uuid.UUID) (eventUseCase.StandTeamView, error)
		GetTeamStandDetail(ctx context.Context, eventID, teamID uuid.UUID) (eventUseCase.StandDetailView, error)
		ResetStandDevice(ctx context.Context, eventID, teamID, challengeID uuid.UUID, device string) error
		RescueStandDevice(ctx context.Context, eventID, teamID, challengeID uuid.UUID, device string, enable bool) error
		ListModeratorsChallenges(ctx context.Context, eventID uuid.UUID) ([]eventUseCase.ModeratorsChallengeView, error)
		GetModeratorsChallengeLabStatus(ctx context.Context, eventID, challengeID uuid.UUID) (exerciseModel.LabDeployStatus, error)
		GetModeratorsVPNConfig(ctx context.Context, eventID, userID uuid.UUID) (string, error)
		OpenModeratorsLabLink(ctx context.Context, eventID, userID, challengeID uuid.UUID, device string, port int32) (labaccess.Link, error)
		ListModeratorsBoard(ctx context.Context, eventID uuid.UUID) ([]eventUseCase.OwnChallengeView, error)
		GetModeratorsTeam(ctx context.Context, eventID uuid.UUID) (eventUseCase.ModeratorsTeamView, error)
		SubmitModeratorsChallenge(ctx context.Context, eventID, userID, challengeID uuid.UUID, in eventUseCase.SubmitChallengeInput) (eventUseCase.SubmitChallengeResult, error)
		ListModeratorsChallengeSolves(ctx context.Context, eventID, challengeID, cursor uuid.UUID, pageSize int) (eventUseCase.ChallengeSolvesPage, error)
		StreamModeratorsChallengeAttachment(ctx context.Context, eventID, challengeID, fileID uuid.UUID) (io.ReadCloser, mediaModel.File, error)
		CreateEvent(ctx context.Context, in eventUseCase.CreateEventInput) (eventUseCase.EventView, error)
		GetEvent(ctx context.Context, id uuid.UUID) (eventUseCase.EventView, error)
		ListEvents(ctx context.Context, f eventUseCase.ListEventsFilter) (eventUseCase.EventsListResult, error)
		UpdateEvent(ctx context.Context, id uuid.UUID, in eventUseCase.UpdateEventInput, updatedBy uuid.UUID) (eventUseCase.EventView, error)
		GetEventPublicName(ctx context.Context, id uuid.UUID) (string, error)
		UpdateEventPublicName(ctx context.Context, id uuid.UUID, name string, updatedBy uuid.UUID) (string, error)
		GetEventLifecycle(ctx context.Context, id uuid.UUID) (eventUseCase.EventLifecycleView, error)
		UpdateEventLifecycle(ctx context.Context, id uuid.UUID, in eventUseCase.UpdateLifecycleInput, updatedBy uuid.UUID) (eventUseCase.EventLifecycleView, error)
		GetEventScoringProfile(ctx context.Context, id uuid.UUID) (eventUseCase.EventScoringProfileView, error)
		SetEventInfrastructure(ctx context.Context, id uuid.UUID, allowed bool, by uuid.UUID) (eventUseCase.EventView, error)
		UpdateEventScoringProfile(ctx context.Context, id uuid.UUID, in eventUseCase.UpdateEventScoringProfileInput, updatedBy uuid.UUID) (eventUseCase.EventScoringProfileView, error)
		ArchiveEvent(ctx context.Context, id uuid.UUID, updatedBy uuid.UUID) (eventUseCase.EventView, error)
		DeleteEvent(ctx context.Context, id uuid.UUID) error

		GetEventConfig(ctx context.Context, eventID uuid.UUID) (eventUseCase.EventConfigView, error)
		GetEventContent(ctx context.Context, eventID uuid.UUID) (eventUseCase.EventContentView, error)
		SaveLandingDraft(ctx context.Context, eventID uuid.UUID, document eventContentModel.Document) error
		PublishLanding(ctx context.Context, eventID uuid.UUID) error
		DiscardLandingDraft(ctx context.Context, eventID uuid.UUID) error
		GetLiveLayoutEditor(ctx context.Context, eventID uuid.UUID) (eventUseCase.LiveLayoutEditorView, error)
		SaveLiveLayoutDraft(ctx context.Context, eventID uuid.UUID, layout eventContentModel.LiveLayout) error
		PublishLiveLayout(ctx context.Context, eventID uuid.UUID) (eventContentModel.LiveLayout, error)
		UploadLiveLogo(ctx context.Context, eventID, userID uuid.UUID, reader io.Reader, size int64) (string, error)
		GetLiveScreenLink(ctx context.Context, eventID uuid.UUID) (*eventUseCase.LiveScreenLinkView, error)
		IssueLiveScreenLink(ctx context.Context, eventID, by uuid.UUID, expiry eventContentModel.LiveScreenExpiry) (eventUseCase.IssuedLiveScreenLinkView, error)
		RegenerateLiveScreenLink(ctx context.Context, eventID, by uuid.UUID) (eventUseCase.IssuedLiveScreenLinkView, error)
		RevokeLiveScreenLink(ctx context.Context, eventID uuid.UUID) error
		CreateEventPage(ctx context.Context, eventID uuid.UUID, in eventUseCase.EventPageInput) (eventUseCase.EventPageView, error)
		ListEventPages(ctx context.Context, eventID uuid.UUID) ([]eventUseCase.EventPageView, error)
		GetEventPage(ctx context.Context, eventID uuid.UUID, slug string) (eventUseCase.EventPageView, error)
		SaveEventPageDraft(ctx context.Context, eventID, pageID uuid.UUID, in eventUseCase.EventPageInput) (eventUseCase.EventPageView, error)
		PublishEventPage(ctx context.Context, eventID, pageID uuid.UUID) (eventUseCase.EventPageView, error)
		DiscardEventPageDraft(ctx context.Context, eventID, pageID uuid.UUID) error
		DeleteEventPage(ctx context.Context, eventID, pageID uuid.UUID) error
		UpdateEventConfig(ctx context.Context, eventID uuid.UUID, in eventUseCase.UpdateConfigInput, by uuid.UUID) (eventUseCase.EventConfigView, error)
		UpdateEventTheme(ctx context.Context, eventID uuid.UUID, brand, accent string, by uuid.UUID) (eventUseCase.EventConfigView, error)
		UploadEventBrandDraft(ctx context.Context, eventID, userID uuid.UUID, kind string, reader io.Reader, size int64) (uuid.UUID, error)
		SaveEventGeneral(ctx context.Context, eventID, userID uuid.UUID, input eventUseCase.EventGeneralInput) (eventUseCase.EventGeneralView, error)
		SaveEventAppearance(ctx context.Context, eventID, userID uuid.UUID, input eventUseCase.EventAppearanceInput) (eventUseCase.EventAppearanceView, error)
		StreamEventFavicon(ctx context.Context, eventID, fileID uuid.UUID) (io.ReadCloser, string, error)
		UploadEventLogo(ctx context.Context, eventID, userID uuid.UUID, reader io.Reader, size int64) (string, error)
		RemoveEventLogo(ctx context.Context, eventID uuid.UUID) error
		StreamEventLogo(ctx context.Context, eventID, fileID uuid.UUID) (io.ReadCloser, string, error)
		UploadEventPreviewPicture(ctx context.Context, eventID, userID uuid.UUID, reader io.Reader, size int64) (string, error)
		RemoveEventPreviewPicture(ctx context.Context, eventID, userID uuid.UUID) error
		StreamEventPreviewPicture(ctx context.Context, eventID, fileID uuid.UUID) (io.ReadCloser, string, error)
		UploadEventContentImage(ctx context.Context, eventID, userID uuid.UUID, reader io.Reader, size int64) (string, error)
		StreamEventContentImage(ctx context.Context, eventID, fileID uuid.UUID) (io.ReadCloser, string, error)
		GetParticipantForm(ctx context.Context, eventID uuid.UUID) (eventUseCase.ParticipantFormView, error)
		GetTeamFields(ctx context.Context, eventID uuid.UUID) (eventUseCase.ParticipantFormView, error)
		GetParticipantStaffFields(ctx context.Context, eventID, userID uuid.UUID) (eventUseCase.StaffFieldsView, error)
		UpdateParticipantStaffFields(ctx context.Context, eventID, userID, actorID uuid.UUID, values map[string]any) (eventUseCase.StaffFieldsView, error)
		GetTeamStaffFields(ctx context.Context, eventID, teamID uuid.UUID) (eventUseCase.StaffFieldsView, error)
		UpdateTeamStaffFields(ctx context.Context, eventID, teamID, actorID uuid.UUID, values map[string]any) (eventUseCase.StaffFieldsView, error)
		ConfigureTeamFields(ctx context.Context, eventID uuid.UUID, in eventUseCase.ConfigureParticipantFormInput) (eventUseCase.ParticipantFormView, error)
		ConfigureParticipantForm(ctx context.Context, eventID uuid.UUID, in eventUseCase.ConfigureParticipantFormInput) (eventUseCase.ParticipantFormView, error)
		ListParticipantFormAnswers(ctx context.Context, eventID uuid.UUID) ([]eventUseCase.ParticipantFormAnswerView, error)
		UploadAnswerFile(ctx context.Context, eventID, actorID uuid.UUID, scope eventFormModel.AnswerScope, fieldKey, name string, r io.Reader) (eventUseCase.AnswerFileView, error)
		StreamManagedAnswerFile(ctx context.Context, eventID, fileID uuid.UUID) (io.ReadCloser, eventUseCase.AnswerFileView, error)
		InviteParticipants(ctx context.Context, eventID, by uuid.UUID, entries []eventUseCase.ParticipantInvitationInput) ([]eventUseCase.ParticipantInvitationResult, error)
		InviteTeamMembers(ctx context.Context, eventID, teamID, by uuid.UUID, entries []eventUseCase.ParticipantInvitationInput) ([]eventUseCase.ParticipantInvitationResult, error)
		ResendParticipantInvitation(ctx context.Context, eventID, userID, by uuid.UUID) (eventUseCase.ParticipantInvitationResult, error)
		RevokeParticipantInvitation(ctx context.Context, eventID, userID, by uuid.UUID) error
		GetTeamProfile(ctx context.Context, eventID, teamID uuid.UUID) (eventUseCase.TeamProfileView, error)
		SetTeamAdmission(ctx context.Context, eventID, teamID uuid.UUID, admittedManually bool) (eventUseCase.TeamView, error)
		SetTeamHidden(ctx context.Context, eventID, teamID uuid.UUID, hidden bool) (eventUseCase.TeamView, error)
		GetListColumns(ctx context.Context, eventID uuid.UUID, list string) ([]eventUseCase.ListColumnView, error)
		PutListColumns(ctx context.Context, eventID uuid.UUID, list string, columns []eventUseCase.ListColumnView, by uuid.UUID) ([]eventUseCase.ListColumnView, error)
		CreateEventForm(ctx context.Context, eventID uuid.UUID, in eventUseCase.CreateEventFormInput) (eventUseCase.EventFormView, error)
		ListEventForms(ctx context.Context, eventID uuid.UUID) ([]eventUseCase.EventFormView, error)
		GetEventForm(ctx context.Context, eventID, formID uuid.UUID) (eventUseCase.EventFormView, error)
		UpdateEventForm(ctx context.Context, eventID, formID uuid.UUID, in eventUseCase.CreateEventFormInput) (eventUseCase.EventFormView, error)
		ListEventFormAnswers(ctx context.Context, eventID, formID uuid.UUID) ([]eventUseCase.EventFormAnswerView, error)
		ListEventFormDeliveries(ctx context.Context, eventID, formID uuid.UUID) ([]eventUseCase.EventFormDeliveryView, error)
		AssignEventForm(ctx context.Context, eventID, formID uuid.UUID, in eventUseCase.CreateEventFormAssignmentInput) error
		ListParticipants(ctx context.Context, f eventUseCase.ListParticipantsFilter) (eventUseCase.ParticipantsListResult, error)
		GetParticipantDetail(ctx context.Context, eventID, userID uuid.UUID) (eventUseCase.ParticipantDetailView, error)
		SetIndividualParticipantHidden(ctx context.Context, eventID, userID uuid.UUID, hidden bool) error
		ListTeams(ctx context.Context, f eventUseCase.ListTeamsFilter) (eventUseCase.TeamsListResult, error)
		ListParticipantsTable(ctx context.Context, q eventUseCase.ParticipantsTableQuery) (eventUseCase.ParticipantsTableResult, error)
		ListTeamsTable(ctx context.Context, q eventUseCase.TeamsTableQuery) (eventUseCase.TeamsTableResult, error)
		AssignParticipantToTeam(ctx context.Context, eventID, teamID, userID uuid.UUID) error
		CreateManagedTeam(ctx context.Context, eventID uuid.UUID, in eventUseCase.CreateManagedTeamInput) (eventUseCase.TeamView, error)
		CreateManagedTeams(ctx context.Context, eventID, by uuid.UUID, in []eventUseCase.BatchTeamInput, dryRun bool) (eventUseCase.BatchTeamsResult, error)
		UpdateManagedTeam(ctx context.Context, eventID, teamID uuid.UUID, in eventUseCase.UpdateManagedTeamInput) (eventUseCase.TeamView, error)
		RemoveParticipantFromTeam(ctx context.Context, eventID, teamID, userID uuid.UUID) error
		FormManagedTeam(ctx context.Context, eventID, teamID, moderatorID uuid.UUID) error
		DeleteManagedTeam(ctx context.Context, eventID, teamID uuid.UUID) error
		TransferManagedTeamCaptaincy(ctx context.Context, eventID, teamID, newCaptainID uuid.UUID) error
		AttachExercise(ctx context.Context, eventID uuid.UUID, in eventUseCase.AttachExerciseInput, by uuid.UUID) (eventUseCase.EventExerciseView, error)
		ListEventExercises(ctx context.Context, eventID uuid.UUID) ([]eventUseCase.EventExerciseView, error)
		ListPublishedExercisesForEvent(ctx context.Context, eventID uuid.UUID, search, infrastructure string, tags []string) ([]eventUseCase.PublishedExerciseChoice, error)
		ListEventCatalogTags(ctx context.Context, eventID uuid.UUID, prefix string, limit int) ([]eventUseCase.EventCatalogTag, error)
		GetPublishedExercisePreviewForEvent(ctx context.Context, eventID, versionID uuid.UUID, variant int) (eventUseCase.PublishedExercisePreview, error)
		ReplaceEventExercise(ctx context.Context, eventID, eventExerciseID uuid.UUID, in eventUseCase.ReplaceEventExerciseInput, by uuid.UUID) (eventUseCase.EventExerciseView, error)
		UpdateEventExercise(ctx context.Context, eventID, eventExerciseID uuid.UUID, versionID *uuid.UUID, by uuid.UUID) (eventUseCase.EventExerciseView, error)
		ForkEventExercise(ctx context.Context, eventID, eventExerciseID, by uuid.UUID) (eventUseCase.EventExerciseView, error)
		RevertEventExercise(ctx context.Context, eventID, eventExerciseID uuid.UUID) (eventUseCase.EventExerciseView, error)
		DetachEventExercise(ctx context.Context, eventID, eventExerciseID uuid.UUID, confirmed bool, by uuid.UUID) error
		UpdateChallengeHintCosts(ctx context.Context, eventID, eventExerciseID, challengeID uuid.UUID, costs []eventUseCase.HintCostInput) (eventUseCase.EventChallengeView, error)
		ListHintUnlocks(ctx context.Context, eventID uuid.UUID) ([]eventUseCase.HintUnlockView, error)
		ListEventChallenges(ctx context.Context, eventID, eventExerciseID uuid.UUID) ([]eventUseCase.EventChallengeView, error)
		UpdateEventChallenge(ctx context.Context, eventID, eventExerciseID, challengeID uuid.UUID, in eventUseCase.UpdateEventChallengeInput) (eventUseCase.EventChallengeView, error)
		BulkUpdateChallengeScoring(ctx context.Context, eventID, eventExerciseID uuid.UUID, in eventUseCase.BulkUpdateChallengeScoringInput) error
		ReorderEventChallenges(ctx context.Context, eventID, eventExerciseID uuid.UUID, in eventUseCase.ReorderEventChallengesInput) error
		CreateChallengeGroup(ctx context.Context, eventID uuid.UUID, in eventUseCase.CreateChallengeGroupInput) (eventUseCase.ChallengeGroupView, error)
		ListChallengeGroups(ctx context.Context, eventID uuid.UUID) ([]eventUseCase.ChallengeGroupView, error)
		UpdateChallengeGroup(ctx context.Context, eventID, groupID uuid.UUID, in eventUseCase.UpdateChallengeGroupInput) (eventUseCase.ChallengeGroupView, error)
		ReorderChallengeGroups(ctx context.Context, eventID uuid.UUID, in eventUseCase.ReorderChallengeGroupsInput) error
		ReorderGroupChallenges(ctx context.Context, eventID uuid.UUID, in eventUseCase.ReorderGroupChallengesInput) error
		SetEventExerciseVisibility(ctx context.Context, eventID, eventExerciseID uuid.UUID, published bool) error
		DeleteChallengeGroup(ctx context.Context, eventID, groupID uuid.UUID) error
		UpdateEventChallengeRelations(ctx context.Context, eventID, eventExerciseID, challengeID uuid.UUID, in eventUseCase.UpdateEventChallengeRelationsInput) error
		ApproveParticipant(ctx context.Context, eventID, userID, by uuid.UUID) error
		RejectParticipant(ctx context.Context, eventID, userID, by uuid.UUID) error
		ListEventManagers(ctx context.Context, eventID uuid.UUID) ([]eventUseCase.EventManagerView, error)
		SetEventManager(ctx context.Context, eventID uuid.UUID, in eventUseCase.SetEventManagerInput) (eventUseCase.EventManagerView, error)
		RemoveEventManager(ctx context.Context, eventID, userID uuid.UUID) error
		ListSolutionAttempts(ctx context.Context, f eventUseCase.ListSolutionAttemptsFilter) (eventUseCase.SolutionAttemptsListResult, error)
		ListNotificationSubscriptions(ctx context.Context, eventID uuid.UUID) ([]eventUseCase.NotificationSubscriptionView, error)
		UpsertNotificationSubscription(ctx context.Context, eventID uuid.UUID, in eventUseCase.UpsertNotificationSubscriptionInput) (eventUseCase.NotificationSubscriptionView, error)
		ResetNotificationSubscription(ctx context.Context, eventID uuid.UUID, signalType, channel string) error
		ListEventEmailTemplates(ctx context.Context, eventID uuid.UUID, filter emailModel.ListFilter) ([]eventUseCase.EventEmailTemplateView, error)
		CreateEventEmailTemplate(ctx context.Context, eventID uuid.UUID, in emailModel.CreateTemplateInput) (emailModel.EmailTemplate, error)
		GetEventEmailTemplate(ctx context.Context, eventID, templateID uuid.UUID) (eventUseCase.EventEmailTemplateView, error)
		CustomizeEventEmailTemplate(ctx context.Context, eventID, platformTemplateID, updatedBy uuid.UUID) (emailModel.EmailTemplate, error)
		SendEventEmailTemplateTest(ctx context.Context, eventID, templateID, userID uuid.UUID) (string, error)
		UpdateEventEmailTemplate(ctx context.Context, eventID uuid.UUID, in emailModel.UpdateTemplateInput) (emailModel.EmailTemplate, error)
		DeleteEventEmailTemplate(ctx context.Context, eventID, templateID uuid.UUID) error
		ResetEventEmailTemplateType(ctx context.Context, eventID uuid.UUID, notificationType string) error
		PublishEventEmailTemplate(ctx context.Context, eventID, templateID, updatedBy uuid.UUID) (emailModel.EmailTemplate, error)
		RollbackEventEmailTemplate(ctx context.Context, eventID, sourceID, updatedBy uuid.UUID) (emailModel.EmailTemplate, error)
		PreviewEventEmail(ctx context.Context, eventID uuid.UUID, in emailUseCase.PreviewInput) (emailUseCase.PreviewOutput, error)
		ListEventEmailPresets(ctx context.Context, eventID uuid.UUID) ([]emailModel.BlockPreset, error)
		StreamEventEmailImage(ctx context.Context, eventID, fileID uuid.UUID) (io.ReadCloser, mediaModel.File, error)
		UploadEventEmailImage(ctx context.Context, eventID, templateID, userID uuid.UUID, reader io.Reader, name string) (mediaModel.File, error)
		EventEmailBrandLogo(ctx context.Context, eventID uuid.UUID) (data []byte, contentType string, err error)
		ListEventInAppTemplates(ctx context.Context, eventID uuid.UUID, filter inAppModel.ListFilter) ([]eventUseCase.EventInAppTemplateView, error)
		CreateEventInAppTemplate(ctx context.Context, eventID uuid.UUID, in inAppModel.CreateTemplateInput) (inAppModel.InAppTemplate, error)
		GetEventInAppTemplate(ctx context.Context, eventID, templateID uuid.UUID) (eventUseCase.EventInAppTemplateView, error)
		CustomizeEventInAppTemplate(ctx context.Context, eventID, platformTemplateID, updatedBy uuid.UUID) (inAppModel.InAppTemplate, error)
		UpdateEventInAppTemplate(ctx context.Context, eventID uuid.UUID, in inAppModel.UpdateTemplateInput) (inAppModel.InAppTemplate, error)
		DeleteEventInAppTemplate(ctx context.Context, eventID, templateID uuid.UUID) error
		ResetEventInAppTemplateType(ctx context.Context, eventID uuid.UUID, notificationType string) error
		PublishEventInAppTemplate(ctx context.Context, eventID, templateID, updatedBy uuid.UUID) (inAppModel.InAppTemplate, error)
		RollbackEventInAppTemplate(ctx context.Context, eventID, sourceID, updatedBy uuid.UUID) (inAppModel.InAppTemplate, error)
		DecideSolutionAttempt(ctx context.Context, eventID, attemptID uuid.UUID, in eventUseCase.DecideSolutionAttemptInput) (eventUseCase.SolutionAttemptDecisionView, error)
		AnnulSolve(ctx context.Context, eventID, teamID, challengeID uuid.UUID, reason string, by uuid.UUID) (eventUseCase.AnnulSolveView, error)
		GetSolutionAttemptsStamp(ctx context.Context, eventID uuid.UUID) (eventUseCase.SolutionAttemptsStampView, error)
		GetManageResults(ctx context.Context, eventID uuid.UUID) (eventUseCase.ManageResultsView, error)
		GetResultsSettings(ctx context.Context, eventID uuid.UUID) (eventUseCase.ResultsSettingsView, error)
		UpdateResultsSettings(ctx context.Context, eventID uuid.UUID, visibility eventConfigModel.Visibility, in eventConfigModel.ResultsSettings, by uuid.UUID) (eventUseCase.ResultsSettingsView, error)
		SetResultsOpened(ctx context.Context, eventID uuid.UUID, opened bool, by uuid.UUID) (eventUseCase.ResultsSettingsView, error)
		RequireManageEvent(ctx context.Context, eventID, userID uuid.UUID) error
		RequireReadEvent(ctx context.Context, eventID, userID uuid.UUID) error
	}
)

func NewEventAPIHandler(useCase IUseCase, prot IProtection) *Handler {
	return &Handler{useCase: useCase, prot: prot}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	ev := router.Group("events")
	{
		ev.GET("", h.prot.RequirePermission(rbac.PermEventsRead), h.list)
		// Public: anonymous landing-page card (allowlisted in tools/checkroutes).
		ev.GET("upcoming", h.upcoming)
		ev.GET(":id/logo/:fileID", h.streamEventLogo)
		ev.GET(":id/favicon/:fileID", h.streamEventFavicon)
		ev.GET(":id/preview-picture/:fileID", h.streamEventPreviewPicture)
		ev.GET(":id/content-images/:fileID", h.streamEventContentImage)
		ev.POST("", h.prot.RequirePermission(rbac.PermEventsWrite), h.create)

		ev.GET(":id", h.prot.RequirePermission(rbac.PermEventsRead), h.get)
		ev.PUT(":id", h.prot.RequirePermission(rbac.PermEventsWrite), h.update)
		ev.POST(":id/archive", h.prot.RequirePermission(rbac.PermEventsWrite), h.archive)
		ev.DELETE(":id", h.prot.RequirePermission(rbac.PermEventsWrite), h.delete)

		ev.GET(":id/config", h.prot.RequirePermission(rbac.PermEventsRead), h.getConfig)
		ev.PUT(":id/config", h.prot.RequirePermission(rbac.PermEventsWrite), h.updateConfig)
		ev.PUT(":id/infrastructure", h.prot.RequirePermission(rbac.PermEventsWrite), h.setInfrastructure)
		ev.PUT(":id/theme", h.prot.RequirePermission(rbac.PermEventsWrite), h.updateTheme)
		ev.GET(":id/managers", h.prot.RequirePermission(rbac.PermEventsRead), h.listManagers)
		ev.PUT(":id/managers/:userID", h.prot.RequirePermission(rbac.PermEventsWrite), h.setManager)
		ev.DELETE(":id/managers/:userID", h.prot.RequirePermission(rbac.PermEventsWrite), h.removeManager)
		ev.GET(":id/participants", h.prot.RequirePermission(rbac.PermEventsRead), h.listParticipants)
		ev.GET(":id/solution-attempts", h.prot.RequirePermission(rbac.PermEventsSolutionAttemptsRead), h.listSolutionAttempts)
		ev.PATCH(":id/solution-attempts/:attemptID/decision", h.prot.RequirePermission(rbac.PermEventsSolutionAttemptsWrite), h.decideSolutionAttempt)
		ev.POST(":id/participants/:userID/approve", h.prot.RequirePermission(rbac.PermEventsWrite), h.approveParticipant)
		ev.POST(":id/participants/:userID/reject", h.prot.RequirePermission(rbac.PermEventsWrite), h.rejectParticipant)

		manage := ev.Group(":id/manage", h.prot.RequirePermission(rbac.PermSelf))
		manage.GET("access", h.requireRead, h.getManagementAccess)
		manage.GET("name", h.requireRead, h.getPublicName)
		manage.PUT("name", h.requireManage, h.updatePublicName)
		manage.GET("config", h.requireRead, h.getConfig)
		manage.GET("content", h.requireRead, h.getContent)
		manage.GET("content/variables", h.requireRead, h.getContentVariables)
		manage.PUT("content/landing", h.requireManage, h.saveLandingDraft)
		manage.POST("content/landing/publish", h.requireManage, h.publishLanding)
		manage.DELETE("content/landing/draft", h.requireManage, h.discardLandingDraft)
		manage.GET("content/live", h.requireRead, h.getLiveLayoutEditor)
		manage.GET("content/live/version", h.requireRead, h.getLiveLayoutVersion)
		manage.PUT("content/live", h.requireManage, h.saveLiveLayoutDraft)
		manage.POST("content/live/publish", h.requireManage, h.publishLiveLayout)
		manage.POST("content/live/logos", h.requireManage, h.uploadLiveLogo)
		manage.GET("content/live/screen-link", h.requireManage, h.getLiveScreenLink)
		manage.POST("content/live/screen-link", h.requireManage, h.issueLiveScreenLink)
		manage.POST("content/live/screen-link/regenerate", h.requireManage, h.regenerateLiveScreenLink)
		manage.DELETE("content/live/screen-link", h.requireManage, h.revokeLiveScreenLink)
		manage.GET("pages", h.requireRead, h.listEventPages)
		manage.POST("pages", h.requireManage, h.createEventPage)
		manage.GET("pages/:slug", h.requireRead, h.getEventPage)
		manage.PUT("pages/:pageID", h.requireManage, h.saveEventPageDraft)
		manage.POST("pages/:pageID/publish", h.requireManage, h.publishEventPage)
		manage.DELETE("pages/:pageID/draft", h.requireManage, h.discardEventPageDraft)
		manage.DELETE("pages/:pageID", h.requireManage, h.deleteEventPage)
		manage.PUT("config", h.requireManage, h.updateConfig)
		manage.PUT("theme", h.requireManage, h.updateTheme)
		manage.POST("brand-drafts/:kind", h.requireManage, h.uploadEventBrandDraft)
		manage.PUT("general", h.requireManage, h.saveEventGeneral)
		manage.PUT("appearance", h.requireManage, h.saveEventAppearance)
		manage.POST("logo", h.requireManage, h.uploadEventLogo)
		manage.DELETE("logo", h.requireManage, h.removeEventLogo)
		manage.POST("preview-picture", h.requireManage, h.uploadEventPreviewPicture)
		manage.POST("content-images", h.requireManage, h.uploadEventContentImage)
		manage.DELETE("preview-picture", h.requireManage, h.removeEventPreviewPicture)
		manage.GET("participant-form", h.requireRead, h.getParticipantForm)
		manage.PUT("participant-form", h.requireManage, h.configureParticipantForm)
		manage.GET("team-fields", h.requireRead, h.getTeamFields)
		manage.PUT("team-fields", h.requireManage, h.configureTeamFields)
		manage.GET("participant-form/answers", h.requireRead, h.listParticipantFormAnswers)
		manage.GET("participants/:userID/staff-fields", h.requireRead, h.getParticipantStaffFields)
		manage.PUT("participants/:userID/staff-fields", h.requireManage, h.updateParticipantStaffFields)
		manage.GET("teams/:teamID/staff-fields", h.requireRead, h.getTeamStaffFields)
		manage.PUT("teams/:teamID/staff-fields", h.requireManage, h.updateTeamStaffFields)
		manage.POST("answer-files", h.requireManage, h.uploadManagedAnswerFile)
		manage.GET("answer-files/:fileID", h.requireRead, h.downloadManagedAnswerFile)
		manage.GET("forms", h.requireRead, h.listForms)
		manage.POST("forms", h.requireManage, h.createForm)
		manage.GET("forms/:formID", h.requireRead, h.getForm)
		manage.PUT("forms/:formID", h.requireManage, h.updateForm)
		manage.GET("forms/:formID/answers", h.requireRead, h.listFormAnswers)
		manage.GET("forms/:formID/deliveries", h.requireRead, h.listFormDeliveries)
		manage.POST("forms/:formID/deliveries", h.requireManage, h.assignForm)
		manage.GET("lifecycle", h.requireRead, h.getLifecycle)
		manage.PUT("lifecycle", h.requireManage, h.updateLifecycle)
		manage.GET("scoring", h.requireRead, h.getScoringProfile)
		manage.PUT("scoring", h.requireManage, h.updateScoringProfile)
		manage.GET("participants", h.requireRead, h.listParticipants)
		manage.GET("participants/:userID", h.requireRead, h.getParticipantDetail)
		manage.POST("participants/invitations", h.requireManage, h.inviteParticipants)
		manage.POST("teams/:teamID/invitations", h.requireManage, h.inviteParticipants)
		manage.POST("participants/:userID/invitation/resend", h.requireManage, h.resendInvitation)
		manage.DELETE("participants/:userID/invitation", h.requireManage, h.revokeInvitation)
		manage.GET("list-columns/:list", h.requireRead, h.getListColumns)
		manage.PUT("list-columns/:list", h.requireManage, h.putListColumns)
		// Viewers read the journal without submitted answers and expected flags.
		manage.GET("solution-attempts", h.requireRead, h.listManageSolutionAttempts)
		manage.GET("solution-attempts/export.csv", h.requireManage, h.exportSolutionAttempts)
		manage.GET("solution-attempts/live", h.requireRead, h.liveSolutionAttempts)
		manage.POST("solution-attempts/annul", h.requireManage, h.annulSolve)
		manage.PATCH("solution-attempts/:attemptID/decision", h.requireManage, h.decideSolutionAttempt)
		// Moderators' own results: always live, every team (R4).
		manage.GET("results", h.requireRead, h.getManageResults)
		manage.GET("results/export.csv", h.requireRead, h.exportManageResults)
		manage.PUT("results/opened", h.requireManage, h.setResultsOpened)
		manage.GET("results-settings", h.requireRead, h.getResultsSettings)
		manage.PUT("results-settings", h.requireManage, h.updateResultsSettings)
		manage.GET("teams", h.requireRead, h.listTeams)
		manage.GET("teams/:teamID", h.requireRead, h.getTeamProfile)
		manage.POST("teams", h.requireManage, h.createManagedTeam)
		manage.POST("teams/batch", h.requireManage, h.createManagedTeams)
		manage.PUT("teams/:teamID", h.requireManage, h.updateManagedTeam)
		manage.DELETE("teams/:teamID", h.requireManage, h.deleteManagedTeam)
		manage.POST("teams/:teamID/members/:userID", h.requireManage, h.assignParticipantToTeam)
		manage.DELETE("teams/:teamID/members/:userID", h.requireManage, h.removeParticipantFromTeam)
		manage.PUT("teams/:teamID/captain/:userID", h.requireManage, h.transferManagedTeamCaptaincy)
		manage.PUT("teams/:teamID/admission", h.requireManage, h.setTeamAdmission)
		manage.PUT("teams/:teamID/hidden", h.requireManage, h.setTeamHidden)
		manage.POST("teams/:teamID/form", h.requireManage, h.formManagedTeam)
		manage.GET("notification-subscriptions", h.requireRead, h.listNotificationSubscriptions)
		manage.GET("notification-types", h.requireRead, h.listNotificationTypes)
		manage.PUT("notification-subscriptions", h.requireManage, h.upsertNotificationSubscription)
		manage.DELETE("notification-subscriptions/:signalType/:channel", h.requireManage, h.resetNotificationSubscription)
		manage.GET("notification-templates/email", h.requireRead, h.listEventEmailTemplates)
		manage.POST("notification-templates/email", h.requireManage, h.createEventEmailTemplate)
		// Static preview/image/logo segments sit beside the ":templateID" param
		// routes (as on the platform template API).
		manage.POST("notification-templates/email/preview", h.requireRead, middleware.RateLimitPerUser(previewRateLimit, time.Minute), h.previewEventEmail)
		manage.GET("notification-templates/email/presets", h.requireRead, h.listEventEmailPresets)
		manage.POST("notification-templates/email/:templateID/images", h.requireManage, h.uploadEventEmailImage)
		manage.GET("notification-templates/email/images/:fileID", h.requireRead, h.streamEventEmailImage)
		manage.GET("notification-templates/email/brand/logo", h.requireRead, h.eventEmailBrandLogo)
		manage.GET("notification-templates/email/:templateID", h.requireRead, h.getEventEmailTemplate)
		manage.PUT("notification-templates/email/:templateID", h.requireManage, h.updateEventEmailTemplate)
		manage.DELETE("notification-templates/email/:templateID", h.requireManage, h.deleteEventEmailTemplate)
		manage.DELETE("notification-templates/email/type/:notificationType", h.requireManage, h.resetEventEmailTemplateType)
		manage.POST("notification-templates/email/:templateID/publish", h.requireManage, h.publishEventEmailTemplate)
		manage.POST("notification-templates/email/:templateID/rollback", h.requireManage, h.rollbackEventEmailTemplate)
		manage.POST("notification-templates/email/:templateID/customize", h.requireManage, h.customizeEventEmailTemplate)
		manage.POST("notification-templates/email/:templateID/test", h.requireManage, h.testEventEmailTemplate)
		manage.GET("notification-templates/in-app", h.requireRead, h.listEventInAppTemplates)
		manage.POST("notification-templates/in-app", h.requireManage, h.createEventInAppTemplate)
		manage.GET("notification-templates/in-app/:templateID", h.requireRead, h.getEventInAppTemplate)
		manage.PUT("notification-templates/in-app/:templateID", h.requireManage, h.updateEventInAppTemplate)
		manage.DELETE("notification-templates/in-app/:templateID", h.requireManage, h.deleteEventInAppTemplate)
		manage.DELETE("notification-templates/in-app/type/:notificationType", h.requireManage, h.resetEventInAppTemplateType)
		manage.POST("notification-templates/in-app/:templateID/publish", h.requireManage, h.publishEventInAppTemplate)
		manage.POST("notification-templates/in-app/:templateID/rollback", h.requireManage, h.rollbackEventInAppTemplate)
		manage.POST("notification-templates/in-app/:templateID/customize", h.requireManage, h.customizeEventInAppTemplate)
		manage.GET("exercises", h.requireRead, h.listEventExercises)
		manage.GET("exercise-catalog", h.requireRead, h.listPublishedExercisesForEvent)
		manage.GET("exercise-catalog/tags", h.requireRead, h.listEventCatalogTags)
		manage.GET("exercise-catalog/:versionID", h.requireRead, h.getPublishedExercisePreviewForEvent)
		manage.POST("exercises", h.requireManage, h.attachExercise)
		manage.POST("exercises/:exerciseID/replace", h.requireManage, h.replaceEventExercise)
		manage.POST("exercises/:exerciseID/update", h.requireManage, h.updateEventExercise)
		manage.POST("exercises/:exerciseID/fork", h.requireManage, h.forkEventExercise)
		manage.POST("exercises/:exerciseID/revert", h.requireManage, h.revertEventExercise)
		manage.DELETE("exercises/:exerciseID", h.requireManage, h.detachEventExercise)
		manage.PUT("exercises/:exerciseID/challenges/:challengeID/hints", h.requireManage, h.updateChallengeHintCosts)
		manage.GET("hint-unlocks", h.requireRead, h.listHintUnlocks)
		manage.GET("exercises/:exerciseID/challenges", h.requireRead, h.listEventChallenges)
		manage.PUT("exercises/:exerciseID/challenges/:challengeID", h.requireManage, h.updateEventChallenge)
		manage.PUT("exercises/:exerciseID/challenges/scoring", h.requireManage, h.bulkUpdateChallengeScoring)
		manage.PUT("exercises/:exerciseID/challenges/:challengeID/relations", h.requireManage, h.updateEventChallengeRelations)
		manage.PUT("exercises/:exerciseID/challenges/order", h.requireManage, h.reorderEventChallenges)
		manage.PUT("exercises/:exerciseID/visibility", h.requireManage, h.setEventExerciseVisibility)
		manage.PUT("challenge-order", h.requireManage, h.reorderGroupChallenges)
		manage.GET("challenge-groups", h.requireRead, h.listChallengeGroups)
		manage.POST("challenge-groups", h.requireManage, h.createChallengeGroup)
		manage.PUT("challenge-groups/order", h.requireManage, h.reorderChallengeGroups)
		manage.PUT("challenge-groups/:groupID", h.requireManage, h.updateChallengeGroup)
		manage.DELETE("challenge-groups/:groupID", h.requireManage, h.deleteChallengeGroup)
		manage.POST("participants/:userID/approve", h.requireManage, h.approveParticipant)
		manage.POST("participants/:userID/reject", h.requireManage, h.rejectParticipant)
		manage.PUT("participants/:userID/visibility", h.requireManage, h.setIndividualParticipantVisibility)
		// Team stands: moderators only; participants never deploy or reconcile.
		manage.GET("labs", h.requireRead, h.getStands)
		manage.PUT("labs/settings", h.requireManage, h.updateStandSettings)
		manage.GET("labs/moderators/challenges", h.requireManage, h.listModeratorsChallenges)
		manage.GET("labs/moderators/challenges/:challengeID/lab", h.requireManage, h.moderatorsChallengeLab)
		manage.POST("labs/moderators/challenges/:challengeID/lab/link", h.requireManage, h.moderatorsLabLink)
		manage.GET("labs/moderators/vpn", h.requireManage, h.moderatorsVPNConfig)
		// The moderators board works without infrastructure (the team exists
		// on every event); owner and moderators only, like the VPN route.
		manage.GET("labs/moderators/board", h.requireManage, h.moderatorsBoard)
		manage.GET("labs/moderators/team", h.requireManage, h.moderatorsTeam)
		manage.POST("labs/moderators/challenges/:challengeID/submit", h.requireManage, h.moderatorsSubmit)
		manage.GET("labs/moderators/challenges/:challengeID/solves", h.requireManage, h.moderatorsSolves)
		manage.GET("labs/moderators/challenges/:challengeID/files/:fileID", h.requireManage, h.moderatorsChallengeFile)
		manage.POST("labs/:teamID/recreate", h.requireManage, h.recreateStand)
		// Live state of one team stand (launch queue, devices, snapshots, image warnings) and the
		// organizer actions on one device of a team Lab.
		manage.GET("labs/:teamID/detail", h.requireRead, h.getStandDetail)
		manage.POST("labs/:teamID/challenges/:challengeID/devices/:device/reset", h.requireManage, h.resetStandDevice)
		manage.POST("labs/:teamID/challenges/:challengeID/devices/:device/rescue", h.requireManage, h.rescueStandDevice)
	}
}

// listPublishedExercisesForEvent godoc
// @Summary List published catalog choices available to this event manager
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param search query string false "name or description substring"
// @Param infrastructure query string false "yes or no: only exercises with or without a lab topology"
// @Param tags query []string false "keep exercises with any of these tags (union); combined with the other filters by AND" collectionFormat(multi)
// @Success 200 {object} response.Response{data=[]publishedExerciseChoiceResponse}
// @Router /events/{id}/manage/exercise-catalog [get]
func (h *Handler) listPublishedExercisesForEvent(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	items, err := h.useCase.ListPublishedExercisesForEvent(ctx, eventID, ctx.Query("search"), ctx.Query("infrastructure"), ctx.QueryArray("tags"))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]publishedExerciseChoiceResponse, 0, len(items))
	for _, item := range items {
		tags := item.Tags
		if tags == nil {
			tags = []string{}
		}
		out = append(out, publishedExerciseChoiceResponse{ID: item.ID, Name: item.Name, Description: item.Description, PublishedVersionID: item.PublishedVersionID,
			Tags: tags, Scope: item.Scope, Infrastructure: item.Infrastructure, Attached: item.Attached})
	}
	response.AbortWithData(ctx, out)
}

// listEventCatalogTags godoc
// @Summary List tags of the exercises this event may attach (tag filter suggestions)
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param prefix query string false "tag prefix (case-insensitive, literal); empty returns the most used tags"
// @Param limit query int false "max tags (default 50, max 200)"
// @Success 200 {object} response.Response{data=[]eventCatalogTagResponse}
// @Failure 400 {object} response.Response
// @Router /events/{id}/manage/exercise-catalog/tags [get]
func (h *Handler) listEventCatalogTags(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	limit := 0
	if raw := ctx.Query("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			response.AbortWithBadRequest(ctx, fmt.Errorf("limit must be a positive integer"))
			return
		}
		limit = value
	}
	items, err := h.useCase.ListEventCatalogTags(ctx, eventID, ctx.Query("prefix"), limit)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]eventCatalogTagResponse, 0, len(items))
	for _, item := range items {
		out = append(out, eventCatalogTagResponse{Tag: item.Tag, ExerciseCount: item.ExerciseCount})
	}
	response.AbortWithData(ctx, out)
}

// getPublishedExercisePreviewForEvent returns a safe catalog summary for an
// event manager before attachment, without flags or infrastructure details.
func (h *Handler) getPublishedExercisePreviewForEvent(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	versionID, err := uuid.FromString(ctx.Param("versionID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	variant := 0
	if raw := ctx.Query("variant"); raw != "" {
		if variant, err = strconv.Atoi(raw); err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
	}
	preview, err := h.useCase.GetPublishedExercisePreviewForEvent(ctx, eventID, versionID, variant)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	tasks := make([]publishedExerciseTaskPreviewResponse, 0, len(preview.Tasks))
	for _, task := range preview.Tasks {
		tasks = append(tasks, publishedExerciseTaskPreviewResponse{Name: task.Name, Difficulty: string(task.Difficulty), HintCount: task.HintCount})
	}
	response.AbortWithData(ctx, publishedExercisePreviewResponse{ID: preview.ID, Name: preview.Name, Description: preview.Description, VersionID: preview.VersionID, VariantCount: preview.VariantCount, Variant: preview.Variant, Tasks: tasks})
}

// listEventExercises godoc
// @Summary List published exercise versions attached to an event
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=[]eventExerciseResponse}
// @Router /events/{id}/manage/exercises [get]
func (h *Handler) listEventExercises(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	items, err := h.useCase.ListEventExercises(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]eventExerciseResponse, 0, len(items))
	for _, item := range items {
		out = append(out, toEventExerciseResponse(item))
	}
	response.AbortWithData(ctx, out)
}

// attachExercise godoc
// @Summary Attach a published exercise version and materialize its board tasks
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body attachExerciseRequest true "published exercise version"
// @Success 200 {object} response.Response{data=eventExerciseResponse}
// @Router /events/{id}/manage/exercises [post]
func (h *Handler) attachExercise(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req attachExerciseRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	value, err := h.useCase.AttachExercise(ctx, id, req.toInput(), claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toEventExerciseResponse(value))
}

// replaceEventExercise godoc
// @Summary Create a corrected event-local revision from a published version
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param exerciseID path string true "event exercise attachment ID"
// @Param body body replaceEventExerciseRequest true "corrected published exercise version"
// @Success 200 {object} response.Response{data=eventExerciseResponse}
// @Router /events/{id}/manage/exercises/{exerciseID}/replace [post]
func (h *Handler) replaceEventExercise(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	exerciseID, err := uuid.FromString(ctx.Param("exerciseID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var req replaceEventExerciseRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	value, err := h.useCase.ReplaceEventExercise(ctx, id, exerciseID, req.toInput(), claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toEventExerciseResponse(value))
}

// listEventChallenges godoc
// @Summary List the materialized board challenges for an event bundle revision
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param exerciseID path string true "event exercise attachment ID"
// @Success 200 {object} response.Response{data=[]eventChallengeResponse}
// @Router /events/{id}/manage/exercises/{exerciseID}/challenges [get]
func (h *Handler) listEventChallenges(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	exerciseID, err := uuid.FromString(ctx.Param("exerciseID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	items, err := h.useCase.ListEventChallenges(ctx, eventID, exerciseID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]eventChallengeResponse, 0, len(items))
	for _, item := range items {
		out = append(out, toEventChallengeResponse(item))
	}
	response.AbortWithData(ctx, out)
}

// updateEventChallenge godoc
// @Summary Configure an active event board challenge
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param exerciseID path string true "event exercise attachment ID"
// @Param challengeID path string true "event challenge ID"
// @Param body body updateEventChallengeRequest true "board challenge settings"
// @Success 200 {object} response.Response{data=eventChallengeResponse}
// @Router /events/{id}/manage/exercises/{exerciseID}/challenges/{challengeID} [put]
func (h *Handler) updateEventChallenge(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	exerciseID, err := uuid.FromString(ctx.Param("exerciseID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var req updateEventChallengeRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	value, err := h.useCase.UpdateEventChallenge(ctx, eventID, exerciseID, challengeID, req.toInput())
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toEventChallengeResponse(value))
}

// updateEventChallengeRelations godoc
// @Summary Set a challenge group and prerequisites for an active board task
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param exerciseID path string true "event exercise attachment ID"
// @Param challengeID path string true "event challenge ID"
// @Param body body updateEventChallengeRelationsRequest true "challenge relations"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/exercises/{exerciseID}/challenges/{challengeID}/relations [put]
func (h *Handler) updateEventChallengeRelations(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	exerciseID, err := uuid.FromString(ctx.Param("exerciseID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var req updateEventChallengeRelationsRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err = h.useCase.UpdateEventChallengeRelations(ctx, eventID, exerciseID, challengeID, req.toInput()); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, nil)
}

// reorderEventChallenges godoc
// @Summary Replace the display order of all challenges in an active bundle
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param exerciseID path string true "event exercise attachment ID"
// @Param body body reorderEventChallengesRequest true "complete ordered challenge IDs"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/exercises/{exerciseID}/challenges/order [put]
func (h *Handler) reorderEventChallenges(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	exerciseID, err := uuid.FromString(ctx.Param("exerciseID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var req reorderEventChallengesRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err = h.useCase.ReorderEventChallenges(ctx, eventID, exerciseID, req.toInput()); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, nil)
}

// reorderGroupChallenges godoc
// @Summary Replace the board order of every challenge in one group (across sets)
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body reorderGroupChallengesRequest true "group (null = no group) and its complete ordered challenge IDs"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/challenge-order [put]
func (h *Handler) reorderGroupChallenges(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req reorderGroupChallengesRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.ReorderGroupChallenges(ctx, eventID, req.toInput()); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, nil)
}

// setEventExerciseVisibility godoc
// @Summary Show or hide every task of one set on the participant board (a set is all-or-nothing)
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param exerciseID path string true "event exercise attachment ID"
// @Param body body setEventExerciseVisibilityRequest true "visibility"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/exercises/{exerciseID}/visibility [put]
func (h *Handler) setEventExerciseVisibility(ctx *gin.Context) {
	eventID, exerciseID, _, ok := parseEventExerciseParams(ctx)
	if !ok {
		return
	}
	var req setEventExerciseVisibilityRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.SetEventExerciseVisibility(ctx, eventID, exerciseID, req.Published); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// listChallengeGroups godoc
// @Summary List event-local challenge groups
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=[]challengeGroupResponse}
// @Router /events/{id}/manage/challenge-groups [get]
func (h *Handler) listChallengeGroups(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	groups, err := h.useCase.ListChallengeGroups(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]challengeGroupResponse, 0, len(groups))
	for _, group := range groups {
		out = append(out, toChallengeGroupResponse(group))
	}
	response.AbortWithData(ctx, out)
}

// createChallengeGroup godoc
// @Summary Create an event-local challenge group
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body createChallengeGroupRequest true "challenge group"
// @Success 200 {object} response.Response{data=challengeGroupResponse}
// @Router /events/{id}/manage/challenge-groups [post]
func (h *Handler) createChallengeGroup(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req createChallengeGroupRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	group, err := h.useCase.CreateChallengeGroup(ctx, eventID, req.toInput())
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toChallengeGroupResponse(group))
}

// reorderChallengeGroups godoc
// @Summary Replace the display order of all event-local challenge groups
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body reorderChallengeGroupsRequest true "complete ordered group IDs"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/challenge-groups/order [put]
func (h *Handler) reorderChallengeGroups(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req reorderChallengeGroupsRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.ReorderChallengeGroups(ctx, eventID, req.toInput()); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, nil)
}

// updateChallengeGroup godoc
// @Summary Rename or move an event-local challenge group
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param groupID path string true "challenge group ID"
// @Param body body updateChallengeGroupRequest true "challenge group"
// @Success 200 {object} response.Response{data=challengeGroupResponse}
// @Router /events/{id}/manage/challenge-groups/{groupID} [put]
func (h *Handler) updateChallengeGroup(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	groupID, err := uuid.FromString(ctx.Param("groupID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var req updateChallengeGroupRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	group, err := h.useCase.UpdateChallengeGroup(ctx, eventID, groupID, req.toInput())
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toChallengeGroupResponse(group))
}

// deleteChallengeGroup godoc
// @Summary Delete an event-local challenge group
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param groupID path string true "challenge group ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/challenge-groups/{groupID} [delete]
func (h *Handler) deleteChallengeGroup(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	groupID, err := uuid.FromString(ctx.Param("groupID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err = h.useCase.DeleteChallengeGroup(ctx, eventID, groupID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, nil)
}

// listTeams godoc
// @Summary List teams in an event
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param search query string false "team name search"
// @Param admission query string false "admitted | notAdmitted"
// @Param filters query string false "JSON array of filters [{Key, Op: contains|any|bool|present|range, Value, Values, Type, From, To}]; Key is a form field or a column (@name, @captain, @captainPending, @members, @status, @created, @pending)"
// @Param page query int false "offset mode: page number (enables sortBy/sortDir and returns an offset page)"
// @Param sortBy query string false "offset mode: column key or form field key"
// @Param sortDir query string false "offset mode: asc | desc"
// @Param cursor query string false "last team ID"
// @Param pageSize query int false "page size"
// @Success 200 {object} response.Response{data=pagination.CursorPage[teamResponse]}
// @Router /events/{id}/manage/teams [get]
func (h *Handler) listTeams(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	page, err := utils.GetCursorPaginationParams(ctx)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	search, err := listSearch(ctx)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var admitted *bool
	switch ctx.Query("admission") {
	case "":
	case "admitted":
		admitted = new(bool)
		*admitted = true
	case "notAdmitted":
		admitted = new(bool)
	default:
		response.AbortWithBadRequest(ctx, errors.New("invalid admission filter"))
		return
	}
	fields, err := eventUseCase.ParseAnswerFilters(ctx.Query("filters"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if ctx.Query("page") != "" {
		h.listTeamsTable(ctx, id, search, fields)
		return
	}
	res, err := h.useCase.ListTeams(ctx, eventUseCase.ListTeamsFilter{EventID: id, Search: search, Admitted: admitted, Fields: fields, Cursor: page.Cursor, PageSize: page.PageSize})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	items := make([]teamResponse, 0, len(res.Teams))
	for _, team := range res.Teams {
		items = append(items, toTeamResponse(team))
	}
	response.AbortWithData(ctx, pagination.NewCursorPage(items, res.HasMore, res.NextCursor, res.Total))
}

// getLifecycle godoc
// @Summary  Get an event's lifecycle configuration
// @Tags     events
// @Produce  json
// @Param    id  path  string  true  "event ID"
// @Success  200  {object}  response.Response{data=lifecycleResponse}
// @Failure  400  {object}  response.Response
// @Router   /events/{id}/manage/lifecycle [get]
//
// Its preceding requireManage middleware authorizes the caller for exactly
// this :id.
func (h *Handler) getLifecycle(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventLifecycle(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toLifecycleResponse(v))
}

// updateLifecycle godoc
// @Summary  Update an event's lifecycle configuration
// @Tags     events
// @Accept   json
// @Produce  json
// @Param    id    path  string                  true  "event ID"
// @Param    body  body  updateLifecycleRequest  true  "lifecycle fields"
// @Success  200  {object}  response.Response{data=lifecycleResponse}
// @Failure  400  {object}  response.Response
// @Router   /events/{id}/manage/lifecycle [put]
func (h *Handler) updateLifecycle(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req updateLifecycleRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.UpdateEventLifecycle(ctx, id, req.toInput(), claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toLifecycleResponse(v))
}

func (h *Handler) getParticipantForm(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetParticipantForm(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toParticipantFormResponse(v))
}

func (h *Handler) configureParticipantForm(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req configureParticipantFormRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.ConfigureParticipantForm(ctx, id, req.toInput())
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toParticipantFormResponse(v))
}

func (h *Handler) listParticipantFormAnswers(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	values, err := h.useCase.ListParticipantFormAnswers(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]participantFormAnswerResponse, 0, len(values))
	for _, value := range values {
		out = append(out, toParticipantFormAnswerResponse(value))
	}
	response.AbortWithData(ctx, out)
}

// listForms godoc
// @Summary List event forms
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/forms [get]
func (h *Handler) listForms(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	values, err := h.useCase.ListEventForms(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, values)
}

// createForm godoc
// @Summary Create an event form
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body eventFormSwaggerRequest true "form"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/forms [post]
func (h *Handler) createForm(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req createEventFormRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	value, err := h.useCase.CreateEventForm(ctx, id, req.toInput())
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, value)
}

// getForm godoc
// @Summary Get the current manager-facing event form version
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param formID path string true "form ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/forms/{formID} [get]
func (h *Handler) getForm(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	formID, err := uuid.FromString(ctx.Param("formID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	value, err := h.useCase.GetEventForm(ctx, id, formID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, value)
}

// updateForm godoc
// @Summary Publish a new immutable version of an event form
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param formID path string true "form ID"
// @Param body body eventFormSwaggerRequest true "form"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/forms/{formID} [put]
func (h *Handler) updateForm(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	formID, err := uuid.FromString(ctx.Param("formID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var req createEventFormRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	value, err := h.useCase.UpdateEventForm(ctx, id, formID, req.toInput())
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, value)
}

// listFormAnswers godoc
// @Summary List individual answers for an event form
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param formID path string true "form ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/forms/{formID}/answers [get]
func (h *Handler) listFormAnswers(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	formID, err := uuid.FromString(ctx.Param("formID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	values, err := h.useCase.ListEventFormAnswers(ctx, id, formID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, values)
}

// listFormDeliveries godoc
// @Summary List individual deliveries for an event form
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param formID path string true "form ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/forms/{formID}/deliveries [get]
func (h *Handler) listFormDeliveries(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	formID, err := uuid.FromString(ctx.Param("formID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	values, err := h.useCase.ListEventFormDeliveries(ctx, id, formID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, values)
}

// assignForm godoc
// @Summary Create an event form delivery assignment
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param formID path string true "form ID"
// @Param body body eventFormAssignmentSwaggerRequest true "assignment"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/forms/{formID}/deliveries [post]
func (h *Handler) assignForm(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	formID, err := uuid.FromString(ctx.Param("formID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var req assignEventFormRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err = h.useCase.AssignEventForm(ctx, id, formID, req.toInput()); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, gin.H{"ok": true})
}

// getScoringProfile godoc
// @Summary Get the event scoring profile
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=scoringProfileResponse}
// @Router /events/{id}/manage/scoring [get]
func (h *Handler) getScoringProfile(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventScoringProfile(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toScoringProfileResponse(v))
}

// updateScoringProfile godoc
// @Summary Update the event default scoring profile
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body updateEventScoringProfileRequest true "complete scoring profile"
// @Success 200 {object} response.Response{data=scoringProfileResponse}
// @Router /events/{id}/manage/scoring [put]
func (h *Handler) updateScoringProfile(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req updateEventScoringProfileRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.UpdateEventScoringProfile(ctx, id, req.toInput(), claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toScoringProfileResponse(v))
}

// bulkUpdateChallengeScoring godoc
// @Summary Set or clear a scoring override for selected active board challenges
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param exerciseID path string true "event exercise attachment ID"
// @Param body body bulkUpdateChallengeScoringRequest true "selected challenge IDs and optional complete override"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/exercises/{exerciseID}/challenges/scoring [put]
func (h *Handler) bulkUpdateChallengeScoring(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	exerciseID, err := uuid.FromString(ctx.Param("exerciseID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var req bulkUpdateChallengeScoringRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	in := eventUseCase.BulkUpdateChallengeScoringInput{ChallengeIDs: req.ChallengeIDs}
	if req.Override != nil {
		profile := req.Override.profile()
		in.Override = &profile
	}
	if err = h.useCase.BulkUpdateChallengeScoring(ctx, eventID, exerciseID, in); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, gin.H{"updated": len(req.ChallengeIDs)})
}

func (h *Handler) requireManage(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	if err := h.useCase.RequireManageEvent(ctx, id, claims.UserID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
}

func (h *Handler) requireRead(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	if err := h.useCase.RequireReadEvent(ctx, id, claims.UserID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
}

// getManagementAccess reports write capability for this event only. The read
// middleware has already hidden missing memberships behind a 403 response.
func (h *Handler) getManagementAccess(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	err := h.useCase.RequireManageEvent(ctx, id, claims.UserID)
	if err != nil && !errors.Is(err, eventManagerModel.ErrEventManagementForbidden.Err()) {
		response.AbortWithError(ctx, err)
		return
	}
	// InfrastructureAllowed (the admin's creation-time decision) lets the
	// manager shell show «Стенди» without widening the public event info.
	event, getErr := h.useCase.GetEvent(ctx, id)
	if getErr != nil {
		response.AbortWithError(ctx, getErr)
		return
	}
	response.AbortWithData(ctx, gin.H{"CanManage": err == nil, "InfrastructureAllowed": event.InfrastructureAllowed})
}

// list godoc
// @Summary  List platform events (cursor page)
// @Tags     events
// @Produce  json
// @Param    search    query  string  false  "tag/name substring"
// @Param    cursor    query  string  false  "id of the last row of the previous page"
// @Param    pageSize  query  int     false  "page size"
// @Success  200  {object}  response.Response{data=pagination.CursorPage[eventResponse]}
// @Failure  400  {object}  response.Response
// @Router   /events [get]
func (h *Handler) list(ctx *gin.Context) {
	if ctx.Query("page") != "" {
		page, err := utils.GetOffsetPaginationParams(ctx)
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		res, err := h.useCase.ListEvents(ctx, eventUseCase.ListEventsFilter{
			Search: ctx.Query("search"), Status: ctx.Query("status"), Page: page.Page, PageSize: page.PageSize,
			SortBy: ctx.Query("sortBy"), SortDir: ctx.Query("sortDir"),
		})
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		items := make([]eventResponse, 0, len(res.Events))
		for _, e := range res.Events {
			items = append(items, toResponse(e))
		}
		response.AbortWithData(ctx, pagination.NewOffsetPage(items, res.Total, page.Page, page.PageSize))
		return
	}
	page, err := utils.GetCursorPaginationParams(ctx)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	res, err := h.useCase.ListEvents(ctx, eventUseCase.ListEventsFilter{
		Search:   ctx.Query("search"),
		Cursor:   page.Cursor,
		PageSize: page.PageSize,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	items := make([]eventResponse, 0, len(res.Events))
	for _, e := range res.Events {
		items = append(items, toResponse(e))
	}
	out := pagination.NewCursorPage(items, res.HasMore, res.NextCursor, res.Total)
	response.AbortWithData(ctx, out)
}

// create godoc
// @Summary  Create a platform event
// @Tags     events
// @Accept   json
// @Produce  json
// @Param    body  body  createEventRequest  true  "event fields"
// @Success  200  {object}  response.Response{data=eventResponse}
// @Failure  400  {object}  response.Response
// @Router   /events [post]
func (h *Handler) create(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req createEventRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.CreateEvent(ctx, eventUseCase.CreateEventInput{
		Tag: req.Tag, Name: req.Name, AvailableFrom: req.AvailableFrom, ArchiveAt: nullableRequestTime(req.ArchiveAt), CreatedBy: claims.UserID,
		InfrastructureAllowed: req.InfrastructureAllowed,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toResponse(v))
}

// get godoc
// @Summary  Get one platform event
// @Tags     events
// @Produce  json
// @Param    id  path  string  true  "event ID"
// @Success  200  {object}  response.Response{data=eventResponse}
// @Failure  400  {object}  response.Response
// @Router   /events/{id} [get]
func (h *Handler) get(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEvent(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toResponse(v))
}

// update godoc
// @Summary  Update a platform event (tag/name/window)
// @Tags     events
// @Accept   json
// @Produce  json
// @Param    id    path  string              true  "event ID"
// @Param    body  body  updateEventRequest  true  "event fields"
// @Success  200  {object}  response.Response{data=eventResponse}
// @Failure  400  {object}  response.Response
// @Router   /events/{id} [put]
func (h *Handler) update(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req updateEventRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.UpdateEvent(ctx, id, eventUseCase.UpdateEventInput{
		Tag: req.Tag, Name: req.Name, AvailableFrom: req.AvailableFrom, ArchiveAt: nullableRequestTime(req.ArchiveAt),
	}, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toResponse(v))
}

// setInfrastructure godoc
// @Summary  Allow or forbid infrastructure challenges (only before publication)
// @Tags     events
// @Accept   json
// @Produce  json
// @Param    id    path  string                   true  "event ID"
// @Param    body  body  setInfrastructureRequest  true  "flag"
// @Success  200  {object}  response.Response{data=eventResponse}
// @Failure  409  {object}  response.Response
// @Router   /events/{id}/infrastructure [put]
func (h *Handler) setInfrastructure(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req setInfrastructureRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.SetEventInfrastructure(ctx, id, req.InfrastructureAllowed, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toResponse(v))
}

// archive godoc
// @Summary  Archive a platform event (collapse the window end to now)
// @Tags     events
// @Produce  json
// @Param    id  path  string  true  "event ID"
// @Success  200  {object}  response.Response{data=eventResponse}
// @Failure  400  {object}  response.Response
// @Router   /events/{id}/archive [post]
func (h *Handler) archive(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.ArchiveEvent(ctx, id, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toResponse(v))
}

// delete godoc
// @Summary  Delete a platform event
// @Tags     events
// @Produce  json
// @Param    id  path  string  true  "event ID"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Router   /events/{id} [delete]
func (h *Handler) delete(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	if err := h.useCase.DeleteEvent(ctx, id); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// getConfig godoc
// @Summary  Get an event's config
// @Tags     events
// @Produce  json
// @Param    id  path  string  true  "event ID"
// @Success  200  {object}  response.Response{data=configResponse}
// @Failure  400  {object}  response.Response
// @Router   /events/{id}/config [get]
func (h *Handler) getConfig(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventConfig(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out, ok := h.configResponse(ctx, id, v)
	if !ok {
		return
	}
	response.AbortWithData(ctx, out)
}

// updateConfig godoc
// @Summary  Update an event's config
// @Tags     events
// @Accept   json
// @Produce  json
// @Param    id    path  string               true  "event ID"
// @Param    body  body  updateConfigRequest  true  "config fields"
// @Success  200  {object}  response.Response{data=configResponse}
// @Failure  400  {object}  response.Response
// @Router   /events/{id}/config [put]
func (h *Handler) updateConfig(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req updateConfigRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.UpdateEventConfig(ctx, id, req.toInput(), claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out, ok := h.configResponse(ctx, id, v)
	if !ok {
		return
	}
	response.AbortWithData(ctx, out)
}

// updateTheme godoc
// @Summary Update an event's brand and optional accent
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body updateThemeRequest true "theme inputs"
// @Success 200 {object} response.Response{data=configResponse}
// @Router /events/{id}/theme [put]
func (h *Handler) updateTheme(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req updateThemeRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.UpdateEventTheme(ctx, id, req.Brand, req.Accent, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out, ok := h.configResponse(ctx, id, v)
	if !ok {
		return
	}
	response.AbortWithData(ctx, out)
}

// listParticipants godoc
// @Summary  List an event's participants (cursor page)
// @Tags     events
// @Produce  json
// @Param    id        path   string  true   "event ID"
// @Param    status    query  int     false  "participant status filter"
// @Param    search    query  string  false  "name, email or pseudonym search"
// @Param    filters   query  string  false  "JSON array of filters [{Key, Op: contains|any|bool|present|range, Value, Values, Type, From, To}]; Key is a form field or a column (@name, @email, @pseudonym, @status, @invitation, @team, @date)"
// @Param    page      query  int     false  "offset mode: page number (enables sortBy/sortDir and returns an offset page)"
// @Param    sortBy    query  string  false  "offset mode: column key or form field key"
// @Param    sortDir   query  string  false  "offset mode: asc | desc"
// @Param    cursor    query  string  false  "id of the last row of the previous page"
// @Param    pageSize  query  int     false  "page size"
// @Success  200  {object}  response.Response{data=pagination.CursorPage[participantResponse]}
// @Failure  400  {object}  response.Response
// @Router   /events/{id}/participants [get]
func (h *Handler) listParticipants(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	page, err := utils.GetCursorPaginationParams(ctx)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var status *participantModel.Status
	if raw := ctx.Query("status"); raw != "" {
		s, sErr := strconv.Atoi(raw)
		if sErr != nil {
			response.AbortWithBadRequest(ctx, sErr)
			return
		}
		st := participantModel.Status(s)
		status = &st
	}
	kind := participantRepo.Kind(ctx.Query("kind"))
	search, err := listSearch(ctx)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	fields, err := eventUseCase.ParseAnswerFilters(ctx.Query("filters"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if ctx.Query("page") != "" {
		h.listParticipantsTable(ctx, id, status, kind, search, fields)
		return
	}
	res, err := h.useCase.ListParticipants(ctx, eventUseCase.ListParticipantsFilter{
		EventID:  id,
		Status:   status,
		Kind:     kind,
		Search:   search,
		Fields:   fields,
		Cursor:   page.Cursor,
		PageSize: page.PageSize,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	items := make([]participantResponse, 0, len(res.Participants))
	for _, p := range res.Participants {
		items = append(items, toParticipantResponse(p))
	}
	response.AbortWithData(ctx, participantsPageResponse{
		CursorPage: pagination.NewCursorPage(items, res.HasMore, res.NextCursor, res.Total),
		Counts:     participantCountsResponse{Participants: res.Counts.Participants, Applications: res.Counts.Applications, Invitations: res.Counts.Invitations},
	})
}

// approveParticipant godoc
// @Summary  Approve a pending participant
// @Tags     events
// @Produce  json
// @Param    id      path  string  true  "event ID"
// @Param    userID  path  string  true  "participant user ID"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Router   /events/{id}/participants/{userID}/approve [post]
func (h *Handler) approveParticipant(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	userID, ok := parseUserID(ctx)
	if !ok {
		return
	}
	if err := h.useCase.ApproveParticipant(ctx, id, userID, claims.UserID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// rejectParticipant godoc
// @Summary  Reject a pending participant
// @Tags     events
// @Produce  json
// @Param    id      path  string  true  "event ID"
// @Param    userID  path  string  true  "participant user ID"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Router   /events/{id}/participants/{userID}/reject [post]
func (h *Handler) rejectParticipant(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	userID, ok := parseUserID(ctx)
	if !ok {
		return
	}
	if err := h.useCase.RejectParticipant(ctx, id, userID, claims.UserID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// parseEventID extracts and validates the :id path param.
func parseEventID(ctx *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, false
	}
	return id, true
}

// parseUserID extracts and validates the :userID path param.
func parseUserID(ctx *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.FromString(ctx.Param("userID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, false
	}
	return id, true
}

// configResponse adds the admin-owned infrastructure flag (stored on the
// event row, never written through the config) to a config response.
func (h *Handler) configResponse(ctx *gin.Context, eventID uuid.UUID, v eventUseCase.EventConfigView) (configResponse, bool) {
	event, err := h.useCase.GetEvent(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return configResponse{}, false
	}
	out := toConfigResponse(v)
	out.InfrastructureAllowed = event.InfrastructureAllowed
	return out, true
}

func parseEventExerciseParams(ctx *gin.Context) (eventID, exerciseID, userID uuid.UUID, ok bool) {
	claims, found := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !found {
		response.AbortWithUnauthenticated(ctx)
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	if eventID, ok = parseEventID(ctx); !ok {
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	exerciseID, err := uuid.FromString(ctx.Param("exerciseID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	return eventID, exerciseID, claims.UserID, true
}

// updateEventExercise godoc
// @Summary «Оновити»: switch the attachment in place to the latest (or given) published version, keeping event overrides
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param exerciseID path string true "event exercise attachment ID"
// @Param body body updateEventExerciseRequest false "target version (default: latest published)"
// @Success 200 {object} response.Response{data=eventExerciseResponse}
// @Router /events/{id}/manage/exercises/{exerciseID}/update [post]
func (h *Handler) updateEventExercise(ctx *gin.Context) {
	eventID, exerciseID, userID, ok := parseEventExerciseParams(ctx)
	if !ok {
		return
	}
	var req updateEventExerciseRequest
	if ctx.Request.ContentLength != 0 {
		if err := ctx.ShouldBindJSON(&req); err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
	}
	value, err := h.useCase.UpdateEventExercise(ctx, eventID, exerciseID, req.ExerciseVersionID, userID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toEventExerciseResponse(value))
}

// forkEventExercise godoc
// @Summary «Налаштувати під захід»: switch to the event's own copy of the catalog exercise
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param exerciseID path string true "event exercise attachment ID"
// @Success 200 {object} response.Response{data=eventExerciseResponse}
// @Router /events/{id}/manage/exercises/{exerciseID}/fork [post]
func (h *Handler) forkEventExercise(ctx *gin.Context) {
	eventID, exerciseID, userID, ok := parseEventExerciseParams(ctx)
	if !ok {
		return
	}
	value, err := h.useCase.ForkEventExercise(ctx, eventID, exerciseID, userID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toEventExerciseResponse(value))
}

// revertEventExercise godoc
// @Summary «Повернути оригінал»: switch a fork attachment back to its catalog source
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param exerciseID path string true "event exercise attachment ID"
// @Success 200 {object} response.Response{data=eventExerciseResponse}
// @Router /events/{id}/manage/exercises/{exerciseID}/revert [post]
func (h *Handler) revertEventExercise(ctx *gin.Context) {
	eventID, exerciseID, _, ok := parseEventExerciseParams(ctx)
	if !ok {
		return
	}
	value, err := h.useCase.RevertEventExercise(ctx, eventID, exerciseID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toEventExerciseResponse(value))
}

// detachEventExercise godoc
// @Summary Detach an exercise from the event (confirm=true when teams already attempted it)
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param exerciseID path string true "event exercise attachment ID"
// @Param confirm query bool false "confirm detaching an attempted exercise"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/exercises/{exerciseID} [delete]
func (h *Handler) detachEventExercise(ctx *gin.Context) {
	eventID, exerciseID, userID, ok := parseEventExerciseParams(ctx)
	if !ok {
		return
	}
	if err := h.useCase.DetachEventExercise(ctx, eventID, exerciseID, ctx.Query("confirm") == "true", userID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// updateChallengeHintCosts godoc
// @Summary Override (or reset with null) hint costs of one board challenge
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param exerciseID path string true "event exercise attachment ID"
// @Param challengeID path string true "event challenge ID"
// @Param body body updateHintCostsRequest true "costs"
// @Success 200 {object} response.Response{data=eventChallengeResponse}
// @Router /events/{id}/manage/exercises/{exerciseID}/challenges/{challengeID}/hints [put]
func (h *Handler) updateChallengeHintCosts(ctx *gin.Context) {
	eventID, exerciseID, _, ok := parseEventExerciseParams(ctx)
	if !ok {
		return
	}
	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var req updateHintCostsRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	costs := make([]eventUseCase.HintCostInput, 0, len(req.Costs))
	for _, item := range req.Costs {
		costs = append(costs, eventUseCase.HintCostInput{HintID: item.HintID, Cost: item.Cost})
	}
	value, err := h.useCase.UpdateChallengeHintCosts(ctx, eventID, exerciseID, challengeID, costs)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toEventChallengeResponse(value))
}

// listHintUnlocks godoc
// @Summary Who unlocked which hint, when and for how much
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=[]hintUnlockResponse}
// @Router /events/{id}/manage/hint-unlocks [get]
func (h *Handler) listHintUnlocks(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	items, err := h.useCase.ListHintUnlocks(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]hintUnlockResponse, 0, len(items))
	for _, item := range items {
		out = append(out, hintUnlockResponse(item))
	}
	response.AbortWithData(ctx, out)
}

// previewRateLimit is how many template previews one user may request per minute: the editor asks on edits,
// a person types far slower than this.
const previewRateLimit = 60
