package event

import (
	"context"
	"fmt"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"sort"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	"github.com/cybericebox/daemon/internal/model/rbac"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

// ContentAccess carries the optional caller identity for a public event-page
// read. Public routes can safely pass RolePublic with a nil user.
type ContentAccess struct {
	UserID *uuid.UUID
	Role   rbac.Role
}

// PublicEventContentView is the renderer-neutral content available outside
// management screens. Variables are deliberately filtered before delivery.
type PublicEventContentView struct {
	Landing   eventContentModel.Document
	Live      eventContentModel.LiveLayout
	Variables map[string]any
}

type PublicEventPageView struct {
	Page      EventPageView
	Variables map[string]any
}

type NavigationPageView struct {
	Slug            string `json:"Slug"`
	Title           string `json:"Title"`
	NavigationOrder int32  `json:"NavigationOrder"`
}

// ListNavigationPages exposes titles and routes only for pages the current
// audience can enter. Documents and hidden page names stay in management.
func (u *EventUseCase) ListNavigationPages(ctx context.Context, eventID uuid.UUID, access ContentAccess) ([]NavigationPageView, error) {
	info, err := u.GetEventInfo(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if info.Status == eventModel.LifecycleNotPublished || info.Status == eventModel.LifecycleWithdrawn {
		return nil, eventModel.ErrEventNotFound.Err()
	}
	pages, err := u.ListEventPages(ctx, eventID)
	if err != nil {
		return nil, err
	}
	approved := false
	if access.UserID != nil {
		participant, participantErr := u.participants.Get(ctx, eventID, *access.UserID)
		approved = participantErr == nil && participant.Status == participantModel.StatusApproved
	}
	return projectNavigationPages(pages, approved), nil
}

func projectNavigationPages(pages []EventPageView, approved bool) []NavigationPageView {
	sort.Slice(pages, func(i, j int) bool {
		if pages[i].NavigationOrder == pages[j].NavigationOrder {
			return pages[i].Slug < pages[j].Slug
		}
		return pages[i].NavigationOrder < pages[j].NavigationOrder
	})
	out := make([]NavigationPageView, 0, len(pages))
	for _, page := range pages {
		if page.PublishedAt == nil || page.Navigation != eventContentModel.PageNavigationNavbar {
			continue
		}
		if page.Visibility == eventContentModel.PageVisibilityPublic || (page.Visibility == eventContentModel.PageVisibilityParticipant && approved) {
			out = append(out, NavigationPageView{Slug: page.Slug, Title: page.Title, NavigationOrder: page.NavigationOrder})
		}
	}
	return out
}

// GetEventContent combines stored content configuration with current,
// event-wide values. Authorization is deliberately left to the caller: the
// manager and public/participant routes apply their different read policies
// before exposing this view.
func (u *EventUseCase) GetEventContent(ctx context.Context, eventID uuid.UUID) (EventContentView, error) {
	event, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventContentView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventContentView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	config, err := u.configs.Get(ctx, eventID)
	if err != nil {
		return EventContentView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	teamCount, err := u.teams.Count(ctx, eventID)
	if err != nil {
		return EventContentView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count event teams").Err()
	}
	approvedTeamCount, err := u.teams.CountApproved(ctx, eventID)
	if err != nil {
		return EventContentView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count approved event teams").Err()
	}
	participantCount, err := u.participants.Count(ctx, eventID, -1, participantRepo.KindAll)
	if err != nil {
		return EventContentView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count event participants").Err()
	}
	approvedParticipantCount, err := u.participants.Count(ctx, eventID, int32(participantModel.StatusApproved), participantRepo.KindAll)
	if err != nil {
		return EventContentView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count approved event participants").Err()
	}
	statistics, err := u.eventChallenges.ContentStatistics(ctx, eventID)
	if err != nil {
		return EventContentView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event content statistics").Err()
	}
	statistics.TeamCount = teamCount
	statistics.ApprovedTeamCount = approvedTeamCount
	statistics.ParticipantCount = participantCount
	statistics.ApprovedParticipantCount = approvedParticipantCount
	settings, err := u.content.GetSettings(ctx, eventID)
	if err != nil {
		return EventContentView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event content settings").Err()
	}
	return EventContentView{
		Landing:      settings.Landing,
		LandingDraft: settings.LandingDraft,
		Live:         settings.Live,
		Variables:    resolveContentVariables(event, config, statistics, time.Now().UTC()),
	}, nil
}

// validateLandingDocument applies the write-side rules of the public landing.
// Form fields are invalid here; the same block document type is shared with
// forms but page content must remain render-only.
func (u *EventUseCase) validateLandingDocument(ctx context.Context, eventID uuid.UUID, document eventContentModel.Document) error {
	if err := document.ValidateContent(); err != nil {
		return eventContentModel.ErrPageInvalid.WithError(err).Err()
	}
	if err := u.validateContentImageURLs(ctx, eventID, document); err != nil {
		return err
	}
	if err := validateContentAudience(document, eventContentModel.PageVisibilityPublic); err != nil {
		return eventContentModel.ErrPageInvalid.WithError(err).Err()
	}
	return u.validateContentResultsPolicy(ctx, eventID, document, eventContentModel.PageVisibilityPublic)
}

// SaveLandingDraft stores the editor's landing without publishing it.
func (u *EventUseCase) SaveLandingDraft(ctx context.Context, eventID uuid.UUID, document eventContentModel.Document) error {
	if err := u.validateLandingDocument(ctx, eventID, document); err != nil {
		return err
	}
	affected, err := u.content.SaveLandingDraft(ctx, eventID, document)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to save event landing draft").Err()
	}
	if affected == 0 {
		return eventModel.ErrEventNotFound.Err()
	}
	return nil
}

// PublishLanding makes the saved draft the public landing. The draft is
// checked again: settings it depends on (result visibility, uploaded images)
// may have changed since it was saved.
func (u *EventUseCase) PublishLanding(ctx context.Context, eventID uuid.UUID) error {
	settings, err := u.content.GetSettings(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventModel.ErrEventNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event landing draft").Err()
	}
	if settings.LandingDraft == nil {
		return eventContentModel.ErrLandingDraftNotFound.Err()
	}
	if err = u.validateLandingDocument(ctx, eventID, *settings.LandingDraft); err != nil {
		return err
	}
	affected, err := u.content.PublishLanding(ctx, eventID, *settings.LandingDraft)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to publish event landing").Err()
	}
	if affected == 0 {
		// Discarded concurrently.
		return eventContentModel.ErrLandingDraftNotFound.Err()
	}
	return nil
}

func (u *EventUseCase) DiscardLandingDraft(ctx context.Context, eventID uuid.UUID) error {
	affected, err := u.content.DiscardLandingDraft(ctx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to discard event landing draft").Err()
	}
	if affected == 0 {
		return eventContentModel.ErrLandingDraftNotFound.Err()
	}
	return nil
}

type LiveLayoutEditorView struct {
	Published eventContentModel.LiveLayout  `json:"Published"`
	Draft     *eventContentModel.LiveLayout `json:"Draft"`
}

func (u *EventUseCase) GetLiveLayoutEditor(ctx context.Context, eventID uuid.UUID) (LiveLayoutEditorView, error) {
	settings, err := u.content.GetSettings(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return LiveLayoutEditorView{}, eventModel.ErrEventNotFound.Err()
		}
		return LiveLayoutEditorView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get live layout").Err()
	}
	return LiveLayoutEditorView{Published: settings.Live, Draft: settings.LiveDraft}, nil
}

