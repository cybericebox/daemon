package platformAnalytics

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
	"github.com/cybericebox/daemon/internal/model/rbac"
	platformAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/platformAnalytics"
)

// InfrastructureUseCase is the port of the infrastructure section.
type InfrastructureUseCase interface {
	GetInfrastructure(ctx context.Context, from, to *time.Time) (platformAnalyticsUseCase.InfrastructureView, error)
}

func (h *Handler) initInfrastructure(router *gin.RouterGroup) {
	group := router.Group("analytics", h.prot.RequirePermission(rbac.PermAnalyticsRead))
	group.GET("infrastructure", h.infrastructure)
	group.GET("infrastructure/export.csv", h.exportInfrastructure)
}

type (
	infrastructureResponse struct {
		Period periodResponse `json:"Period"`
		// Bucket is the granularity of Peaks: hour (periods up to 14 days) or day.
		Bucket string `json:"Bucket"`
		// Stands is the live picture right now, whatever the period.
		Stands standCountsResponse `json:"Stands"`
		// Moderators is the moderators-team part of Stands.
		Moderators standCountsResponse `json:"Moderators"`
		// TestLabs are the catalog test labs (exercise test deploys), not team stands.
		TestLabs   testLabCountsResponse `json:"TestLabs"`
		StandHours standHoursResponse    `json:"StandHours"`
		// Resources is what the labs of each kind use right now.
		Resources resourcesByKindResponse `json:"Resources"`
		// Peaks counts team stands only, TestLabPeaks test labs only, AllPeaks every lab at once.
		Peaks        []peakPointResponse     `json:"Peaks"`
		TestLabPeaks []peakPointResponse     `json:"TestLabPeaks"`
		AllPeaks     []peakPointResponse     `json:"AllPeaks"`
		PeakMax      int64                   `json:"PeakMax"`
		AllPeakMax   int64                   `json:"AllPeakMax"`
		Failures     []failureReasonResponse `json:"Failures"`
		FailedLabs   int64                   `json:"FailedLabs"`
		FailedStands int64                   `json:"FailedStands"`
		Capacity     []capacityPointResponse `json:"Capacity"`
		// CapacityStepSeconds is the width of one capacity point.
		CapacityStepSeconds int64 `json:"CapacityStepSeconds"`
	}
	standCountsResponse struct {
		// Active is creating plus ready.
		Active   int64 `json:"Active"`
		Creating int64 `json:"Creating"`
		Ready    int64 `json:"Ready"`
		Failed   int64 `json:"Failed"`
		Removed  int64 `json:"Removed"`
	}
	resourcesByKindResponse struct {
		Event      resourcesResponse `json:"Event"`
		Moderators resourcesResponse `json:"Moderators"`
		Test       resourcesResponse `json:"Test"`
	}
	// resourcesResponse: live usage (UsageAvailable) or the requested fallback; Known is false while nothing was observed.
	resourcesResponse struct {
		Known                  bool  `json:"Known"`
		UsageAvailable         bool  `json:"UsageAvailable"`
		CPUMillicores          int64 `json:"CPUMillicores"`
		MemoryBytes            int64 `json:"MemoryBytes"`
		RequestedCPUMillicores int64 `json:"RequestedCPUMillicores"`
		RequestedMemoryBytes   int64 `json:"RequestedMemoryBytes"`
	}
	testLabCountsResponse struct {
		Active  int64 `json:"Active"`
		Expired int64 `json:"Expired"`
	}
	standHoursResponse struct {
		// TotalHours is the team stand time (event and moderators teams).
		TotalHours  float64                   `json:"TotalHours"`
		TotalEvents int64                     `json:"TotalEvents"`
		Events      []standHoursEventResponse `json:"Events"`
		// Kinds splits the time by lab kind: event, moderators, test.
		Kinds []standHoursKindResponse `json:"Kinds"`
		// AllHours is TotalHours plus the test lab time.
		AllHours float64 `json:"AllHours"`
	}
	standHoursKindResponse struct {
		Kind  string  `json:"Kind"`
		Hours float64 `json:"Hours"`
		Labs  int64   `json:"Labs"`
	}
	standHoursEventResponse struct {
		EventID   uuid.UUID `json:"EventID"`
		EventName string    `json:"EventName"`
		Hours     float64   `json:"Hours"`
		Stands    int64     `json:"Stands"`
	}
	peakPointResponse struct {
		At   time.Time `json:"At"`
		Peak int64     `json:"Peak"`
	}
	failureReasonResponse struct {
		// Code is a reason code (image_pull, crash_loop, ...); the UI labels it.
		Code   string    `json:"Code"`
		Labs   int64     `json:"Labs"`
		Stands int64     `json:"Stands"`
		Events int64     `json:"Events"`
		LastAt time.Time `json:"LastAt"`
	}
	capacityPointResponse struct {
		At                       time.Time `json:"At"`
		AllocatableCPUMillicores int64     `json:"AllocatableCPUMillicores"`
		RequestedCPUMillicores   int64     `json:"RequestedCPUMillicores"`
		AllocatableMemoryBytes   int64     `json:"AllocatableMemoryBytes"`
		RequestedMemoryBytes     int64     `json:"RequestedMemoryBytes"`
		Agents                   int64     `json:"Agents"`
	}
)

