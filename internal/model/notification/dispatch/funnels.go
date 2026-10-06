package dispatchModel

// MailFunnels are the raw outcome figures behind the funnel cards of the mail
// analytics, read from data that already exists (no email tracking).
type MailFunnels struct {
	InvitationsSent                  int64
	InvitationsAccepted              int64
	InvitationAcceptSamples          int64
	InvitationAcceptMedianSeconds    float64
	RegistrationsStarted             int64
	RegistrationsCompleted           int64
	ApplicationsSubmitted            int64
	ApplicationsApproved             int64
	ApplicationsRejected             int64
	ApplicationDecisionSamples       int64
	ApplicationDecisionMedianSeconds float64
}

type (
	// FunnelsSummary is the funnel cards. Rates are 0..1 and null when the
	// denominator is zero; medians are seconds and null without a sample.
	FunnelsSummary struct {
		Invitations  InvitationFunnel   `json:"Invitations"`
		Registration RegistrationFunnel `json:"Registration"`
		Applications ApplicationFunnel  `json:"Applications"`
	}

	// InvitationFunnel: invitations sent -> accepted.
	InvitationFunnel struct {
		Sent                int64    `json:"Sent"`
		Accepted            int64    `json:"Accepted"`
		AcceptRate          *float64 `json:"AcceptRate"`
		MedianAcceptSeconds *int64   `json:"MedianAcceptSeconds"`
	}

	// RegistrationFunnel: registration started -> completed.
	RegistrationFunnel struct {
		Started        int64    `json:"Started"`
		Completed      int64    `json:"Completed"`
		CompletionRate *float64 `json:"CompletionRate"`
	}

	// ApplicationFunnel: applications submitted -> decided. Approved and
	// Rejected rates are shares of the decided ones.
	ApplicationFunnel struct {
		Submitted             int64    `json:"Submitted"`
		Decided               int64    `json:"Decided"`
		Approved              int64    `json:"Approved"`
		Rejected              int64    `json:"Rejected"`
		DecidedRate           *float64 `json:"DecidedRate"`
		ApprovedRate          *float64 `json:"ApprovedRate"`
		RejectedRate          *float64 `json:"RejectedRate"`
		MedianDecisionSeconds *int64   `json:"MedianDecisionSeconds"`
	}
)

func share(part, whole int64) *float64 {
	if whole <= 0 {
		return nil
	}
	v := float64(part) / float64(whole)
	return &v
}

func median(seconds float64, samples int64) *int64 {
	if samples <= 0 {
		return nil
	}
	v := int64(seconds + 0.5)
	return &v
}

// Summary turns the raw figures into the funnel cards.
func (f MailFunnels) Summary() FunnelsSummary {
	decided := f.ApplicationsApproved + f.ApplicationsRejected
	return FunnelsSummary{
		Invitations: InvitationFunnel{
			Sent: f.InvitationsSent, Accepted: f.InvitationsAccepted,
			AcceptRate:          share(f.InvitationsAccepted, f.InvitationsSent),
			MedianAcceptSeconds: median(f.InvitationAcceptMedianSeconds, f.InvitationAcceptSamples),
		},
		Registration: RegistrationFunnel{
			Started: f.RegistrationsStarted, Completed: f.RegistrationsCompleted,
			CompletionRate: share(f.RegistrationsCompleted, f.RegistrationsStarted),
		},
		Applications: ApplicationFunnel{
			Submitted: f.ApplicationsSubmitted, Decided: decided,
			Approved: f.ApplicationsApproved, Rejected: f.ApplicationsRejected,
			DecidedRate:           share(decided, f.ApplicationsSubmitted),
			ApprovedRate:          share(f.ApplicationsApproved, decided),
			RejectedRate:          share(f.ApplicationsRejected, decided),
			MedianDecisionSeconds: median(f.ApplicationDecisionMedianSeconds, f.ApplicationDecisionSamples),
		},
	}
}
