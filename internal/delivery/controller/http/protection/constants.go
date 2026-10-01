package protection

// Auth redirect constants — delivery-layer concerns (URL paths and HTTP
// header names), so they live here rather than in the domain model.
const (
	// SignInPath is the sign-in page on the identity app (id.<domain>).
	SignInPath = "/sign-in"
	// SignInURLHeader carries the sign-in URL on 401 API responses so the client
	// can redirect without computing the address itself. Exposed cross-origin via
	// Access-Control-Expose-Headers (see the middleware package).
	SignInURLHeader = "X-Sign-In-URL"
)