func toInfrastructureResponse(v platformAnalyticsUseCase.InfrastructureView) infrastructureResponse {
	out := infrastructureResponse{
		Period:     periodResponse{From: v.Period.From, To: v.Period.To, All: v.Period.All},
		Bucket:     v.Bucket,
		Stands:     standCounts(v.Stands),
		Moderators: standCounts(v.Moderators),
		TestLabs:   testLabCountsResponse{Active: v.TestLabs.Active, Expired: v.TestLabs.Expired},
		Resources:  resourcesByKindResponse{Event: resourcesOf(v.Resources.Event), Moderators: resourcesOf(v.Resources.Moderators), Test: resourcesOf(v.Resources.Test)},
		StandHours: standHoursResponse{
			TotalHours: v.StandHours.TotalHours, TotalEvents: v.StandHours.TotalEvents, AllHours: v.StandHours.AllHours,
			Events: make([]standHoursEventResponse, 0, len(v.StandHours.Events)),
			Kinds:  make([]standHoursKindResponse, 0, len(v.StandHours.Kinds)),
		},
		Peaks:               peakPoints(v.Peaks),
		TestLabPeaks:        peakPoints(v.TestLabPeaks),
		AllPeaks:            peakPoints(v.AllPeaks),
		PeakMax:             v.PeakMax,
		AllPeakMax:          v.AllPeakMax,
		Failures:            make([]failureReasonResponse, 0, len(v.Failures)),
		FailedLabs:          v.FailedLabs,
		FailedStands:        v.FailedStands,
		Capacity:            make([]capacityPointResponse, 0, len(v.Capacity)),
		CapacityStepSeconds: v.CapacityStepSeconds,
	}
	for _, e := range v.StandHours.Events {
		out.StandHours.Events = append(out.StandHours.Events, standHoursEventResponse{EventID: e.EventID, EventName: e.EventName, Hours: e.Hours, Stands: e.Stands})
	}
	for _, k := range v.StandHours.Kinds {
		out.StandHours.Kinds = append(out.StandHours.Kinds, standHoursKindResponse{Kind: k.Kind, Hours: k.Hours, Labs: k.Labs})
	}
	for _, f := range v.Failures {
		out.Failures = append(out.Failures, failureReasonResponse{Code: f.Code, Labs: f.Labs, Stands: f.Stands, Events: f.Events, LastAt: f.LastAt})
	}
	for _, c := range v.Capacity {
		out.Capacity = append(out.Capacity, capacityPointResponse{
			At: c.At, AllocatableCPUMillicores: c.AllocatableCPUMillicores, RequestedCPUMillicores: c.RequestedCPUMillicores,
			AllocatableMemoryBytes: c.AllocatableMemoryBytes, RequestedMemoryBytes: c.RequestedMemoryBytes, Agents: c.Agents,
		})
	}
	return out
}

func resourcesOf(r labMonitoringModel.Resources) resourcesResponse {
	return resourcesResponse{Known: r.Known, UsageAvailable: r.Available, CPUMillicores: r.CPUMillicores, MemoryBytes: r.MemoryBytes, RequestedCPUMillicores: r.RequestedCPU, RequestedMemoryBytes: r.RequestedMemory}
}

func standCounts(v platformAnalyticsUseCase.StandCountsView) standCountsResponse {
	return standCountsResponse{Active: v.Active, Creating: v.Creating, Ready: v.Ready, Failed: v.Failed, Removed: v.Removed}
}

func peakPoints(points []platformAnalyticsUseCase.PeakPointView) []peakPointResponse {
	out := make([]peakPointResponse, 0, len(points))
	for _, p := range points {
		out = append(out, peakPointResponse{At: p.At, Peak: p.Peak})
	}
	return out
}

