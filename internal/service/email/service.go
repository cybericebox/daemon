package emailService

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/cybericebox/daemon/internal/model/email"
	"html/template"
)

type (
	EmailService struct {
		repository IRepository
	}

	IRepository interface {
		GetPlatformSettings(ctx context.Context, key string) ([]byte, error)

		SendEmail(sendTo, subject, body string) error
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewService(deps Dependencies) *EmailService {
	return &EmailService{
		repository: deps.Repository,
	}
}

func (s *EmailService) SendContinueRegistrationEmail(ctx context.Context, sendTo string, data emailModel.ContinueRegistrationTemplateData) error {
	if err := s.sendEmailWithTemplate(ctx, sendTo, emailModel.ContinueRegistrationTemplate, data); err != nil {
		return emailModel.ErrEmail.WithError(err).WithMessage("Failed to send email").Err()
	}
	return nil
}

func (s *EmailService) SendInvitationToRegistrationEmail(ctx context.Context, sendTo string, data emailModel.InvitationToRegistrationTemplateData) error {
	if err := s.sendEmailWithTemplate(ctx, sendTo, emailModel.InvitationToRegistrationTemplate, data); err != nil {
		return emailModel.ErrEmail.WithError(err).WithMessage("Failed to send email").Err()
	}
	return nil
}

func (s *EmailService) SendAccountExistsEmail(ctx context.Context, sendTo string, data emailModel.AccountExistsTemplateData) error {
	if err := s.sendEmailWithTemplate(ctx, sendTo, emailModel.AccountExistsTemplate, data); err != nil {
		return emailModel.ErrEmail.WithError(err).WithMessage("Failed to send email").Err()
	}
	return nil
}

func (s *EmailService) SendPasswordResettingEmail(ctx context.Context, sendTo string, data emailModel.PasswordResettingTemplateData) error {
	if err := s.sendEmailWithTemplate(ctx, sendTo, emailModel.PasswordResettingTemplate, data); err != nil {
		return emailModel.ErrEmail.WithError(err).WithMessage("Failed to send email").Err()
	}
	return nil
}

func (s *EmailService) SendEmailConfirmationEmail(ctx context.Context, sendTo string, data emailModel.EmailConfirmationTemplateData) error {
	if err := s.sendEmailWithTemplate(ctx, sendTo, emailModel.EmailConfirmationTemplate, data); err != nil {
		return emailModel.ErrEmail.WithError(err).WithMessage("Failed to send email").Err()
	}
	return nil
}

func (s *EmailService) getTemplate(ctx context.Context, templateName string) (*emailModel.EmailTemplate, error) {
	// get email template
	// error with context
	baseError := emailModel.ErrEmail.WithContext("templateName", templateName)

	data, err := s.repository.GetPlatformSettings(ctx, templateName)
	if err != nil {
		return nil, err
	}

	emailTemplate := emailModel.EmailTemplate{}
	// get email template from data
	if err = json.Unmarshal(data, &emailTemplate); err != nil {
		return nil, baseError.WithError(err).WithMessage("Failed to unmarshal email template").Err()
	}

	return &emailTemplate, nil
}

func (s *EmailService) populatedWithData(tmpl string, data interface{}) (string, error) {
	var tpl bytes.Buffer

	t, err := template.New("template").Parse(tmpl)
	if err != nil {
		return "", emailModel.ErrEmail.WithError(err).WithMessage("Failed to parse template").Err()
	}

	if err = t.Execute(&tpl, data); err != nil {
		return "", emailModel.ErrEmail.WithError(err).WithMessage("Failed to execute template").Err()
	}

	return tpl.String(), nil
}

func (s *EmailService) sendEmailWithTemplate(ctx context.Context, sendTo, templateName string, data interface{}) error {
	baseError := emailModel.ErrEmail.WithContext("sendTo", sendTo).WithContext("type", templateName)

	t, err := s.getTemplate(ctx, templateName)
	if err != nil {
		return baseError.WithError(err).WithMessage("Failed to get email template").Err()
	}

	subject, err := s.populatedWithData(t.Subject, data)
	if err != nil {
		return baseError.WithError(err).WithMessage("Failed to populate subject with data").Err()
	}

	body, err := s.populatedWithData(t.Body, data)
	if err != nil {
		return baseError.WithError(err).WithMessage("Failed to populate body with data").Err()
	}

	if err = s.repository.SendEmail(sendTo, subject, body); err != nil {
		return baseError.WithError(err).WithMessage("Failed to send email").Err()
	}
	return nil
}
