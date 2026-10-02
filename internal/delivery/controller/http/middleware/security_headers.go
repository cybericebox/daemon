package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders sets the headers every API response should carry:
//   - the browser must not sniff a response into another type;
//   - no referrer leaks from a link followed out of an API page;
//   - Cross-Origin-Resource-Policy: same-origin: a no-cors load of an API URL (<img>, <script>, <link>)
//     from any other origin is blocked by the browser, including the pages of the same-site lab domain.
//     CORS fetches from the platform frontends are not affected (CORP does not apply to a request that
//     passes CORS). The few URLs our frontends load directly as images relax it to same-site with
//     SameSiteResource;
//   - frame-ancestors 'none' and X-Frame-Options: DENY: no page frames the API.
func SecurityHeaders(c *gin.Context) {
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Cross-Origin-Resource-Policy", "same-origin")
	c.Header("Content-Security-Policy", "frame-ancestors 'none'")
	c.Header("X-Frame-Options", "DENY")
	c.Next()
}

// SameSiteResource relaxes Cross-Origin-Resource-Policy to same-site on a route whose URL a platform
// frontend loads directly (an <img src> or <link rel=icon> on another subdomain of the platform
// domain): avatars, event images and the email template images of the editors.
func SameSiteResource(c *gin.Context) {
	c.Header("Cross-Origin-Resource-Policy", "same-site")
	c.Next()
}

// PublicMediaRoutes are the route templates that serve PUBLIC media only: unauthenticated image
// proxies whose URL an email, an external page or a link preview may embed. They carry
// Cross-Origin-Resource-Policy: cross-origin (PublicMedia); as public reads (PublicReadRoutes) the
// origin guard does not check their Origin or Referer. Keep this set and the routes that use
// PublicMedia identical: a test in the handler package compares them.
var PublicMediaRoutes = map[string]bool{
	"/api/auth/avatar/:id":                    true,
	"/api/events/:id/logo/:fileID":            true,
	"/api/events/:id/favicon/:fileID":         true,
	"/api/events/:id/preview-picture/:fileID": true,
	"/api/events/:id/content-images/:fileID":  true,
}

// PublicMedia is the route middleware of PublicMediaRoutes: any site may embed the response.
func PublicMedia(c *gin.Context) {
	c.Header("Cross-Origin-Resource-Policy", "cross-origin")
	c.Next()
}