// infrastructure godoc
// @Summary  Platform analytics: infrastructure
// @Description Stand-hours per event and by lab kind (event teams, the moderators team, catalog test labs), peaks of concurrently active stands and test labs (per hour up to 14 days, else per day), failures by reason code and the cluster capacity over time. A stand is active while creating or ready; a test lab from its creation to its removal. Stands, TotalHours and Peaks count team stands only; test labs are reported next to them. Failures of test labs are not recorded. Aggregates only.
// @Tags     platform-analytics
// @Produce  json
// @Param    from  query  string  false  "period start (RFC 3339); absent = all time"
// @Param    to    query  string  false  "period end, exclusive (RFC 3339); absent = now"
// @Success  200   {object}  response.Response{data=infrastructureResponse}
// @Router   /analytics/infrastructure [get]
func (h *Handler) infrastructure(ctx *gin.Context) {
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	view, err := h.useCase.GetInfrastructure(ctx.Request.Context(), from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toInfrastructureResponse(view))
}

// exportInfrastructure godoc
// @Summary  Platform analytics: export one infrastructure table as CSV (UTF-8 with BOM)
// @Tags     platform-analytics
// @Produce  text/csv
// @Param    table  query  string  true   "stand_hours | kinds | failures | capacity | peaks"
// @Param    from   query  string  false  "period start (RFC 3339)"
// @Param    to     query  string  false  "period end, exclusive (RFC 3339)"
// @Success  200    {string}  string  "CSV"
// @Router   /analytics/infrastructure/export.csv [get]
func (h *Handler) exportInfrastructure(ctx *gin.Context) {
	table := ctx.Query("table")
	switch table {
	case "stand_hours", "kinds", "failures", "capacity", "peaks":
	default:
		response.AbortWithError(ctx, platformAnalyticsModel.ErrPlatformAnalyticsTableUnknown.Err())
		return
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	view, err := h.useCase.GetInfrastructure(ctx.Request.Context(), from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	w := startCSV(ctx, "infrastructure-"+table)
	writeInfrastructureCSV(w.Write, table, view)
	w.Flush()
}

// writeInfrastructureCSV writes the header and rows of one table.
func writeInfrastructureCSV(write func([]string) error, table string, v platformAnalyticsUseCase.InfrastructureView) {
	switch table {
	case "stand_hours":
		_ = write([]string{"Захід", "Стенд-годин", "Стендів"})
		for _, e := range v.StandHours.Events {
			_ = write([]string{csvText(e.EventName), csvFloat(e.Hours), csvInt(e.Stands)})
		}
	case "kinds":
		_ = write([]string{"Тип", "Годин", "Лабораторій"})
		for _, k := range v.StandHours.Kinds {
			_ = write([]string{csvText(k.Kind), csvFloat(k.Hours), csvInt(k.Labs)})
		}
	case "failures":
		_ = write([]string{"Причина (код)", "Лабораторій", "Стендів", "Заходів", "Останній збій"})
		for _, f := range v.Failures {
			_ = write([]string{csvText(f.Code), csvInt(f.Labs), csvInt(f.Stands), csvInt(f.Events), csvTime(f.LastAt)})
		}
	case "capacity":
		_ = write([]string{"Час", "CPU доступно (мілі-ядер)", "CPU зарезервовано (мілі-ядер)", "Памʼять доступно (байт)", "Памʼять зарезервовано (байт)", "Агентів"})
		for _, c := range v.Capacity {
			_ = write([]string{csvTime(c.At), csvInt(c.AllocatableCPUMillicores), csvInt(c.RequestedCPUMillicores), csvInt(c.AllocatableMemoryBytes), csvInt(c.RequestedMemoryBytes), csvInt(c.Agents)})
		}
	case "peaks":
		_ = write([]string{"Час", "Пік активних стендів", "Пік тестових лабораторій", "Пік усіх лабораторій"})
		tests, all := peakByTime(v.TestLabPeaks), peakByTime(v.AllPeaks)
		for _, p := range v.Peaks {
			_ = write([]string{csvTime(p.At), csvInt(p.Peak), csvInt(tests[p.At.UnixNano()]), csvInt(all[p.At.UnixNano()])})
		}
	}
}

// peakByTime indexes a peak series by its bucket start (unix nanoseconds).
func peakByTime(points []platformAnalyticsUseCase.PeakPointView) map[int64]int64 {
	out := make(map[int64]int64, len(points))
	for _, p := range points {
		out[p.At.UnixNano()] = p.Peak
	}
	return out
}
