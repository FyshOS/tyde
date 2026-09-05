package updates

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestParsePacmanUpdates(t *testing.T) {
	out := `aardvark-dns 1.17.1-1 -> 2.0.0-1
alsa-card-profiles 1:1.6.4-1 -> 1:1.6.8-1
linux 6.1-1 -> 6.2-1 [ignored]

glibc 2.38-1 -> 2.39-1
`

	ups := parsePacmanUpdates(out)
	if len(ups) != 3 {
		t.Fatalf("expected 3 updates, got %d: %v", len(ups), ups)
	}

	if ups[0].Name != "aardvark-dns" || ups[0].OldVersion != "1.17.1-1" || ups[0].NewVersion != "2.0.0-1" {
		t.Errorf("unexpected first update: %+v", ups[0])
	}
	// Epoch-prefixed versions must survive intact.
	if ups[1].OldVersion != "1:1.6.4-1" || ups[1].NewVersion != "1:1.6.8-1" {
		t.Errorf("epoch version mangled: %+v", ups[1])
	}
	for _, up := range ups {
		if up.Name == "linux" {
			t.Error("an [ignored] package was reported as upgradable")
		}
	}
}

func TestParsePacmanUpdatesEmpty(t *testing.T) {
	if ups := parsePacmanUpdates(""); len(ups) != 0 {
		t.Errorf("expected no updates, got %v", ups)
	}
}

func TestParseAptUpdates(t *testing.T) {
	out := `NOTE: This is only a simulation!
Reading package lists...
Inst libc6 [2.36-9] (2.36-9+deb12u1 Debian:12.5/stable [amd64])
Inst tzdata [2024a-0] (2024b-0 Debian:12.5/stable [all])
Inst newpkg (1.0-1 Debian:12.5/stable [amd64])
Conf libc6 (2.36-9+deb12u1 Debian:12.5/stable [amd64])
`

	ups := parseAptUpdates(out)
	if len(ups) != 3 {
		t.Fatalf("expected 3 updates, got %d: %v", len(ups), ups)
	}

	if ups[0].Name != "libc6" || ups[0].OldVersion != "2.36-9" || ups[0].NewVersion != "2.36-9+deb12u1" {
		t.Errorf("unexpected first update: %+v", ups[0])
	}
	// A newly pulled-in package has no current version to report.
	if ups[2].Name != "newpkg" || ups[2].OldVersion != "" || ups[2].NewVersion != "1.0-1" {
		t.Errorf("unexpected new package: %+v", ups[2])
	}
	// Conf lines describe configuration of an already-counted package.
	for _, up := range ups {
		if strings.HasPrefix(up.Name, "Conf") {
			t.Error("a Conf line was parsed as an update")
		}
	}
}

func TestVersionChange(t *testing.T) {
	if got := versionChange(Update{OldVersion: "1", NewVersion: "2"}); got != "1 -> 2" {
		t.Errorf("got %q", got)
	}
	if got := versionChange(Update{NewVersion: "2"}); got != "2 (new)" {
		t.Errorf("got %q", got)
	}
}

func TestUpdateCount(t *testing.T) {
	if got := updateCount(1); got != "1 update" {
		t.Errorf("got %q", got)
	}
	if got := updateCount(3); got != "3 updates" {
		t.Errorf("got %q", got)
	}
}

func TestIsUnknownFlag(t *testing.T) {
	if !isUnknownFlag([]byte("pacman: unrecognized option '--disable-sandbox'")) {
		t.Error("expected an unrecognised option to be detected")
	}
	if isUnknownFlag([]byte("error: failed to synchronize all databases")) {
		t.Error("a genuine failure was mistaken for an unknown flag")
	}
}

// TestInstallErrorAuthCancelled covers the case that matters most for trust: a
// dismissed password prompt must not be reported as a broken upgrade.
func TestInstallErrorAuthCancelled(t *testing.T) {
	err := installError(exitErrorWithCode(t, 126), "some output")
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("expected a cancellation message, got %q", err)
	}

	err = installError(exitErrorWithCode(t, 127), "")
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("expected an authentication failure message, got %q", err)
	}
}

func TestInstallErrorReportsCommandOutput(t *testing.T) {
	err := installError(exitErrorWithCode(t, 1), "error: failed to commit transaction\nerror: could not open file")
	if !strings.Contains(err.Error(), "could not open file") {
		t.Errorf("expected the package manager's own diagnostics, got %q", err)
	}
}

func TestRunErrorPrefersCommandOutput(t *testing.T) {
	err := runError("could not refresh", []byte("error: no mirrors configured"), errors.New("exit status 1"))
	if !strings.Contains(err.Error(), "no mirrors configured") {
		t.Errorf("expected command output in the error, got %q", err)
	}

	// With nothing on either stream we still have to say something useful.
	err = runError("could not refresh", nil, errors.New("exit status 1"))
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Errorf("expected the underlying error, got %q", err)
	}
}

// exitErrorWithCode produces a real *exec.ExitError carrying the given status,
// so installError is tested against the type it actually sees.
func exitErrorWithCode(t *testing.T, code int) error {
	t.Helper()

	err := exec.Command("sh", "-c", "exit "+itoa(code)).Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("expected an ExitError, got %v", err)
	}
	if exit.ExitCode() != code {
		t.Fatalf("expected exit code %d, got %d", code, exit.ExitCode())
	}
	return err
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
