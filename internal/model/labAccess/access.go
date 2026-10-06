// Package labAccessModel defines the transport-neutral desired Laboratory ACL.
package labAccessModel

// ClientPolicy is a complete replacement ACL for one LabGroup VPN client.
type ClientPolicy struct {
	Name        string
	AllowedLabs []string
}
