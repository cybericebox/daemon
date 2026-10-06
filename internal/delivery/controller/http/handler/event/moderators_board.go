package event

import (
	"encoding/json"
	"mime"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/download"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	utils "github.com/cybericebox/daemon/internal/delivery/controller/http/utils"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/cybericebox/daemon/pkg/pagination"
)

// moderatorsBoardItemResponse is the participant board item shape
// (teams/challenges/mine) plus BoardPublished. On this board Locked is always
// false and SolveCount always null.
type moderatorsBoardItemResponse struct {
	ID               uuid.UUID                   `json:"ID"`
	EventChallengeID uuid.UUID                   `json:"EventChallengeID"`
	Snapshot         json.RawMessage             `json:"Snapshot" swaggertype:"object"`
	Readiness        int16                       `json:"Readiness"`
	SolvedAt         *time.Time                  `json:"SolvedAt"`
	Points           int32                       `json:"Points"`
	Order            int32                       `json:"Order"`
	GroupID          *uuid.UUID                  `json:"GroupID"`
	GroupName        string                      `json:"GroupName"`
	GroupOrder       int32                       `json:"GroupOrder"`
	ContentUpdatedAt *time.Time                  `json:"ContentUpdatedAt"`
	Infrastructure   bool                        `json:"Infrastructure"`
	HintsEnabled     bool                        `json:"HintsEnabled"`
	Locked           bool                        `json:"Locked"`
	Prerequisites    []boardPrerequisiteResponse `json:"Prerequisites"`
	Files            []boardFileResponse         `json:"Files"`
	SolveCount       *int64                      `json:"SolveCount"`
	BoardPublished   bool                        `json:"BoardPublished"`
	// Hints: every text is revealed on the moderators board.
	Hints []boardHintResponse `json:"Hints"`
	// AwardedPoints, HintPenalty and SolvedBy are null while the task is unsolved (see the participant board).
	AwardedPoints *int32               `json:"AwardedPoints"`
	HintPenalty   *int32               `json:"HintPenalty"`
	SolvedBy      *boardSolverResponse `json:"SolvedBy"`
}

type boardSolverResponse struct {
	UserID uuid.UUID `json:"UserID"`
	Name   string    `json:"Name"`
}

type boardHintResponse struct {
	ID             uuid.UUID  `json:"ID"`
	Level          string     `json:"Level"`
	Cost           int32      `json:"Cost"`
	Unlocked       bool       `json:"Unlocked"`
	Content        *string    `json:"Content"`
	UnlockedAt     *time.Time `json:"UnlockedAt"`
	UnlockedByName string     `json:"UnlockedByName"`
}

type boardPrerequisiteResponse struct {
	EventChallengeID uuid.UUID `json:"EventChallengeID"`
	Name             string    `json:"Name"`
	Solved           bool      `json:"Solved"`
}

type boardFileResponse struct {
	FileID uuid.UUID `json:"FileID"`
	Name   string    `json:"Name"`
	Size   int64     `json:"Size"`
}

type moderatorsSubmitRequest struct {
	Answer string `json:"Answer"`
}

type moderatorsSubmitResponse struct {
	Correct    bool `json:"Correct"`
	FirstSolve bool `json:"FirstSolve"`
}

type moderatorsSolveResponse struct {
	TeamName   string    `json:"TeamName"`
	SolvedAt   time.Time `json:"SolvedAt"`
	Own        bool      `json:"Own"`
	FirstBlood bool      `json:"FirstBlood"`
}

func toModeratorsBoardItemResponse(v eventUseCase.OwnChallengeView) moderatorsBoardItemResponse {
	out := moderatorsBoardItemResponse{ID: v.ID, EventChallengeID: v.EventChallengeID, Snapshot: v.Snapshot, Readiness: int16(v.Readiness), SolvedAt: v.SolvedAt,
		Points: v.Points, Order: v.Order, GroupID: v.GroupID, GroupName: v.GroupName, GroupOrder: v.GroupOrder, ContentUpdatedAt: v.ContentUpdatedAt,
		Infrastructure: v.Infrastructure, HintsEnabled: v.HintsEnabled, Locked: v.Locked, SolveCount: v.SolveCount, BoardPublished: v.BoardPublished,
		Prerequisites: make([]boardPrerequisiteResponse, 0, len(v.Prerequisites)), Files: make([]boardFileResponse, 0, len(v.Files)),
		Hints: make([]boardHintResponse, 0, len(v.Hints)), AwardedPoints: v.AwardedPoints, HintPenalty: v.HintPenalty}
	if v.SolvedBy != nil {
		out.SolvedBy = &boardSolverResponse{UserID: v.SolvedBy.UserID, Name: v.SolvedBy.Name}
	}
	for _, hint := range v.Hints {
		out.Hints = append(out.Hints, boardHintResponse(hint))
	}
	for _, p := range v.Prerequisites {
		out.Prerequisites = append(out.Prerequisites, boardPrerequisiteResponse{EventChallengeID: p.EventChallengeID, Name: p.Name, Solved: p.Solved})
	}
	for _, f := range v.Files {
		out.Files = append(out.Files, boardFileResponse{FileID: f.FileID, Name: f.Name, Size: f.Size})
	}
	return out
}

