package event

import (
	"fmt"

	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

// reservationShortage is a refusal for lack of room in the event reservation that says how much was needed and how
// much there is. It is a calModel.ErrNotEnoughReserved for every caller that only asks "does it fit".
type reservationShortage struct{ detail eventStandModel.WaitDetail }

func (s reservationShortage) Error() string {
	return fmt.Sprintf("%v: needs %dm CPU and %d bytes, has %dm CPU and %d bytes", calModel.ErrNotEnoughReserved.Err(),
		s.detail.NeedCPUMillicores, s.detail.NeedMemoryBytes, s.detail.HaveCPUMillicores, s.detail.HaveMemoryBytes)
}

func (s reservationShortage) Unwrap() error { return calModel.ErrNotEnoughReserved.Err() }
