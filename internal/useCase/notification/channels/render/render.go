package render

import (
	"bytes"
	"errors"
	htmltemplate "html/template"
	"io"
	texttemplate "text/template"

	"github.com/cybericebox/daemon/internal/model"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

// maxRenderedBytes bounds one rendered template string; variable values are the only thing that can make it
// longer than the template itself.
const maxRenderedBytes = 256 << 10

var errOutputTooLarge = errors.New("rendered template is too large")

// RenderText renders a text template with missingkey=error semantics. A template holds only {{.Variable}}
// substitutions (notificationTypes.ValidateTemplateSyntax): no function, condition, loop or nested template.
func RenderText(tmpl string, vars map[string]any) (string, error) {
	if err := notificationTypes.ValidateTemplateSyntax(tmpl); err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to parse template").Err()
	}
	t, err := texttemplate.New("t").Option("missingkey=error").Parse(notificationTypes.NormalizeTemplate(tmpl))
	return render(vars, t, err)
}

// RenderHTML renders an HTML template (contextual escaping) with missingkey=error, under the same restriction
// to variable substitution.
func RenderHTML(tmpl string, vars map[string]any) (string, error) {
	if err := notificationTypes.ValidateTemplateSyntax(tmpl); err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to parse template").Err()
	}
	t, err := htmltemplate.New("t").Option("missingkey=error").Parse(notificationTypes.NormalizeTemplate(tmpl))
	return render(vars, t, err)
}

func render(
	vars map[string]any,
	t interface {
		Execute(wr io.Writer, data any) error
	},
	err error,
) (string, error) {
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to parse template").Err()
	}
	var b cappedBuffer
	if err = t.Execute(&b, vars); err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to render template").Err()
	}
	return b.String(), nil
}

// cappedBuffer is a bytes.Buffer that refuses to grow past maxRenderedBytes.
type cappedBuffer struct{ bytes.Buffer }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > maxRenderedBytes {
		return 0, errOutputTooLarge
	}
	return b.Buffer.Write(p)
}