func (u *EventUseCase) SaveLiveLayoutDraft(ctx context.Context, eventID uuid.UUID, layout eventContentModel.LiveLayout) error {
	if err := layout.Validate(); err != nil {
		return eventContentModel.ErrLiveLayoutInvalid.WithError(err).Err()
	}
	affected, err := u.content.SaveLiveDraft(ctx, eventID, layout)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to save event live layout draft").Err()
	}
	if affected == 0 {
		return eventModel.ErrEventNotFound.Err()
	}
	return nil
}

// PublishLiveLayout promotes the draft. The draft passed the write-side check
// when saved, but a legacy draft may predate it (e.g. a grid below 3x3), so it
// is checked again before it becomes the published layout.
func (u *EventUseCase) PublishLiveLayout(ctx context.Context, eventID uuid.UUID) (eventContentModel.LiveLayout, error) {
	settings, err := u.content.GetSettings(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventContentModel.LiveLayout{}, eventModel.ErrEventNotFound.Err()
		}
		return eventContentModel.LiveLayout{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get live layout draft").Err()
	}
	// A missing draft is reported by PublishLive below (zero rows).
	if settings.LiveDraft != nil {
		if err = settings.LiveDraft.Validate(); err != nil {
			return eventContentModel.LiveLayout{}, eventContentModel.ErrLiveDraftInvalid.WithError(err).Err()
		}
	}
	layout, err := u.content.PublishLive(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventContentModel.LiveLayout{}, eventContentModel.ErrLiveDraftNotFound.Err()
		}
		return eventContentModel.LiveLayout{}, model.ErrPlatform.WithError(err).WithMessage("Failed to publish event live layout").Err()
	}
	return layout, nil
}

