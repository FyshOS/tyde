// Package updates reports pending system package updates in the status area and
// applies them from the settings window.
package updates

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// Update describes a single package that has a newer version available.
type Update struct {
	Name       string
	OldVersion string // empty for a package that is newly pulled in
	NewVersion string
}

// Result is the outcome of a successful check.
type Result struct {
	Updates []Update

	// Stale is set when repository metadata could not be refreshed and the
	// answer came from whatever the system had already cached. The updates
	// listed are still real, but there may be newer ones we cannot see - so
	// an empty list must not be presented as "you are up to date".
	Stale bool

	// StaleReason explains, in the user's terms, why the refresh was skipped.
	StaleReason string
}

// Backend abstracts a system package manager. Implementations must make Check
// safe to run unprivileged and safe to run concurrently with normal package
// manager use - it must never take the system package database lock, because
// doing so would break an unrelated install the user is running in a terminal.
type Backend interface {
	// Name is the package manager's name, shown in the settings panel.
	Name() string

	// Check returns the updates currently available. It refreshes repository
	// metadata into a private database first, so a result of "no updates"
	// means the mirrors really have nothing newer rather than merely that a
	// cached list is stale. It requires no privileges. Where the refresh is
	// not possible the result is returned with Stale set instead of failing.
	Check(context.Context) (Result, error)

	// UpgradeArgs is the argv, to be run via pkexec as root, that applies every
	// available update. It re-syncs metadata itself so it cannot act on the
	// stale view Check happened to see.
	UpgradeArgs() []string
}

// DetectBackend returns the backend matching this system, or nil if the
// package manager is not one we support. pacman is probed before apt because a
// handful of Arch systems carry an apt binary for cross-distro tooling, whereas
// a Debian system never carries pacman.
func DetectBackend() Backend {
	if path, err := exec.LookPath("pacman"); err == nil {
		return &pacmanBackend{pacman: path}
	}
	if path, err := exec.LookPath("apt-get"); err == nil {
		return &aptBackend{aptGet: path}
	}
	return nil
}

// runError turns a failed command into an error that carries the command's own
// diagnostics, which are far more useful than "exit status 1". Package managers
// report the real problem (no mirrors, bad signature, no network) on stderr.
func runError(what string, out []byte, err error) error {
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		return &backendError{what: what, detail: err.Error()}
	}

	// Keep the tail: package managers print progress first and the failure last.
	if lines := strings.Split(msg, "\n"); len(lines) > 6 {
		msg = strings.Join(lines[len(lines)-6:], "\n")
	}
	return &backendError{what: what, detail: msg}
}

// exitOutput joins a command's stdout with the stderr captured on its exit
// error, so runError can report diagnostics from whichever stream carried them.
func exitOutput(out []byte, err error) []byte {
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		return append(out, exit.Stderr...)
	}
	return out
}

type backendError struct {
	what   string
	detail string
}

func (e *backendError) Error() string {
	return e.what + ": " + e.detail
}
