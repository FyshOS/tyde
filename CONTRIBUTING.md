# Contributing

Thanks for your interest in contributing to Tyde.

Tyde is a Go desktop environment built with Fyne. Useful contributions are focused, based on the `develop` branch, and include tests or validation for the desktop, window manager, or platform behavior they affect.

## Getting Started

1. Install Go.
2. Fork and clone the repository.
3. Base your work on the `develop` branch.

```bash
git clone https://github.com/FyshOS/tyde.git
cd tyde
git checkout develop
git checkout -b your-change
```

Some local checks and tests need desktop or graphics-related system libraries. The GitHub Actions workflows install packages such as `clang`, `lld`, Mesa/OpenGL development libraries, X11/Wayland development libraries, `libpam-dev`, and `xvfb` on Linux.

## Local Tests

Run the Go test suite before opening a pull request:

```bash
go test -tags ci -covermode=atomic -coverprofile=coverage.out ./...
```

On Linux, the CI workflow runs the main test suite under `xvfb-run` and also runs Wayland-specific tests:

```bash
xvfb-run go test -tags ci -covermode=atomic -coverprofile=coverage.out ./...
go test -tags no_glfw,ci,wayland ./...
```

## Static Analysis

The static analysis workflow runs:

```bash
go vet ./...
gofumpt -d -e .
goimports -e -d .
gocyclo -over 30 .
staticcheck ./...
```

If formatting tools report changes, apply them before pushing.

## Pull Requests

Please keep pull requests focused and include:

- what changed
- why the change is needed
- which tests and static checks you ran
- whether the change affects public APIs, desktop behavior, window manager behavior, or platform-specific code

The pull request template asks contributors to include tests, run lint and formatting checks, and confirm that tests pass. If any item does not apply, explain why in the pull request.
