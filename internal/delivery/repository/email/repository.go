package email

import (
	"fmt"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/rs/zerolog/log"
	"gopkg.in/gomail.v2"
)

type (
	EmailRepository struct {
		dialer  *gomail.Dialer
		sender  string
		replyTo string
	}

	Dependencies struct {
		Config *config.EmailConfig
	}
)

func NewRepository(deps Dependencies) *EmailRepository {
	return &EmailRepository{
		dialer:  gomail.NewDialer(deps.Config.Host, deps.Config.Port, deps.Config.Username, deps.Config.Password),
		sender:  fmt.Sprintf("%s <%s>", deps.Config.SenderName, deps.Config.SenderEmail),
		replyTo: fmt.Sprintf("%s <%s>", deps.Config.ReplyToName, deps.Config.ReplyToEmail),
	}
}

func (r *EmailRepository) SendEmail(to, subject, body string) error {
	message := gomail.NewMessage()
	message.SetHeader("From", r.sender)
	message.SetHeader("Reply-To", r.replyTo)
	message.SetHeader("To", to)

	message.SetHeader("Subject", subject)

	message.SetBody("text/html", body)

	log.Debug().Msgf("Sending email to %s", to)

	if err := r.dialer.DialAndSend(message); err != nil {
		return err
	}

	return nil
}
