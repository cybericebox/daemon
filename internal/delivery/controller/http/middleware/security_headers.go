package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders sets the headers every API response should carry: the browser must not sniff a response
// into another type, and no referrer leaks from a link followed out of an API page.
func SecurityHeaders(c *gin.Context) {
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Next()
}
