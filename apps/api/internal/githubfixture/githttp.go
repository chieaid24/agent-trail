package githubfixture

import (
	"errors"
	"net/http"
	"net/http/cgi"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitHandler serves the bare repository at origin over git's smart HTTP
// protocol by wrapping `git http-backend` as a CGI program. Clone and push
// are anonymous: this is a test fixture, never production serving. Mount it
// under prefix (e.g. "/git/"); the repository is addressed as
// <prefix>origin.git.
func GitHandler(origin, prefix string) (http.Handler, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, errors.New("git is required on PATH to serve the fixture repository")
	}
	return &cgi.Handler{
		Path: gitPath,
		Args: []string{"http-backend"},
		Root: strings.TrimSuffix(prefix, "/"),
		Env: []string{
			"GIT_PROJECT_ROOT=" + filepath.Dir(origin),
			"GIT_HTTP_EXPORT_ALL=1",
		},
	}, nil
}
