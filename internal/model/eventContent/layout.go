package eventContentModel

import "errors"

// Pointers distinguish an inherited default from an explicitly removed spacing.
type BlockSpacingValues struct {
	Top    *int `json:"top,omitempty"`
	Bottom *int `json:"bottom,omitempty"`
	Inline *int `json:"inline,omitempty"`
}

type BlockSpacing struct {
	Desktop *BlockSpacingValues `json:"desktop,omitempty"`
	Mobile  *BlockSpacingValues `json:"mobile,omitempty"`
}

func validateBlockLayout(block Block) error {
	if block.Spacing != nil {
		for _, values := range []*BlockSpacingValues{block.Spacing.Desktop, block.Spacing.Mobile} {
			if values == nil {
				continue
			}
			for _, value := range []*int{values.Top, values.Bottom, values.Inline} {
				if value != nil && (*value < 0 || *value > 160) {
					return errors.New("block spacing must be between 0 and 160 pixels")
				}
			}
		}
	}
	if block.MaxWidth != nil && (*block.MaxWidth < 320 || *block.MaxWidth > 1920) {
		return errors.New("block maximum width must be between 320 and 1920 pixels")
	}
	if block.ContentAlign != "" && block.ContentAlign != "start" && block.ContentAlign != "center" && block.ContentAlign != "end" {
		return errors.New("block content alignment is unsupported")
	}
	if block.TextColumns != nil && (block.Type != BlockText || (*block.TextColumns != 1 && *block.TextColumns != 2)) {
		return errors.New("text columns must be one or two and only apply to text blocks")
	}
	if block.MobileFullWidth && block.Type != BlockBanner {
		return errors.New("mobile full width only applies to banners")
	}
	return nil
}
