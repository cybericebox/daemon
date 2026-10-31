package eventAnalytics

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

type (
	submissionsResponse struct {
		// Note is the caveat that goes with the numbers: they count who SUBMITTED a correct flag, not who solved
		// the task (code submissions_not_solutions).
		Note string `json:"Note"`
		// PeopleHidden: the caller may not see people (a read-only moderator): the participant names and ids are
		// empty and the addresses masked.
		PeopleHidden bool                 `json:"PeopleHidden"`
		Items        []submissionResponse `json:"Items"`
	}

	submissionResponse struct {
		TeamID          uuid.UUID `json:"TeamID"`
		TeamName        string    `json:"TeamName"`
		ChallengeID     uuid.UUID `json:"ChallengeID"`
		ChallengeName   string    `json:"ChallengeName"`
		UserID          uuid.UUID `json:"UserID"`
		ParticipantName string    `json:"ParticipantName"`
		Email           string    `json:"Email"`
		SubmittedAt     time.Time `json:"SubmittedAt"`
		// MinutesFromStart counts from the event start (0 before it); MinutesFromOpen from the team's first open of
		// the task, null when none was recorded.
		MinutesFromStart int64  `json:"MinutesFromStart"`
		MinutesFromOpen  *int64 `json:"MinutesFromOpen"`
		// ParticipantWrong / TeamWrong are the wrong attempts before the correct one.
		ParticipantWrong int64 `json:"ParticipantWrong"`
		TeamWrong        int64 `json:"TeamWrong"`
	}
)

func toSubmissionsResponse(v eventAnalyticsUseCase.SubmissionsView) submissionsResponse {
	out := submissionsResponse{
		Note:  eventAnalyticsUseCase.SubmissionNote,
		Items: make([]submissionResponse, 0, len(v.Items)),
	}
	for _, s := range v.Items {
		out.Items = append(out.Items, submissionResponse{
			TeamID:           s.TeamID,
			TeamName:         s.TeamName,
			ChallengeID:      s.ChallengeID,
			ChallengeName:    s.ChallengeName,
			UserID:           s.UserID,
			ParticipantName:  s.ParticipantName,
			Email:            s.Email,
			SubmittedAt:      s.SubmittedAt,
			MinutesFromStart: s.MinutesFromStart,
			MinutesFromOpen:  s.MinutesFromOpen,
			ParticipantWrong: s.ParticipantWrong,
			TeamWrong:        s.TeamWrong,
		})
	}
	return out
}

// hidePeople anonymizes the participants of the report for a viewer without the people access: no name, no id, a
// masked address. Teams, tasks, minutes and counts stay.
func hidePeople(v eventAnalyticsUseCase.SubmissionsView) eventAnalyticsUseCase.SubmissionsView {
	items := make([]eventAnalyticsUseCase.SubmissionView, len(v.Items))
	for i, item := range v.Items {
		item.UserID, item.ParticipantName = uuid.Nil, ""
		item.Email = dispatchModel.MaskEmail(item.Email)
		items[i] = item
	}
	v.Items = items
	return v
}

// submissionsNoteText is the caveat written into the export, in the language of its headers.
const submissionsNoteText = "Примітка: рахуються здачі прапора (хто відправив правильну відповідь), а не розвʼязання: у команді прапор може знайти і не той, хто його вводить."

// submissions godoc
// @Summary Who submitted the flag of each solved task
// @Description One row per solved task of a visible team: the participant whose submission was the first correct one, the minute from the event start and from the team's first open of the task, and the wrong attempts before it. It counts submissions, not solutions (Note).
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=submissionsResponse}
// @Router /events/{id}/manage/analytics/submissions [get]
func (h *Handler) submissions(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	people, ok := h.seesPeople(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsSubmissions(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	if !people {
		v = hidePeople(v)
	}
	out := toSubmissionsResponse(v)
	out.PeopleHidden = !people
	response.AbortWithData(ctx, out)
}

// exportSubmissions godoc
// @Summary Export the submissions per participant as CSV (UTF-8 with BOM)
// @Description The first line is a note that the rows count flag submissions, not solutions.
// @Tags event-analytics
// @Produce text/csv
// @Param id path string true "event ID"
// @Success 200 {string} string "CSV"
// @Router /events/{id}/manage/analytics/submissions/export.csv [get]
func (h *Handler) exportSubmissions(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	people, ok := h.seesPeople(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventAnalyticsSubmissions(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	if !people {
		v = hidePeople(v)
	}
	w := startAnalyticsCSV(ctx, "submissions")
	n := func(x int64) string { return strconv.FormatInt(x, 10) }
	_ = w.Write([]string{submissionsNoteText})
	_ = w.Write(submissionsCSVHeader())
	for _, s := range v.Items {
		_ = w.Write(
			[]string{
				csvCellText(s.ParticipantName),
				csvCellText(s.Email),
				csvCellText(s.TeamName),
				csvCellText(s.ChallengeName),
				csvTimePtr(
					&s.SubmittedAt,
				),
				n(s.MinutesFromStart),
				csvSeconds(s.MinutesFromOpen),
				n(s.ParticipantWrong),
				n(s.TeamWrong),
			},
		)
	}
	w.Flush()
}

// CSV column titles of the submissions export.
const (
	columnParticipant = "Учасник"
	columnTeam        = "Команда"
	columnTask        = "Завдання"
)

func submissionsCSVHeader() []string {
	return []string{
		columnParticipant, "Пошта", columnTeam, columnTask, "Здано (UTC)", "Хвилин від старту",
		"Хвилин від відкриття завдання", "Хибних спроб учасника", "Хибних спроб команди",
	}
}