// GetPublicEventContent returns the landing configuration after the event has
// been published. It shares the manager resolver but never exposes raw
// operational counts to a public renderer.
func (u *EventUseCase) GetPublicEventContent(ctx context.Context, eventID uuid.UUID, _ ContentAccess) (PublicEventContentView, error) {
	content, err := u.GetEventContent(ctx, eventID)
	if err != nil {
		return PublicEventContentView{}, err
	}
	if content.Variables["event.isPublished"] != true || content.Variables["event.isWithdrawn"] == true {
		return PublicEventContentView{}, eventModel.ErrEventNotFound.Err()
	}
	if err := validateContentAudience(content.Landing, eventContentModel.PageVisibilityPublic); err != nil {
		return PublicEventContentView{}, eventContentModel.ErrPageInvalid.WithError(err).Err()
	}
	return PublicEventContentView{Landing: content.Landing, Live: content.Live, Variables: projectContentVariables(content.Landing, content.Variables, eventContentModel.PageVisibilityPublic)}, nil
}

// GetPublicEventPage resolves a static page according to its visibility. A
// manager-only page remains available only through management routes.
func (u *EventUseCase) GetPublicEventPage(ctx context.Context, eventID uuid.UUID, slug string, access ContentAccess) (PublicEventPageView, error) {
	content, err := u.GetEventContent(ctx, eventID)
	if err != nil {
		return PublicEventPageView{}, err
	}
	if content.Variables["event.isPublished"] != true || content.Variables["event.isWithdrawn"] == true {
		return PublicEventPageView{}, eventModel.ErrEventNotFound.Err()
	}
	page, err := u.getPublishedPage(ctx, eventID, slug)
	if err != nil {
		return PublicEventPageView{}, err
	}
	approved := false
	var teamID *uuid.UUID
	if access.UserID != nil {
		participant, participantErr := u.participants.Get(ctx, eventID, *access.UserID)
		approved = participantErr == nil && participant.Status == participantModel.StatusApproved
		if approved {
			teamID = participant.TeamID
		}
	}
	if !canReadContentPage(eventContentModel.Page{Visibility: page.Visibility}, access, approved) {
		return PublicEventPageView{}, eventModel.ErrEventNotFound.Err()
	}
	if err := validateContentAudience(page.Document, page.Visibility); err != nil {
		return PublicEventPageView{}, eventContentModel.ErrPageInvalid.WithError(err).Err()
	}
	if page.Visibility == eventContentModel.PageVisibilityParticipant && content.Variables["event.runtimeOpen"] == true && teamID != nil && documentUsesVariable(page.Document, "event.availableChallengeCount") {
		challenges, listErr := u.teamChallenges.ListPublished(ctx, *teamID)
		if listErr != nil {
			return PublicEventPageView{}, model.ErrPlatform.WithError(listErr).WithMessage("Failed to count available team challenges").Err()
		}
		var available int64
		for _, challenge := range challenges {
			if challenge.Challenge.Readiness == teamChallengeModel.ReadinessPublished {
				available++
			}
		}
		content.Variables["event.availableChallengeCount"] = available
	} else if page.Visibility == eventContentModel.PageVisibilityParticipant {
		content.Variables["event.availableChallengeCount"] = int64(0)
	}
	return PublicEventPageView{Page: page, Variables: projectContentVariables(page.Document, content.Variables, page.Visibility)}, nil
}

