package mailModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

// MailObjectCode — next free detail code: 9
var (
	ErrSMTPSettingsInvalid = err.ErrInvalidData.WithObjectCode(model.MailObjectCode).
				WithMessage("SMTP settings are invalid").WithDetailCode(1)
	ErrSecretsUnavailable = err.ErrConflict.WithObjectCode(model.MailObjectCode).
				WithMessage("SMTP passwords cannot be stored: the secrets key is not configured").WithDetailCode(2)
	ErrRequiredNotification = err.ErrConflict.WithObjectCode(model.MailObjectCode).
				WithMessage("This notification is always sent and cannot be changed").WithDetailCode(3)
	ErrIdentityInvalid = err.ErrInvalidData.WithObjectCode(model.MailObjectCode).
				WithMessage("Mail sender settings are invalid").WithDetailCode(4)
	ErrFooterInvalid = err.ErrInvalidData.WithObjectCode(model.MailObjectCode).
				WithMessage("Email footer is invalid").WithDetailCode(5)
	ErrSendingDomainInvalid = err.ErrInvalidData.WithObjectCode(model.MailObjectCode).
				WithMessage("Sending domain is invalid").WithDetailCode(6)
	ErrProviderNameTaken = err.ErrConflict.WithObjectCode(model.MailObjectCode).
				WithMessage("An SMTP provider with this name already exists").WithDetailCode(7)
	ErrProviderNotFound = err.ErrObjectNotFound.WithObjectCode(model.MailObjectCode).
				WithMessage("SMTP provider not found").WithDetailCode(8)
)
