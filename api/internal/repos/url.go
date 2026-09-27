package repos

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// ErrInvalidURL is wrapped by every error ParseGitHubURL returns.
var ErrInvalidURL = errors.New("invalid repository url")

// RepoRef is a validated repository. URL is rebuilt from Owner and Name,
// never copied from the raw input.
type RepoRef struct {
	Owner string
	Name  string
	URL   string
}

const maxURLLen = 200

var (
	// GitHub usernames: 1-39 chars, alphanumeric or inner hyphens.
	ownerRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
	nameRe  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

func invalid(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidURL, reason)
}

// ParseGitHubURL accepts only https://github.com/<owner>/<repo>, with an
// optional trailing ".git" and "/". The URL reaches `git clone`, so anything
// unusual is rejected instead of normalised.
func ParseGitHubURL(raw string) (RepoRef, error) {
	s := strings.Trim(raw, " \t\n\v\f\r") // ASCII whitespace only, same as the Python worker
	if s == "" {
		return RepoRef{}, invalid("empty")
	}
	if len(s) > maxURLLen {
		return RepoRef{}, invalid("too long")
	}
	for i := 0; i < len(s); i++ {
		// printable ASCII only: blocks control chars, spaces, unicode lookalikes
		if s[i] < 0x21 || s[i] > 0x7e {
			return RepoRef{}, invalid("contains a disallowed character")
		}
	}
	if strings.ContainsAny(s, `%\?#`) {
		return RepoRef{}, invalid("contains a disallowed character")
	}

	u, err := url.Parse(s)
	if err != nil {
		return RepoRef{}, invalid("not a valid URL")
	}
	if u.Scheme != "https" {
		return RepoRef{}, invalid("scheme must be https")
	}
	if u.User != nil || u.Opaque != "" {
		return RepoRef{}, invalid("credentials are not allowed")
	}
	if !strings.EqualFold(u.Host, "github.com") {
		return RepoRef{}, invalid("host must be github.com")
	}

	path := strings.TrimSuffix(u.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 3 || parts[0] != "" {
		return RepoRef{}, invalid("path must be /<owner>/<repo>")
	}
	owner, name := parts[1], parts[2]
	if !ownerRe.MatchString(owner) {
		return RepoRef{}, invalid("invalid owner")
	}
	if !nameRe.MatchString(name) || name == "." || name == ".." || strings.HasPrefix(name, "-") {
		return RepoRef{}, invalid("invalid repository name")
	}

	return RepoRef{Owner: owner, Name: name, URL: "https://github.com/" + owner + "/" + name}, nil
}