// RequirePublicEventPage is a cheap uncached access check before a caller uses
// a separately cached public page document. It must not load live statistics.
func (u *EventUseCase) RequirePublicEventPage(ctx context.Context, eventID uuid.UUID, slug string) error {
	event, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventModel.ErrEventNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	phase := event.Lifecycle.Status(time.Now().UTC())
	if phase == eventModel.LifecycleNotPublished || phase == eventModel.LifecycleWithdrawn {
		return eventModel.ErrEventNotFound.Err()
	}
	page, err := u.getPublishedPage(ctx, eventID, slug)
	if err != nil {
		return err
	}
	if page.Visibility != eventContentModel.PageVisibilityPublic {
		return eventContentModel.ErrPageNotFound.Err()
	}
	return nil
}

func documentUsesVariable(document eventContentModel.Document, name string) bool {
	for _, block := range document.Blocks {
		for _, binding := range block.Variables {
			if binding.Name == name {
				return true
			}
		}
	}
	return false
}

func canReadContentPage(page eventContentModel.Page, access ContentAccess, approved bool) bool {
	switch page.Visibility {
	case eventContentModel.PageVisibilityPublic:
		return true
	case eventContentModel.PageVisibilityParticipant:
		return approved
	default:
		return false
	}
}

var contentVariableAudiences = map[string]eventContentModel.PageVisibility{
	"event.name":                     eventContentModel.PageVisibilityPublic,
	"event.tag":                      eventContentModel.PageVisibilityPublic,
	"event.previewDescription":       eventContentModel.PageVisibilityPublic,
	"event.phase":                    eventContentModel.PageVisibilityPublic,
	"event.startAt":                  eventContentModel.PageVisibilityPublic,
	"event.finishAt":                 eventContentModel.PageVisibilityPublic,
	"event.effectiveFinishAt":        eventContentModel.PageVisibilityPublic,
	"event.isStarted":                eventContentModel.PageVisibilityPublic,
	"event.isFinished":               eventContentModel.PageVisibilityPublic,
	"event.registrationOpen":         eventContentModel.PageVisibilityPublic,
	"event.participation":            eventContentModel.PageVisibilityPublic,
	"event.registration":             eventContentModel.PageVisibilityPublic,
	"event.joinPolicy":               eventContentModel.PageVisibilityPublic,
	"event.maxTeamSize":              eventContentModel.PageVisibilityPublic,
	"event.minTeamSize":              eventContentModel.PageVisibilityPublic,
	"event.maxTeams":                 eventContentModel.PageVisibilityPublic,
	"event.scoringProfile":           eventContentModel.PageVisibilityPublic,
	"event.approvedTeamCount":        eventContentModel.PageVisibilityPublic,
	"event.approvedParticipantCount": eventContentModel.PageVisibilityPublic,
	"event.registrationUnitCount":    eventContentModel.PageVisibilityPublic,
	"event.availableChallengeCount":  eventContentModel.PageVisibilityPublic,
	"event.solvedChallengeCount":     eventContentModel.PageVisibilityPublic,
	"event.solveCount":               eventContentModel.PageVisibilityPublic,
	"event.rosterOpen":               eventContentModel.PageVisibilityParticipant,
}

