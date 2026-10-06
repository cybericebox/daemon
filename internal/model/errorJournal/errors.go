package errorJournal

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

// ErrorJournalObjectCode — next free detail code: 9
var (
	ErrGroupNotFound = err.ErrObjectNotFound.WithObjectCode(model.ErrorJournalObjectCode).
				WithMessage("The error group was not found").WithDetailCode(1)
	ErrFilterInvalid = err.ErrInvalidData.WithObjectCode(model.ErrorJournalObjectCode).
				WithMessage("The error journal filter is invalid").WithDetailCode(2)
	ErrStatusInvalid = err.ErrInvalidData.WithObjectCode(model.ErrorJournalObjectCode).
				WithMessage("The error group status is invalid").WithDetailCode(3)
	ErrEmailInvalid = err.ErrInvalidData.WithObjectCode(model.ErrorJournalObjectCode).
			WithMessage("The notification address is not a valid e-mail address").WithDetailCode(4)
	ErrTooManyEmails = err.ErrInvalidData.WithObjectCode(model.ErrorJournalObjectCode).
				WithMessage("Too many notification e-mail addresses").WithDetailCode(5)
	ErrChatIDInvalid = err.ErrInvalidData.WithObjectCode(model.ErrorJournalObjectCode).
				WithMessage("The Telegram chat id is not valid").WithDetailCode(6)
	ErrTooManyChats = err.ErrInvalidData.WithObjectCode(model.ErrorJournalObjectCode).
			WithMessage("Too many Telegram chats").WithDetailCode(7)
	ErrPeriodInvalid = err.ErrInvalidData.WithObjectCode(model.ErrorJournalObjectCode).
				WithMessage("The error journal period is invalid").WithDetailCode(8)
)
