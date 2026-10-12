package render_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRichTextElementAlignmentSurvivesEmailRendering(t *testing.T) {
	for _, typ := range []string{"paragraph", "heading", "quote", "list", "listitem", "root"} {
		for _, align := range []string{"left", "center", "right", "justify"} {
			t.Run(typ+"/"+align, func(t *testing.T) {
				content := fmt.Sprintf(`{"root":{"type":"root","children":[{"type":%q,"tag":"h2","listType":"bullet","format":%q,"children":[{"type":"text","text":"Formatted text","format":1}]}]}}`, typ, align)
				out, err := renderBlocks(json.RawMessage(`[{"type":"rich_text","content":`+content+`}]`), nil, nil, nil)
				require.NoError(t, err)
				assert.Contains(t, out, `text-align:`+align)
				assert.Contains(t, out, `align="`+align+`"`)
				assert.Contains(t, out, `<strong>Formatted text</strong>`, "numeric inline formatting is independent of element alignment")
			})
		}
	}
}

func TestElementAlignmentCannotInjectEmailMarkup(t *testing.T) {
	body := json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[{"type":"paragraph","format":"center\" onmouseover=\"bad()","children":[{"type":"text","text":"Safe"}]}]}}}]`)
	out, err := renderBlocks(body, nil, nil, nil)
	require.NoError(t, err)
	assert.NotContains(t, out, "onmouseover")
	assert.NotContains(t, out, "bad()")
	assert.Contains(t, out, "text-align:left")
}

func TestEmailMediaAndButtonAlignmentUsesTableCellFallback(t *testing.T) {
	for _, typ := range []string{"logo", "image", "button"} {
		for _, align := range []string{"", "left", "center", "right"} {
			t.Run(typ+"/"+align, func(t *testing.T) {
				body := json.RawMessage(fmt.Sprintf(`[{"type":%q,"align":%q,"url":"https://example.test/asset.png","label":"Open","width_pct":50}]`, typ, align))
				out, err := renderBlocks(body, nil, nil, nil)
				require.NoError(t, err)
				want := align
				if want == "" {
					want = "center"
				}
				assert.Contains(t, out, `<table role="presentation"`)
				assert.Contains(t, out, `<td align="`+want+`"`)
				assert.Contains(t, out, `text-align:`+want)
				if typ == "button" {
					margin := map[string]string{"left": "0", "center": "0 auto", "right": "0 0 0 auto"}[want]
					assert.Contains(t, out, `style="margin:`+margin+`"`, "the nested table must move, not only its text")
				}
			})
		}
	}
}