func variableAllowed(name string, audience eventContentModel.PageVisibility, values map[string]any) bool {
	minimum, known := contentVariableAudiences[name]
	if !known {
		minimum = eventContentModel.PageVisibilityManager
	}
	if audience < minimum {
		return false
	}
	if name == "event.solveCount" || name == "event.solvedChallengeCount" {
		visibility, _ := values["event.scoreboardVisibility"].(string)
		return visibility == "public" || (audience >= eventContentModel.PageVisibilityParticipant && visibility == "private") || audience == eventContentModel.PageVisibilityManager
	}
	return true
}

func validateContentAudience(document eventContentModel.Document, audience eventContentModel.PageVisibility) error {
	for _, block := range document.Blocks {
		for _, binding := range block.Variables {
			minimum, known := contentVariableAudiences[binding.Name]
			if !known {
				minimum = eventContentModel.PageVisibilityManager
			}
			if audience < minimum {
				return fmt.Errorf("variable %s is unavailable to this page audience", binding.Name)
			}
		}
	}
	return nil
}

func (u *EventUseCase) validateContentResultsPolicy(ctx context.Context, eventID uuid.UUID, document eventContentModel.Document, audience eventContentModel.PageVisibility) error {
	needsPolicy := false
	for _, block := range document.Blocks {
		for _, binding := range block.Variables {
			if binding.Name == "event.solveCount" || binding.Name == "event.solvedChallengeCount" {
				needsPolicy = true
			}
		}
	}
	if !needsPolicy {
		return nil
	}
	config, err := u.configs.Get(ctx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get result visibility for page content").Err()
	}
	values := map[string]any{"event.scoreboardVisibility": visibilityName(config.ScoreboardVisibility)}
	for _, block := range document.Blocks {
		for _, binding := range block.Variables {
			if (binding.Name == "event.solveCount" || binding.Name == "event.solvedChallengeCount") && !variableAllowed(binding.Name, audience, values) {
				return eventContentModel.ErrPageInvalid.WithError(fmt.Errorf("variable %s is hidden by result visibility", binding.Name)).Err()
			}
		}
	}
	return nil
}

func projectContentVariables(document eventContentModel.Document, values map[string]any, audience eventContentModel.PageVisibility) map[string]any {
	filtered := make(map[string]any)
	for _, block := range document.Blocks {
		if block.Type == eventContentModel.BlockCTA && (block.Action != nil && block.Action.Kind == "join_event" || block.SecondaryAction != nil && block.SecondaryAction.Kind == "join_event") {
			for _, name := range []string{"event.registrationOpen", "event.startAt", "event.effectiveFinishAt", "event.joinPolicy", "event.tag"} {
				if value, ok := values[name]; ok && variableAllowed(name, audience, values) {
					filtered[name] = value
				}
			}
		}
		for _, binding := range block.Variables {
			if !variableAllowed(binding.Name, audience, values) {
				continue
			}
			if value, ok := values[binding.Name]; ok {
				if binding.Name == "event.availableChallengeCount" && audience == eventContentModel.PageVisibilityPublic {
					filtered[binding.Name] = int64(0)
				} else {
					filtered[binding.Name] = value
				}
			}
		}
	}
	for _, state := range []string{"event.isStarted", "event.isFinished", "event.phase", "event.registrationOpen"} {
		if _, needed := filtered[state]; !needed {
			continue
		}
		for _, name := range []string{"event.startAt", "event.effectiveFinishAt", "event.joinPolicy"} {
			if value, ok := values[name]; ok && variableAllowed(name, audience, values) {
				filtered[name] = value
			}
		}
		break
	}
	return filtered
}

// validatePageForPublic applies the write-side rules to the page as it will be
// published (draft values when a draft exists).
func (u *EventUseCase) validatePageForPublic(ctx context.Context, eventID uuid.UUID, page eventContentModel.Page) error {
	if err := page.Validate(); err != nil {
		return eventContentModel.ErrPageInvalid.WithError(err).Err()
	}
	if err := u.validateContentImageURLs(ctx, eventID, page.Document); err != nil {
		return err
	}
	if err := validateContentAudience(page.Document, page.Visibility); err != nil {
		return eventContentModel.ErrPageInvalid.WithError(err).Err()
	}
	return u.validateContentResultsPolicy(ctx, eventID, page.Document, page.Visibility)
}

