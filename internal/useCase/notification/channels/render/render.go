package render

import (
	"bytes"
	htmltemplate "html/template"
	"io"
	texttemplate "text/template"

	"github.com/cybericebox/daemon/internal/model"
)

// RenderText renders a text/template with missingkey=error semantics.
func RenderText(tmpl string, vars map[string]any) (string, error) {
	t, err := texttemplate.New("t").Option("missingkey=error").Parse(tmpl)
	return render(vars, t, err)
}

// RenderHTML renders an html/template (contextual escaping) with missingkey=error.
func RenderHTML(tmpl string, vars map[string]any) (string, error) {
	t, err := htmltemplate.New("t").Option("missingkey=error").Parse(tmpl)
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
	var b bytes.Buffer
	if err = t.Execute(&b, vars); err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to render template").Err()
	}
	return b.String(), nil
}
