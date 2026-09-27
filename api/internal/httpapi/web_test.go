package httpapi

import (
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"repopilot/api/internal/repos"
	"repopilot/api/web"
)

func webRouter() http.Handler {
	store := &fakeStore{list: func() ([]repos.Repository, error) { return []repos.Repository{newRepo(1, "ready")}, nil }}
	return NewRouter(Deps{DB: fakePinger{}, Repos: store})
}

func TestWebRoutes(t *testing.T) {
	tests := []struct {
		path        string
		contentType string
	}{
		{"/", "text/html; charset=utf-8"},
		{"/terminal.css", "text/css; charset=utf-8"},
		{"/core.js", "text/javascript; charset=utf-8"},
		{"/terminal.js", "text/javascript; charset=utf-8"},
		{"/selftest.js", "text/javascript; charset=utf-8"},
	}
	r := webRouter()
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			w := do(r, http.MethodGet, tc.path, "")
			if w.Code != http.StatusOK {
				t.Fatalf("got %d, want 200", w.Code)
			}
			if got := w.Header().Get("Content-Type"); got != tc.contentType {
				t.Errorf("Content-Type = %q, want %q", got, tc.contentType)
			}
			if w.Body.Len() == 0 {
				t.Error("empty body")
			}
			if got := w.Header().Get("Cache-Control"); got != "no-cache" {
				t.Errorf("Cache-Control = %q, want no-cache", got)
			}
		})
	}
}

func TestWebSecurityHeaders(t *testing.T) {
	r := webRouter()
	for _, a := range assets {
		w := do(r, http.MethodGet, a.route, "")
		csp := w.Header().Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "connect-src 'self'", "base-uri 'none'", "form-action 'none'", "frame-ancestors 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: CSP %q lacks %q", a.route, csp, want)
			}
		}
		if strings.Contains(csp, "unsafe") {
			t.Errorf("%s: CSP must not contain unsafe-*: %q", a.route, csp)
		}
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", a.route, got)
		}
		if got := w.Header().Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("%s: Referrer-Policy = %q", a.route, got)
		}
	}
}

func TestFavicon(t *testing.T) {
	w := do(webRouter(), http.MethodGet, "/favicon.ico", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204", w.Code)
	}
}

func TestUnknownPathsGiveJSONNotFound(t *testing.T) {
	r := webRouter()
	for _, path := range []string{"/nope", "/api/nope", "/index.html", "/static/x.js", "/api/repositories/1/nope"} {
		w := do(r, http.MethodGet, path, "")
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404", path, w.Code)
			continue
		}
		if code := errCode(t, w); code != "not_found" {
			t.Errorf("%s: error code %q, want not_found", path, code)
		}
	}
}

func TestAPIRoutesStillWinOverPages(t *testing.T) {
	r := webRouter()
	w := do(r, http.MethodGet, "/healthz", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Fatalf("/healthz: %d %s", w.Code, w.Body.String())
	}
	w = do(r, http.MethodGet, "/api/repositories", "")
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Body.String(), "[") {
		t.Fatalf("/api/repositories: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Security-Policy") != "" {
		t.Error("API responses do not carry the page policy")
	}
}

func TestEmbeddedFilesMatchDirectory(t *testing.T) {
	entries, err := os.ReadDir("../../web")
	if err != nil {
		t.Fatal(err)
	}
	var onDisk []string
	for _, e := range entries {
		// Hidden entries (editor or tool folders such as .claude) are not part of the UI.
		if !strings.HasSuffix(e.Name(), ".go") && !strings.HasPrefix(e.Name(), ".") {
			onDisk = append(onDisk, e.Name())
		}
	}
	listed := append([]string(nil), web.Files...)
	sort.Strings(onDisk)
	sort.Strings(listed)
	if strings.Join(onDisk, ",") != strings.Join(listed, ",") {
		t.Fatalf("web.Files = %v, files on disk = %v (update web.Files and the go:embed line)", listed, onDisk)
	}
	for _, name := range web.Files {
		if _, err := web.FS.ReadFile(name); err != nil {
			t.Errorf("%s is listed but not embedded: %v", name, err)
		}
	}
	served := map[string]bool{}
	for _, a := range assets {
		served[a.file] = true
	}
	for _, name := range web.Files {
		if !served[name] {
			t.Errorf("%s is embedded but not served", name)
		}
	}
}

func source(t *testing.T, name string) string {
	t.Helper()
	b, err := web.FS.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Everything the UI shows can be attacker-influenced (repository content, model output). The safe way to render it is
// textContent, so the APIs that parse markup or run strings as code must not appear at all.
func TestScriptsUseNoDangerousAPIs(t *testing.T) {
	banned := []string{
		"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "createContextualFragment", "DOMParser",
		"eval(", "new Function", `setTimeout("`, `setTimeout('`, `setInterval("`, `setInterval('`, "javascript:",
		"XMLHttpRequest", "WebSocket", "importScripts", "srcdoc",
	}
	for _, name := range []string{"core.js", "terminal.js", "selftest.js"} {
		src := source(t, name)
		for _, b := range banned {
			if strings.Contains(src, b) {
				t.Errorf("%s contains %q", name, b)
			}
		}
	}
}

func TestCoreHasNoDOMOrNetwork(t *testing.T) {
	src := source(t, "core.js")
	for _, b := range []string{"document.", "fetch(", "localStorage", "XMLHttpRequest"} {
		if strings.Contains(src, b) {
			t.Errorf("core.js must stay pure but contains %q", b)
		}
	}
}

var (
	scriptTag   = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)
	srcAttr     = regexp.MustCompile(`(?i)\ssrc\s*=\s*"([^"]*)"`)
	styleAttr   = regexp.MustCompile(`(?i)\sstyle\s*=`)
	handlerAttr = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
)

func TestIndexHTMLStructure(t *testing.T) {
	html := source(t, "index.html")

	for _, m := range scriptTag.FindAllStringSubmatch(html, -1) {
		if strings.TrimSpace(m[2]) != "" {
			t.Errorf("inline script body: %q", m[2])
		}
		src := srcAttr.FindStringSubmatch(m[1])
		if src == nil {
			t.Errorf("script without src: %q", m[1])
			continue
		}
		if !strings.HasPrefix(src[1], "/") || strings.HasPrefix(src[1], "//") {
			t.Errorf("script src is not same-origin: %q", src[1])
		}
	}
	if strings.Count(strings.ToLower(html), "<script") != len(scriptTag.FindAllString(html, -1)) {
		t.Error("a <script> tag is not closed properly")
	}
	if strings.Contains(strings.ToLower(html), "<style") {
		t.Error("inline <style> element")
	}
	if styleAttr.MatchString(html) {
		t.Error("style attribute")
	}
	if handlerAttr.MatchString(html) {
		t.Error("event-handler attribute")
	}
	for _, bad := range []string{"http://", "https://", "javascript:"} {
		if strings.Contains(html, bad) {
			t.Errorf("index.html contains %q", bad)
		}
	}
	for _, need := range []string{`role="log"`, `id="cmd"`, `<label for="cmd"`, `<html lang=`, `name="viewport"`} {
		if !strings.Contains(html, need) {
			t.Errorf("index.html lacks %s", need)
		}
	}
}

func TestCSSHasNoExternalResources(t *testing.T) {
	css := source(t, "terminal.css")
	for _, bad := range []string{"@import", "http://", "https://", "url("} {
		if strings.Contains(css, bad) {
			t.Errorf("terminal.css contains %q", bad)
		}
	}
}
