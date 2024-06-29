package model

type (
	AccountExistsTemplateData struct {
		Username string
	}

	ContinueRegistrationTemplateData struct {
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

const (
	AccountExistsTemplate        = "account_exists_template"
	ContinueRegistrationTemplate = "continue_registration_template"
	PasswordResettingTemplate    = "password_resetting_template"
	EmailConfirmationTemplate    = "email_confirmation_template"
)

const (
	// links for email confirmation, password resetting, etc.

	ContinueRegistrationLink = "/sign-up/"
	PasswordResettingLink    = "/reset-password/"
	EmailConfirmationLink    = "/email/confirm/"
)