func pageDraftFromInput(in EventPageInput) eventContentModel.PageDraft {
	navigation := in.Navigation
	if in.Visibility == eventContentModel.PageVisibilityManager {
		navigation = eventContentModel.PageNavigationNone
	}
	return eventContentModel.PageDraft{Slug: in.Slug, Title: in.Title, Document: in.Document, Visibility: in.Visibility, Navigation: navigation, NavigationAfter: in.NavigationAfter}
}

// CreateEventPage stores a new page as an unpublished draft; it is not
// visible on the site until PublishEventPage.
func (u *EventUseCase) CreateEventPage(ctx context.Context, eventID uuid.UUID, in EventPageInput) (EventPageView, error) {
	now := time.Now().UTC()
	draft := pageDraftFromInput(in)
	page := eventContentModel.Page{ID: uuid.Must(uuid.NewV7()), EventID: eventID, Draft: &draft, CreatedAt: now, UpdatedAt: now}
	page = page.WithDraftValues()
	if err := u.validatePageForPublic(ctx, eventID, page); err != nil {
		return EventPageView{}, err
	}
	if err := u.ensureDraftSlugFree(ctx, eventID, page.ID, draft.Slug); err != nil {
		return EventPageView{}, err
	}
	created, err := u.content.Create(ctx, page)
	if err != nil {
		if creator, ok := repositoryTools.UniqueViolationError(err, eventContentModel.ErrPageExists); ok {
			return EventPageView{}, creator.Err()
		}
		return EventPageView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create event page").Err()
	}
	return toEventPageView(created), nil
}

// ensureDraftSlugFree keeps editor addresses unambiguous: a draft slug may not
// be another page's published or draft slug.
func (u *EventUseCase) ensureDraftSlugFree(ctx context.Context, eventID, pageID uuid.UUID, slug string) error {
	pages, err := u.content.List(ctx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list event pages").Err()
	}
	for _, page := range pages {
		if page.ID != pageID && (page.Slug == slug || page.Draft != nil && page.Draft.Slug == slug) {
			return eventContentModel.ErrPageDraftSlugTaken.Err()
		}
	}
	return nil
}

func (u *EventUseCase) ListEventPages(ctx context.Context, eventID uuid.UUID) ([]EventPageView, error) {
	pages, err := u.content.List(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list event pages").Err()
	}
	out := make([]EventPageView, 0, len(pages))
	for _, page := range pages {
		out = append(out, toEventPageView(page))
	}
	return out, nil
}

// GetEventPage is the editor read: the published slug wins, then a draft slug.
func (u *EventUseCase) GetEventPage(ctx context.Context, eventID uuid.UUID, slug string) (EventPageView, error) {
	page, err := u.content.GetBySlug(ctx, eventID, slug)
	if err == nil {
		return toEventPageView(page), nil
	}
	if !repositoryTools.IsObjectNotFoundError(err) {
		return EventPageView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event page").Err()
	}
	pages, err := u.content.List(ctx, eventID)
	if err != nil {
		return EventPageView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list event pages").Err()
	}
	for _, page := range pages {
		if page.EditorSlug() == slug {
			return toEventPageView(page), nil
		}
	}
	return EventPageView{}, eventContentModel.ErrPageNotFound.Err()
}

// getPublishedPage is the site read: unpublished pages do not exist there.
func (u *EventUseCase) getPublishedPage(ctx context.Context, eventID uuid.UUID, slug string) (EventPageView, error) {
	page, err := u.content.GetBySlug(ctx, eventID, slug)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventPageView{}, eventContentModel.ErrPageNotFound.Err()
		}
		return EventPageView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event page").Err()
	}
	if !page.Published() {
		return EventPageView{}, eventContentModel.ErrPageNotFound.Err()
	}
	return toEventPageView(page), nil
}

