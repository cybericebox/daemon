package userModel

import "slices"

// The documents an installation may require on account creation, as the request names them.
const (
	LegalDocumentTerms   = "terms"
	LegalDocumentPrivacy = "privacy"
)

// legalDocuments is the canonical order.
var legalDocuments = []string{LegalDocumentTerms, LegalDocumentPrivacy}

// ValidLegalDocument reports whether name is a known document.
func ValidLegalDocument(name string) bool { return slices.Contains(legalDocuments, name) }

// CanonicalDocuments keeps the known documents of list once each, in the canonical order. It never returns nil.
func CanonicalDocuments(list []string) []string {
	out := make([]string, 0, len(legalDocuments))
	for _, name := range legalDocuments {
		if slices.Contains(list, name) {
			out = append(out, name)
		}
	}
	return out
}

// CoversDocuments reports whether accepted contains every document of required.
func CoversDocuments(accepted, required []string) bool {
	for _, name := range required {
		if !slices.Contains(accepted, name) {
			return false
		}
	}
	return true
}
