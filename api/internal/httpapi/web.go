package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"repopilot/api/web"
)

// The page needs nothing from another origin, so the policy allows none. Even if a rendering bug let hostile text from a
// repository or the model into the page, injected script would be blocked.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

type asset struct {
	route       string
	file        string
	contentType string
}

var assets = []asset{
	{"/", "index.html", "text/html; charset=utf-8"},
	{"/terminal.css", "terminal.css", "text/css; charset=utf-8"},
	{"/core.js", "core.js", "text/javascript; charset=utf-8"},
	{"/terminal.js", "terminal.js", "text/javascript; charset=utf-8"},
	{"/selftest.js", "selftest.js", "text/javascript; charset=utf-8"},
}

// registerWeb serves the embedded UI and answers unknown paths with the JSON error shape instead of gin's plain text.
func registerWeb(r *gin.Engine) {
	for _, a := range assets {
		body, err := web.FS.ReadFile(a.file)
		if err != nil {
			panic("embedded file missing: " + a.file) // a build error, caught by TestEmbeddedFilesMatchDirectory
		}
		contentType := a.contentType
		r.GET(a.route, func(c *gin.Context) {
			h := c.Writer.Header()
			h.Set("Content-Security-Policy", contentSecurityPolicy)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Cache-Control", "no-cache")
			c.Data(http.StatusOK, contentType, body)
		})
	}
	r.GET("/favicon.ico", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.NoRoute(func(c *gin.Context) {
		writeError(c, http.StatusNotFound, "not_found", "not found")
	})
}
