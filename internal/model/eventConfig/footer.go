package eventConfigModel

import (
	"encoding/json"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	"github.com/gofrs/uuid"
	"time"
)

// SetFooterSignature changes the website credit without changing service links.
func (c *EventConfig) SetFooterSignature(raw json.RawMessage, now time.Time, by uuid.UUID) error {
	normalized, err := eventContentModel.NormalizeOptionalRichText(raw)
	if err != nil || len(raw) > 20000 {
		return ErrFooterSignatureInvalid.WithError(err).Err()
	}
	c.FooterSignature = normalized
	c.touch(now, by)
	return nil
}
