package email

import (
	"context"
	"errors"
	"fmt"
	"io"

	"gopkg.in/gomail.v2"
)

var (
	ErrEmptyHost        = errors.New("email: SMTP host cannot be empty")
	ErrEmptySenderEmail = errors.New("email: sender email cannot be empty")
	ErrSendFailed       = errors.New("email: failed to send")
)

// TLSMode selects how the SMTP connection is secured.
type TLSMode string

const (
	// TLSModeStartTLS dials in plain text and upgrades with STARTTLS (587).
	TLSModeStartTLS TLSMode = "starttls"
	// TLSModeImplicit dials straight into TLS (465).
	TLSModeImplicit TLSMode = "tls"
)

// Address is a display name and mailbox; an empty Email means "unset".
type Address struct {
	Name  string
	Email string
}

// Config holds SMTP connection and sender identity settings.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	// TLS defaults to implicit TLS on port 465 and STARTTLS otherwise.
	TLS          TLSMode
	SenderName   string
	SenderEmail  string
	ReplyToName  string
	ReplyToEmail string
}

// Client sends HTML emails via SMTP.
type Client struct {
	dialer  *gomail.Dialer
	sender  Address
	replyTo Address
}

// New creates a new email Client from cfg. Returns ErrEmptyHost if Host is
// empty, ErrEmptySenderEmail if SenderEmail is empty. Defaults Port to 587.
func New(cfg Config) (*Client, error) {
	if cfg.Host == "" {
		return nil, ErrEmptyHost
	}
	if cfg.SenderEmail == "" {
		return nil, ErrEmptySenderEmail
	}
	port := cfg.Port
	if port == 0 {
		port = 587
	}
	dialer := gomail.NewDialer(cfg.Host, port, cfg.Username, cfg.Password)
	switch cfg.TLS {
	case TLSModeImplicit:
		dialer.SSL = true
	case TLSModeStartTLS:
		dialer.SSL = false
	}
	return &Client{
		dialer:  dialer,
		sender:  Address{Name: cfg.SenderName, Email: cfg.SenderEmail},
		replyTo: Address{Name: cfg.ReplyToName, Email: cfg.ReplyToEmail},
	}, nil
}

// InlinePart is an image embedded in the message body and referenced from the
// HTML as <img src="cid:ContentID">.
type InlinePart struct {
	ContentID   string
	ContentType string
	Data        []byte
}

// Message is one email to a single recipient. Text, when set, is sent as the
// text/plain alternative of HTML. From and ReplyTo override the client's
// configured identity when their Email is set.
type Message struct {
	To      string
	Subject string
	HTML    string
	Text    string
	Inline  []InlinePart
	From    Address
	ReplyTo Address
}

// SendMessage delivers msg to its recipient.
func (c *Client) SendMessage(_ context.Context, msg Message) error {
	from, replyTo := c.sender, c.replyTo
	if msg.From.Email != "" {
		from = msg.From
	}
	if msg.ReplyTo.Email != "" {
		replyTo = msg.ReplyTo
	}
	if err := c.dialer.DialAndSend(BuildMessage(from, replyTo, msg)); err != nil {
		return fmt.Errorf("%w: %w", ErrSendFailed, err)
	}
	return nil
}

// BuildMessage composes the MIME message. Inline parts make it
// multipart/related (with multipart/alternative nested inside when Text is
// set); there are never attachments, so it is never multipart/mixed and the
// images are not listed as files by mail clients. Display names are encoded
// as RFC 2047 words (Event names are Cyrillic). Exported for tests.
func BuildMessage(from, replyTo Address, msg Message) *gomail.Message {
	m := gomail.NewMessage()
	m.SetAddressHeader("From", from.Email, from.Name)
	if replyTo.Email != "" {
		m.SetAddressHeader("Reply-To", replyTo.Email, replyTo.Name)
	}
	m.SetHeader("To", msg.To)
	m.SetHeader("Subject", msg.Subject)
	if msg.Text != "" {
		m.SetBody("text/plain", msg.Text)
		m.AddAlternative("text/html", msg.HTML)
	} else {
		m.SetBody("text/html", msg.HTML)
	}
	for _, part := range msg.Inline {
		data := part.Data
		m.Embed(part.ContentID,
			gomail.SetCopyFunc(func(w io.Writer) error {
				_, err := w.Write(data)
				return err
			}),
			gomail.SetHeader(map[string][]string{
				"Content-ID":          {"<" + part.ContentID + ">"},
				"Content-Type":        {part.ContentType},
				"Content-Disposition": {"inline"},
			}),
		)
	}
	return m
}
