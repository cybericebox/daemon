package model

import "github.com/cybericebox/daemon/internal/appError"

type (
	AccountExistsTemplateData struct {
		Username string
	}

	ContinueRegistrationTemplateData struct {
		Link string
	}

	InvitationToRegistrationTemplateData struct {
		Link string
	}

	PasswordResettingTemplateData struct {
		Username string
		Link     string
	}

	EmailConfirmationTemplateData struct {
		Username string
		Link     string
	}
)

// email templates names
const (
	AccountExistsTemplate            = "account_exists_template"
	ContinueRegistrationTemplate     = "continue_registration_template"
	InvitationToRegistrationTemplate = "invitation_to_registration_template"
	PasswordResettingTemplate        = "password_resetting_template"
	EmailConfirmationTemplate        = "email_confirmation_template"
)

// links for email confirmation, password resetting, etc.
const (
	ContinueRegistrationLink = "/sign-up/"
	PasswordResettingLink    = "/reset-password/"
	EmailConfirmationLink    = "/confirm-email/"
)

// errors
var (
	ErrEmail = appError.ErrInternal.WithObjectCode(emailObjectCode)
)
