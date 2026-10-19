package inboxModel

// The cluster reservations: an event reservation ends while the event runs or was made smaller than what runs in
// it (the platform admins and the organizers hear it), and an author's test laboratory gets its ending notice or
// ends with its reservation. They exist only as inbox messages.
const (
	TypeReservationEnding  = "resources.reservation.ending"
	TypeReservationReduced = "resources.reservation.reduced"
	TypeTestLabEnding      = "testlab.ending"
	TypeTestLabEnded       = "testlab.ended"
)

func init() {
	for _, typ := range []string{TypeReservationEnding, TypeReservationReduced, TypeTestLabEnding, TypeTestLabEnded} {
		categoryRules[typ] = map[RecipientRole]Category{"": CategoryPersonal}
	}
}
