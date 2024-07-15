package email

import (
	"bytes"
	"context"
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/cybericebox/daemon/internal/model"
	"html/template"
)

type (
	EmailService struct {
		repository IRepository
	}

	IRepository interface {
		GetEmailTemplateBody(ctx context.Context, key string) (string, error)
		GetEmailTemplateSubject(ctx context.Context, key string) (string, error)

		SendEmail(sendTo, subject, body string) error
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewEmailService(deps Dependencies) *EmailService {
	return &EmailService{
		repository: deps.Repository,
	}
}

func (s *EmailService) SendContinueRegistrationEmail(ctx context.Context, sendTo string, data model.ContinueRegistrationTemplateData) error {

	subjectT, bodyT, err := s.getTemplate(ctx, model.ContinueRegistrationTemplate)
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to get email template")
	}

	subject, err := s.populatedWithData(subjectT, data)
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to populate subject with data")
	}

	body, err := s.populatedWithData(bodyT, data)
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to populate body with data")
	}

	if err = s.repository.SendEmail(sendTo, subject, body); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to send email")
	}
	return nil
}

func (s *EmailService) SendAccountExistsEmail(ctx context.Context, sendTo string, data model.AccountExistsTemplateData) error {
	subjectT, bodyT, err := s.getTemplate(ctx, model.AccountExistsTemplate)
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to get email template")
	}

	subject, err := s.populatedWithData(subjectT, data)
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to populate subject with data")
	}

	body, err := s.populatedWithData(bodyT, data)
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to populate body with data")
	}

	if err = s.repository.SendEmail(sendTo, subject, body); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to send email")
	}
	return nil
}

func (s *EmailService) SendPasswordResettingEmail(ctx context.Context, sendTo string, data model.PasswordResettingTemplateData) error {
	subjectT, bodyT, err := s.getTemplate(ctx, model.PasswordResettingTemplate)
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to get email template")
	}

	subject, err := s.populatedWithData(subjectT, data)
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to populate subject with data")
	}

	body, err := s.populatedWithData(bodyT, data)
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to populate body with data")
	}

	if err = s.repository.SendEmail(sendTo, subject, body); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to send email")
	}
	return nil
}

func (s *EmailService) SendEmailConfirmationEmail(ctx context.Context, sendTo string, data model.EmailConfirmationTemplateData) error {
	subjectT, bodyT, err := s.getTemplate(ctx, model.EmailConfirmationTemplate)
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to get email template")
	}

	subject, err := s.populatedWithData(subjectT, data)
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to populate subject with data")
	}

	body, err := s.populatedWithData(bodyT, data)
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to populate body with data")
	}

	if err = s.repository.SendEmail(sendTo, subject, body); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to send email")
	}
	return nil
}

func (s *EmailService) getTemplate(ctx context.Context, templateName string) (string, string, error) {
	// get email template
	body, err := s.repository.GetEmailTemplateBody(ctx, templateName)
	if err != nil {
		return "", "", appError.NewError().WithError(err).WithMessage("failed to get email template body")
	}

	subject, err := s.repository.GetEmailTemplateSubject(ctx, templateName)
	if err != nil {
		return "", "", appError.NewError().WithError(err).WithMessage("failed to get email template subject")
	}

	return subject, body, nil
}

func (s *EmailService) populatedWithData(tmpl string, data interface{}) (string, error) {
	var tpl bytes.Buffer

	t, err := template.New("template").Parse(tmpl)
	if err != nil {
		return "", appError.NewError().WithError(err).WithMessage("failed to parse template")
	}

	if err = t.Execute(&tpl, data); err != nil {
		return "", appError.NewError().WithError(err).WithMessage("failed to execute template")
	}

	return tpl.String(), nil
}
