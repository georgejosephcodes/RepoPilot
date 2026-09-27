package retrieval

import (
	"path"
	"strings"
)

var testDirs = map[string]bool{"test": true, "tests": true, "__tests__": true, "testdata": true, "spec": true}

// IsTestPath reports whether a repository-relative path looks like test code: a directory named test, tests,
// __tests__, testdata or spec, or a file named test_*.py, *_test.py, *_test.go, conftest.py, *.test.* or *.spec.*
// (JavaScript and TypeScript). A heuristic: a project that keeps real code under spec/ would be misjudged.
func IsTestPath(p string) bool {
	p = strings.ToLower(p)
	dir, name := path.Split(p)
	for _, seg := range strings.Split(strings.Trim(dir, "/"), "/") {
		if testDirs[seg] {
			return true
		}
	}
	if name == "conftest.py" || strings.HasSuffix(name, "_test.go") {
		return true
	}
	if strings.HasSuffix(name, ".py") && (strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test.py")) {
		return true
	}
	for _, ext := range []string{".js", ".jsx", ".ts", ".tsx"} {
		if strings.HasSuffix(name, ".test"+ext) || strings.HasSuffix(name, ".spec"+ext) {
			return true
		}
	}
	return false
}
