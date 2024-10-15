package email

import (
	"bytes"
	"context"
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
	if err := s.sendEmailWithTemplate(ctx, sendTo, model.ContinueRegistrationTemplate, data); err != nil {
		return model.ErrEmail.WithError(err).WithMessage("Failed to send email").Cause()
	}
	return nil
}

func (s *EmailService) SendInvitationToRegistrationEmail(ctx context.Context, sendTo string, data model.InvitationToRegistrationTemplateData) error {
	if err := s.sendEmailWithTemplate(ctx, sendTo, model.InvitationToRegistrationTemplate, data); err != nil {
		return model.ErrEmail.WithError(err).WithMessage("Failed to send email").Cause()
	}
	return nil
}

func (s *EmailService) SendAccountExistsEmail(ctx context.Context, sendTo string, data model.AccountExistsTemplateData) error {
	if err := s.sendEmailWithTemplate(ctx, sendTo, model.AccountExistsTemplate, data); err != nil {
		return model.ErrEmail.WithError(err).WithMessage("Failed to send email").Cause()
	}
	return nil
}

func (s *EmailService) SendPasswordResettingEmail(ctx context.Context, sendTo string, data model.PasswordResettingTemplateData) error {
	if err := s.sendEmailWithTemplate(ctx, sendTo, model.PasswordResettingTemplate, data); err != nil {
		return model.ErrEmail.WithError(err).WithMessage("Failed to send email").Cause()
	}
	return nil
}

func (s *EmailService) SendEmailConfirmationEmail(ctx context.Context, sendTo string, data model.EmailConfirmationTemplateData) error {
	if err := s.sendEmailWithTemplate(ctx, sendTo, model.EmailConfirmationTemplate, data); err != nil {
		return model.ErrEmail.WithError(err).WithMessage("Failed to send email").Cause()
	}
	return nil
}

func (s *EmailService) getTemplate(ctx context.Context, templateName string) (string, string, error) {
	// get email template
	// error with context
	baseError := model.ErrEmail.WithContext("templateName", templateName)

	body, err := s.repository.GetEmailTemplateBody(ctx, templateName)
	if err != nil {
		return "", "", baseError.WithError(err).WithMessage("Failed to get email template body").Cause()
	}

	subject, err := s.repository.GetEmailTemplateSubject(ctx, templateName)
	if err != nil {
		return "", "", baseError.WithError(err).WithMessage("Failed to get email template subject").Cause()
	}

	return subject, body, nil
}

func (s *EmailService) populatedWithData(tmpl string, data interface{}) (string, error) {
	var tpl bytes.Buffer

	t, err := template.New("template").Parse(tmpl)
	if err != nil {
		return "", model.ErrEmail.WithError(err).WithMessage("Failed to parse template").Cause()
	}

	if err = t.Execute(&tpl, data); err != nil {
		return "", model.ErrEmail.WithError(err).WithMessage("Failed to execute template").Cause()
	}

	return tpl.String(), nil
}

func (s *EmailService) sendEmailWithTemplate(ctx context.Context, sendTo, templateName string, data interface{}) error {
	baseError := model.ErrEmail.WithContext("sendTo", sendTo).WithContext("type", templateName)

	subjectT, bodyT, err := s.getTemplate(ctx, templateName)
	if err != nil {
		return baseError.WithError(err).WithMessage("Failed to get email template").Cause()
	}

	subject, err := s.populatedWithData(subjectT, data)
	if err != nil {
		return baseError.WithError(err).WithMessage("Failed to populate subject with data").Cause()
	}

	body, err := s.populatedWithData(bodyT, data)
	if err != nil {
		return baseError.WithError(err).WithMessage("Failed to populate body with data").Cause()
	}

	if err = s.repository.SendEmail(sendTo, subject, body); err != nil {
		return baseError.WithError(err).WithMessage("Failed to send email").Cause()
	}
	return nil
}
