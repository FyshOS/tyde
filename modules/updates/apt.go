package updates

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// aptBackend drives Debian and derivatives through apt-get.
type aptBackend struct {
	aptGet string
}

func (a *aptBackend) Name() string {
	return "apt"
}

func (a *aptBackend) UpgradeArgs() []string {
	// "upgrade" rather than "full-upgrade": it never removes an installed
	// package to satisfy a dependency, so an unattended run cannot silently
	// take away something the user relies on. DEBIAN_FRONTEND is set inline
	// because pkexec deliberately does not carry the caller's environment
	// through, and without it a maintainer script can block on a prompt
	// forever with nothing to display it.
	return []string{
		"/bin/sh", "-c",
		"apt-get update && DEBIAN_FRONTEND=noninteractive apt-get -y upgrade",
	}
}

// Check refreshes repository metadata into a private lists directory and then
// simulates an upgrade against it.
//
// Reading the system lists in /var/lib/apt/lists instead would be simpler, but
// those are only as fresh as the last apt-daily.timer run. If that timer is
// masked, or the machine is asleep during its window, the cache goes stale and
// the check reports "up to date" indefinitely - failing silently in the
// reassuring direction, which is the worst way for an update check to break.
// Syncing our own copy keeps the check honest without needing root.
func (a *aptBackend) Check(ctx context.Context) (Result, error) {
	opts, err := a.privateStateOpts()
	if err != nil {
		return Result{}, err
	}

	res := Result{}
	update := exec.CommandContext(ctx, a.aptGet, append([]string{"update"}, opts...)...)
	if out, err := update.CombinedOutput(); err != nil {
		// A refresh we cannot do is not a check we cannot do: fall back to the
		// system lists, but say so, because their age is not something we know.
		res.Stale = true
		res.StaleReason = runError("package lists could not be refreshed", out, err).Error()
		opts = nil
	}

	// -s simulates, so this only reports what would happen and needs no lock.
	sim := exec.CommandContext(ctx, a.aptGet, append([]string{"-s", "upgrade"}, opts...)...)
	out, err := sim.Output()
	if err != nil {
		return Result{}, runError("could not list available updates", exitOutput(out, err), err)
	}

	res.Updates = parseAptUpdates(string(out))
	return res, nil
}

// privateStateOpts builds the -o overrides that redirect apt's mutable state
// into our cache directory. Configuration still comes from /etc/apt, so the
// user's real sources and pinning apply. Because the lock files live under the
// overridden paths too, this cannot collide with an apt run in a terminal.
func (a *aptBackend) privateStateOpts() ([]string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, &backendError{what: "could not locate cache directory", detail: err.Error()}
	}
	dir := filepath.Join(cache, "tyde", "apt")

	lists := filepath.Join(dir, "lists")
	archives := filepath.Join(dir, "archives")
	// apt stages in-flight downloads in "partial" and fails if it is missing.
	for _, sub := range []string{filepath.Join(lists, "partial"), filepath.Join(archives, "partial")} {
		if err := os.MkdirAll(sub, 0o755); err != nil {
			return nil, &backendError{what: "could not create update cache", detail: err.Error()}
		}
	}

	return []string{
		"-o", "Dir::State::Lists=" + lists,
		"-o", "Dir::Cache::Archives=" + archives,
	}, nil
}

// aptInstLine matches apt's simulated install records, e.g.
//
//	Inst libc6 [2.36-9] (2.36-9+deb12u1 Debian:12.5/stable [amd64])
//
// The bracketed current version is absent for a package being newly pulled in.
var aptInstLine = regexp.MustCompile(`^Inst\s+(\S+)\s+(?:\[([^\]]+)\]\s+)?\((\S+)`)

// parseAptUpdates reads the Inst lines from an apt-get -s upgrade transcript.
func parseAptUpdates(out string) []Update {
	var ups []Update
	for _, line := range strings.Split(out, "\n") {
		m := aptInstLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		ups = append(ups, Update{Name: m[1], OldVersion: m[2], NewVersion: m[3]})
	}
	return ups
}
