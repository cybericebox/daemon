package event

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
)

// A wait travels from the deploy step to the place that records it as a machine code; the need and the room of a
// refusal for lack of room ride along after a separator.
const waitDetailSeparator = "\x1f"

func withWaitDetail(reason string, detail *eventStandModel.WaitDetail) string {
	if detail == nil {
		return reason
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return reason
	}
	return reason + waitDetailSeparator + string(raw)
}

// recordWaitDetail keeps what the waiting Lab needs and what the reservation has left, or forgets it when the wait
// says nothing of the kind.
func (u *EventUseCase) recordWaitDetail(ctx context.Context, binding labBindingModel.Binding, detail *eventStandModel.WaitDetail, now time.Time) {
	if u.waitDetails == nil {
		return
	}
	var err error
	if detail != nil {
		err = u.waitDetails.Set(ctx, binding.ID, *detail, now)
	} else if binding.WaitReason != "" {
		err = u.waitDetails.Clear(ctx, binding.ID)
	}
	if err != nil {
		log.Warn().Err(err).Str("lab_group", binding.LabGroupName).Str("lab", binding.LabName).Msg("Failed to record what a waiting stand lab needs")
	}
}

func splitWaitDetail(wait string) (reason string, detail *eventStandModel.WaitDetail) {
	reason, raw, found := strings.Cut(wait, waitDetailSeparator)
	if !found {
		return wait, nil
	}
	var d eventStandModel.WaitDetail
	if json.Unmarshal([]byte(raw), &d) != nil {
		return reason, nil
	}
	return reason, &d
}
