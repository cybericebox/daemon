package app

import (
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/sse"
	challengeAttempt "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	mailUseCase "github.com/cybericebox/daemon/internal/useCase/mail"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
)

// applyTunables hands the operator-set limits and timings to the packages that own them. It runs once
// at start, before anything serves.
func applyTunables(t config.TunablesConfig) {
	eventStandModel.DeployTimeout = t.EventStandDeployTimeout
	sse.SetMaxLifetime(t.SSEMaxLifetime)
	eventContentModel.LiveScreenLinkMaxTTL = t.LiveScreenLinkMaxTTL
	eventConfigModel.SetDefaultMaxTeamSize(t.EventDefaultMaxTeamSize)

	mailUseCase.ConfigureLimiter(mailUseCase.LimiterTimings{
		MaxRateWait: t.MailMaxRateWait, QuotaRetryAfter: t.MailQuotaRetryAfter,
		QuotaRecheck: t.MailQuotaRecheck, QuotaWindow: t.MailQuotaWindow,
	})
	mailModel.ConfigureLimitCaps(t.MailMaxPerSecondLimit, t.MailDailyQuotaLimit)

	authUseCase.MaxAvatarBytes = t.AvatarMaxBytes
	challengeAttempt.MaxAnswerBytes = t.FlagAnswerMaxBytes
	eventUseCase.ConfigureUploadLimits(eventUseCase.UploadLimits{
		Logo: int64(t.EventLogoMaxBytes), PreviewPicture: int64(t.EventPreviewPictureMaxBytes),
		ContentImage: int64(t.EventContentImageMaxBytes), LiveLogo: int64(t.LiveLogoMaxBytes),
	})
	emailUseCase.ConfigureTemplateImages(emailUseCase.TemplateImageLimits{
		UploadBytes: t.EmailImageUploadMaxBytes, Bytes: t.EmailImageMaxBytes,
		Width: t.EmailImageMaxWidth, Pixels: t.EmailImageMaxPixels,
	})
}
