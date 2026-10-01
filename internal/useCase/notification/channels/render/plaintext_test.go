package render

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPlainText(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"strips tags":           {`<p>Hello <b>Bob</b></p>`, "Hello Bob"},
		"collapses whitespace":  {"<div>\n  a \t\n</div>\n<div>b</div>", "a\n\nb"},
		"paragraphs and breaks": {`<p>Hi</p><p>Best regards,<br/>Team<br/>site</p>`, "Hi\n\nBest regards,\nTeam\nsite"},
		"unescapes entities":    {`<p>Tom &amp; Jerry &lt;3 &#39;x&#39;</p>`, "Tom & Jerry <3 'x'"},
		"tags separate words":   {`<td>one</td><td>two</td>`, "one two"},
		"drops style and head":  {`<html><head><title>T</title><style>p{color:red}</style></head><body><p>x</p></body></html>`, "x"},
		"drops script":          {`<script>alert(1)</script>ok`, "ok"},
		"empty":                 {``, ""},
		// Links keep their target: text-only readers of reset/confirm mails
		// must still be able to follow them.
		"link keeps url":         {`<p>Click <a href="https://x.test/reset?t=1&amp;u=2" style="color:red">Reset password</a> now</p>`, "Click Reset password (https://x.test/reset?t=1&u=2) now"},
		"link label equals url":  {`<a href="https://x.test/a">https://x.test/a</a>`, "https://x.test/a"},
		"link nested label tags": {`<a href='https://x.test/b'><span><b>Open</b> it</span></a>`, "Open it (https://x.test/b)"},
		"image-only link":        {`<a href="https://x.test/c"><img src="cid:logo"></a>`, "https://x.test/c"},
		"link without href":      {`<a name="top">Top</a>`, "Top"},
		"entity label and url":   {`<a href="https://x.test/?q=a&amp;amp;b">Tom &amp; Jerry</a>`, "Tom & Jerry (https://x.test/?q=a&amp;b)"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, PlainText(tc.in))
		})
	}
}
