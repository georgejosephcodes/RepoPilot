// Package web holds the terminal-style UI. The files are compiled into the API binary.
package web

import "embed"

// Files lists every embedded file. A test checks it against the directory so a new file cannot be forgotten.
var Files = []string{"index.html", "terminal.css", "core.js", "terminal.js", "selftest.js"}

//go:embed index.html terminal.css core.js terminal.js selftest.js
var FS embed.FS
