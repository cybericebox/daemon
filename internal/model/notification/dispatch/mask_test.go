package dispatchModel

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMaskEmail(t *testing.T) {
	require.Equal(t, "o***@example.com", MaskEmail("olena@example.com"))
	require.Equal(t, "о***@приклад.укр", MaskEmail("олена@приклад.укр"))
	require.Equal(t, "***@example.com", MaskEmail("@example.com"))
	require.Empty(t, MaskEmail(""))
	require.Equal(t, "no address", MaskEmail("no address"))
}

func TestMaskRecipients_HidesEveryAddressAndLeavesTheRest(t *testing.T) {
	rows := []DispatchDetail{
		{
			DispatchInfo: DispatchInfo{
				RecipientEmail: "olena@example.com",
				RecipientName:  "Олена",
				Status:         "error",
			},
			Targets: []DispatchTarget{
				{
					Channel:       "email",
					Status:        "error",
					Recipient:     "olena@example.com",
					Error:         "550 5.1.1 <olena@example.com>: mailbox unavailable",
					FallbackError: "553 bad sender team@uni.edu.ua",
				},
			},
		},
	}
	masked := MaskRecipients(rows)
	require.Equal(t, "o***@example.com", masked[0].RecipientEmail)
	require.Equal(t, "Олена", masked[0].RecipientName)
	require.Equal(t, "o***@example.com", masked[0].Targets[0].Recipient)
	require.Equal(
		t,
		"550 5.1.1 <o***@example.com>: mailbox unavailable",
		masked[0].Targets[0].Error,
	)
	require.Equal(t, "553 bad sender t***@uni.edu.ua", masked[0].Targets[0].FallbackError)
	// The input is not changed.
	require.Equal(t, "olena@example.com", rows[0].Targets[0].Recipient)
}

// Unicode local parts and IDN domains in a server reply are masked too, and the row's own address is masked
// wherever it appears.
func TestMaskRecipients_UnicodeAndIDNAddresses(t *testing.T) {
	rows := []DispatchDetail{{
		DispatchInfo: DispatchInfo{RecipientEmail: "олена@приклад.укр"},
		Targets: []DispatchTarget{{
			Recipient:     "олена@приклад.укр",
			Error:         "550 5.1.1 олена@приклад.укр: user unknown; cc: müller@bücher.de",
			FallbackError: `553 rejected "jose@xn--bcher-kva.de"`,
		}},
	}}
	masked := MaskRecipients(rows)[0].Targets[0]
	require.Equal(t, "о***@приклад.укр", masked.Recipient)
	require.Equal(t, "550 5.1.1 о***@приклад.укр: user unknown; cc: m***@bücher.de", masked.Error)
	require.Equal(t, `553 rejected "j***@xn--bcher-kva.de"`, masked.FallbackError)
	require.NotContains(t, masked.Error, "олена@")
}