// SaveEventPageDraft stores the editor's page without publishing it.
func (u *EventUseCase) SaveEventPageDraft(ctx context.Context, eventID, pageID uuid.UUID, in EventPageInput) (EventPageView, error) {
	draft := pageDraftFromInput(in)
	candidate := eventContentModel.Page{ID: pageID, EventID: eventID, Draft: &draft}.WithDraftValues()
	if err := u.validatePageForPublic(ctx, eventID, candidate); err != nil {
		return EventPageView{}, err
	}
	if err := u.ensureDraftSlugFree(ctx, eventID, pageID, draft.Slug); err != nil {
		return EventPageView{}, err
	}
	saved, err := u.content.SaveDraft(ctx, eventID, pageID, draft, time.Now().UTC())
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventPageView{}, eventContentModel.ErrPageNotFound.Err()
		}
		if creator, ok := repositoryTools.UniqueViolationError(err, eventContentModel.ErrPageDraftSlugTaken); ok {
			return EventPageView{}, creator.Err()
		}
		return EventPageView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save event page draft").Err()
	}
	return toEventPageView(saved), nil
}

// PublishEventPage makes the draft public together with its settings and its
// navbar place, in one statement (B9: no separate reorder request).
func (u *EventUseCase) PublishEventPage(ctx context.Context, eventID, pageID uuid.UUID) (EventPageView, error) {
	pages, err := u.content.List(ctx, eventID)
	if err != nil {
		return EventPageView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list event pages").Err()
	}
	var page *eventContentModel.Page
	for index := range pages {
		if pages[index].ID == pageID {
			page = &pages[index]
		}
	}
	if page == nil {
		return EventPageView{}, eventContentModel.ErrPageNotFound.Err()
	}
	if page.Draft == nil {
		return EventPageView{}, eventContentModel.ErrPageDraftNotFound.Err()
	}
	next := page.WithDraftValues()
	if err = u.validatePageForPublic(ctx, eventID, next); err != nil {
		return EventPageView{}, err
	}
	order := eventContentModel.PlaceInNavigation(pages, pageID, next.Navigation == eventContentModel.PageNavigationNavbar, page.Draft.NavigationAfter)
	published, err := u.content.Publish(ctx, *page, order, time.Now().UTC())
	if err != nil {
		if creator, ok := repositoryTools.UniqueViolationError(err, eventContentModel.ErrPageExists); ok {
			return EventPageView{}, creator.Err()
		}
		return EventPageView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to publish event page").Err()
	}
	if published == 0 {
		// The page changed (or vanished) after it was read.
		if _, getErr := u.GetEventPage(ctx, eventID, page.EditorSlug()); getErr != nil {
			return EventPageView{}, getErr
		}
		return EventPageView{}, eventContentModel.ErrPageModified.Err()
	}
	view, err := u.GetEventPage(ctx, eventID, next.Slug)
	if err != nil {
		return EventPageView{}, err
	}
	return view, nil
}

// DiscardEventPageDraft drops unpublished changes of a published page. A
// never-published page has nothing to fall back to: delete it instead.
func (u *EventUseCase) DiscardEventPageDraft(ctx context.Context, eventID, pageID uuid.UUID) error {
	affected, err := u.content.DiscardDraft(ctx, eventID, pageID, time.Now().UTC())
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to discard event page draft").Err()
	}
	if affected == 0 {
		return eventContentModel.ErrPageDraftNotFound.Err()
	}
	return nil
}

func (u *EventUseCase) DeleteEventPage(ctx context.Context, eventID, pageID uuid.UUID) error {
	affected, err := u.content.Delete(ctx, eventID, pageID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete event page").Err()
	}
	if affected == 0 {
		return eventContentModel.ErrPageNotFound.Err()
	}
	return nil
}

func toEventPageView(page eventContentModel.Page) EventPageView {
	return EventPageView{ID: page.ID, Slug: page.Slug, Title: page.Title, Document: page.Document, Visibility: page.Visibility, Navigation: page.Navigation, NavigationOrder: page.NavigationOrder, Draft: page.Draft, PublishedAt: page.PublishedAt, CreatedAt: page.CreatedAt, UpdatedAt: page.UpdatedAt}
}
