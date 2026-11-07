package main

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// entrypointPath is the composition root this package is compiled from, relative
// to the module root.
const entrypointPath = "cmd/server/main.go"

// TestReadinessFailsOnceShutdownBegins proves the drain gate: as soon as shutdown
// starts every readiness probe fails, so no new traffic is routed to a dying
// instance. The dependency checks are never reached while draining, which is what
// keeps this test free of PostgreSQL and Kafka.
func TestReadinessFailsOnceShutdownBegins(t *testing.T) {
	t.Parallel()

	state := &readiness{}
	require.NoError(t, state.Ready(context.Background()))

	state.beginShutdown()

	err := state.Ready(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "draining")
}

// TestEntrypointGitIgnoreRulesKeepItTracked proves the repository keeps its
// entrypoint: a rule in .gitignore that matched cmd/server/main.go would hide the
// composition root from `git status` and from every fresh clone, so the build
// would break for everyone but the machine that created the file. The check is
// read-only and skips itself when git or a worktree is unavailable.
func TestEntrypointGitIgnoreRulesKeepItTracked(t *testing.T) {
	t.Parallel()

	root := requireGitWorktree(t)

	tracked, err := git(root, "ls-files", "--error-unmatch", entrypointPath)
	require.NoError(t, err, "%s must be tracked by git", entrypointPath)
	assert.Equal(t, entrypointPath, tracked)

	// `git check-ignore` exits 0 when a rule matches the path and 1 when none
	// does, so only exit code 1 proves .gitignore leaves the entrypoint alone.
	// --no-index asks the rules themselves: without it git answers for the index
	// and reports nothing for a file it already tracks, which is exactly the
	// case a new entrypoint file would face before its first `git add`.
	_, err = git(root, "check-ignore", "-q", "--no-index", entrypointPath)
	require.Error(t, err, "%s is ignored by the repository's ignore rules; the entrypoint must stay tracked", entrypointPath)

	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr), "git check-ignore failed unexpectedly: %v", err)
	assert.Equal(t, 1, exitErr.ExitCode(),
		"%s must not match any ignore rule (git check-ignore exited %d)", entrypointPath, exitErr.ExitCode())
}

// requireGitWorktree skips the calling test unless git can inspect this module
// root, so the suite still passes from a source snapshot without .git metadata.
func requireGitWorktree(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed; the ignore rules cannot be checked here")
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	if _, err := git(root, "rev-parse", "--is-inside-work-tree"); err != nil {
		t.Skip("not a git worktree; the ignore rules cannot be checked here")
	}

	return root
}

// git runs one read-only git command in dir and returns its trimmed stdout.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	output, err := cmd.Output()
	return strings.TrimSpace(string(output)), err
}
