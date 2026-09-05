package updates

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// pacmanBackend drives Arch Linux's pacman.
type pacmanBackend struct {
	pacman string
}

func (p *pacmanBackend) Name() string {
	return "pacman"
}

func (p *pacmanBackend) UpgradeArgs() []string {
	// A full -Syu, never a targeted upgrade: Arch does not support partial
	// upgrades and applying a subset is a documented way to break a system.
	return []string{p.pacman, "-Syu", "--noconfirm"}
}

// Check syncs the repository databases into a private copy and asks what is out
// of date, which is what the checkupdates script from pacman-contrib does. It
// is reimplemented here so the feature does not depend on that optional package.
//
// pacman refuses -Sy unless it is running as root - a plain euid check that
// applies even when the database it would write is our own - so the sync runs
// under fakeroot. Where fakeroot is unavailable we fall back to reading the
// system database and mark the result stale rather than reporting a failure.
func (p *pacmanBackend) Check(ctx context.Context) (Result, error) {
	db, err := p.privateDB()
	if err != nil {
		return Result{}, err
	}

	if reason := p.sync(ctx, db); reason != "" {
		ups, err := p.list(ctx, "") // system database: current, but possibly stale
		if err != nil {
			return Result{}, err
		}
		return Result{Updates: ups, Stale: true, StaleReason: reason}, nil
	}

	ups, err := p.list(ctx, db)
	if err != nil {
		return Result{}, err
	}
	return Result{Updates: ups}, nil
}

// sync refreshes the private database, returning a human-readable reason when
// it could not be done. A reason is not an error: the caller degrades to the
// system database instead of failing the whole check.
func (p *pacmanBackend) sync(ctx context.Context, db string) string {
	fakeroot, err := exec.LookPath("fakeroot")
	if err != nil {
		return "Repository metadata could not be refreshed because fakeroot is not installed. " +
			"Install the fakeroot package for an up-to-the-minute check."
	}

	// --disable-sandbox is needed under fakeroot: pacman 7 drops to the 'alpm'
	// user behind a Landlock ruleset, and neither is possible without real
	// privileges. The flag does not exist before pacman 7, so a run rejecting
	// it is retried without. Nothing is lost by disabling it here - the sandbox
	// protects the download, which we are writing to a throwaway directory.
	base := []string{p.pacman, "-Sy", "--dbpath", db, "--logfile", os.DevNull}
	out, err := p.runSync(ctx, fakeroot, append(base, "--disable-sandbox"))
	if err != nil && isUnknownFlag(out) {
		out, err = p.runSync(ctx, fakeroot, base)
	}
	if err != nil {
		return runError("could not refresh package databases", out, err).Error()
	}
	return ""
}

func (p *pacmanBackend) runSync(ctx context.Context, fakeroot string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, fakeroot, append([]string{"--"}, args...)...)
	return cmd.CombinedOutput()
}

// list reports out-of-date packages against the given database, or the system
// database when dbPath is empty.
func (p *pacmanBackend) list(ctx context.Context, dbPath string) ([]Update, error) {
	args := []string{"-Qu"}
	if dbPath != "" {
		args = append(args, "--dbpath", dbPath)
	}

	out, err := exec.CommandContext(ctx, p.pacman, args...).Output()
	if err != nil {
		// pacman -Qu exits non-zero purely to signal "nothing to upgrade", so an
		// exit status with nothing on either stream is the up-to-date case, not
		// a failure. Anything that did print is a real error.
		var exit *exec.ExitError
		if errors.As(err, &exit) && strings.TrimSpace(string(out)) == "" &&
			strings.TrimSpace(string(exit.Stderr)) == "" {
			return nil, nil
		}
		return nil, runError("could not list available updates", exitOutput(out, err), err)
	}
	return parsePacmanUpdates(string(out)), nil
}

// privateDB prepares the throwaway sync database, linking in the real local
// database so pacman can compare installed versions against fresh remotes.
func (p *pacmanBackend) privateDB() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", &backendError{what: "could not locate cache directory", detail: err.Error()}
	}
	db := filepath.Join(cache, "tyde", "pacman-db")
	if err := os.MkdirAll(filepath.Join(db, "sync"), 0o755); err != nil {
		return "", &backendError{what: "could not create update cache", detail: err.Error()}
	}

	// The local database records what is installed; it is only ever read here.
	local := filepath.Join(db, "local")
	if _, err := os.Lstat(local); os.IsNotExist(err) {
		if err := os.Symlink(filepath.Join(p.dbPath(), "local"), local); err != nil {
			return "", &backendError{what: "could not prepare update cache", detail: err.Error()}
		}
	}
	return db, nil
}

// dbPath resolves pacman's configured database directory, falling back to the
// default when pacman-conf is unavailable or the key is unset.
func (p *pacmanBackend) dbPath() string {
	const fallback = "/var/lib/pacman/"

	conf, err := exec.LookPath("pacman-conf")
	if err != nil {
		return fallback
	}
	out, err := exec.Command(conf, "DBPath").Output()
	if err != nil {
		return fallback
	}
	if path := strings.TrimSpace(string(out)); path != "" {
		return path
	}
	return fallback
}

// isUnknownFlag reports whether a command failed because it did not recognise
// an option, rather than because the operation itself went wrong.
func isUnknownFlag(out []byte) bool {
	msg := strings.ToLower(string(out))
	return strings.Contains(msg, "unrecognized option") ||
		strings.Contains(msg, "unrecognised option") ||
		strings.Contains(msg, "invalid option")
}

// parsePacmanUpdates reads the "name old -> new" lines of pacman -Qu.
func parsePacmanUpdates(out string) []Update {
	var ups []Update
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		// Packages held back by an IgnorePkg rule are annotated and will not be
		// upgraded, so reporting them would promise something we do not deliver.
		if line == "" || strings.Contains(line, "[ignored]") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 || fields[2] != "->" {
			continue
		}
		ups = append(ups, Update{Name: fields[0], OldVersion: fields[1], NewVersion: fields[3]})
	}
	return ups
}