// moderatorsBoard godoc
// @Summary The challenge board as the moderators team (Ready and Published assignments, unlocked)
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=[]moderatorsBoardItemResponse}
// @Failure 409 {object} response.Response "moderators team unavailable (no event owner)"
// @Router /events/{id}/manage/labs/moderators/board [get]
func (h *Handler) moderatorsBoard(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	items, err := h.useCase.ListModeratorsBoard(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]moderatorsBoardItemResponse, 0, len(items))
	for _, item := range items {
		out = append(out, toModeratorsBoardItemResponse(item))
	}
	response.AbortWithData(ctx, out)
}

// moderatorsSubmit godoc
// @Summary Submit an answer as the moderators team
// @Description A real attempt of the hidden moderators team: recorded, rate limited and scored like any team (it stays out of the public results). Allowed before the start and outside the window, not after the event is withdrawn or archived. Idempotency-Key is optional; without it every request is a new attempt.
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param challengeID path string true "event challenge ID"
// @Param Idempotency-Key header string false "UUID generated once per submit action"
// @Param body body moderatorsSubmitRequest true "answer"
// @Success 200 {object} response.Response{data=moderatorsSubmitResponse}
// @Router /events/{id}/manage/labs/moderators/challenges/{challengeID}/submit [post]
func (h *Handler) moderatorsSubmit(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var req moderatorsSubmitRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	idempotencyKey, err := uuid.FromString(ctx.GetHeader("Idempotency-Key"))
	if err != nil || idempotencyKey == uuid.Nil {
		idempotencyKey = uuid.Must(uuid.NewV7())
	}
	v, err := h.useCase.SubmitModeratorsChallenge(ctx, eventID, claims.UserID, challengeID, eventUseCase.SubmitChallengeInput{
		Answer: req.Answer, IdempotencyKey: idempotencyKey, ReceivedAt: middleware.RequestReceivedAt(ctx.Request.Context())})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, moderatorsSubmitResponse{Correct: v.Correct, FirstSolve: v.FirstSolve})
}

// moderatorsSolves godoc
// @Summary List one page of who solved a moderators board challenge
// @Description Live (no freeze). Visible teams plus the moderators team's own solves (Own). Oldest solve first; FirstBlood marks the earliest solve among the visible teams. The cursor is the NextCursor of the previous page.
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param challengeID path string true "event challenge ID"
// @Param cursor query string false "NextCursor of the previous page"
// @Param pageSize query int false "page size"
// @Success 200 {object} response.Response{data=pagination.CursorPage[moderatorsSolveResponse]}
// @Router /events/{id}/manage/labs/moderators/challenges/{challengeID}/solves [get]
func (h *Handler) moderatorsSolves(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	params, err := utils.GetCursorPaginationParams(ctx)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	page, err := h.useCase.ListModeratorsChallengeSolves(ctx, eventID, challengeID, params.Cursor, params.PageSize)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	items := make([]moderatorsSolveResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, moderatorsSolveResponse{TeamName: item.TeamName, SolvedAt: item.SolvedAt, Own: item.Own, FirstBlood: item.FirstBlood})
	}
	response.AbortWithData(ctx, pagination.NewCursorPage(items, page.HasMore, page.Next, page.Total))
}

// moderatorsChallengeFile godoc
// @Summary Download an attachment of a moderators board challenge
// @Tags events
// @Produce application/octet-stream
// @Param id path string true "event ID"
// @Param challengeID path string true "event challenge ID"
// @Param fileID path string true "attachment file ID"
// @Success 200 {file} file
// @Router /events/{id}/manage/labs/moderators/challenges/{challengeID}/files/{fileID} [get]
func (h *Handler) moderatorsChallengeFile(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	fileID, err := uuid.FromString(ctx.Param("fileID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	reader, file, err := h.useCase.StreamModeratorsChallengeAttachment(ctx, eventID, challengeID, fileID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	defer func() { _ = reader.Close() }()
	download.ExtendDeadline(ctx)
	ctx.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Name}))
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	ctx.Header("Cache-Control", "private, no-store")
	contentType := file.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	ctx.DataFromReader(http.StatusOK, file.SizeBytes, contentType, reader, nil)
}

type moderatorsTeamMemberResponse struct {
	UserID uuid.UUID `json:"UserID"`
	Name   string    `json:"Name"`
	Role   int16     `json:"Role"`
}

type moderatorsTeamResponse struct {
	TeamID  uuid.UUID                      `json:"TeamID"`
	Members []moderatorsTeamMemberResponse `json:"Members"`
}

// moderatorsTeam godoc
// @Summary The hidden moderators team for the organizers' team page (read-only)
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=moderatorsTeamResponse}
// @Failure 409 {object} response.Response "moderators team unavailable (no event owner)"
// @Router /events/{id}/manage/labs/moderators/team [get]
func (h *Handler) moderatorsTeam(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	view, err := h.useCase.GetModeratorsTeam(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := moderatorsTeamResponse{TeamID: view.TeamID, Members: make([]moderatorsTeamMemberResponse, 0, len(view.Members))}
	for _, member := range view.Members {
		out.Members = append(out.Members, moderatorsTeamMemberResponse{UserID: member.UserID, Name: member.Name, Role: member.Role})
	}
	response.AbortWithData(ctx, out)
}
