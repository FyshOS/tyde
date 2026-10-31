# If PREFIX isn't provided, we check for /usr/local and use that if it exists.
# Otherwice we fall back to using /usr.

LOCAL != test -d $(DESTDIR)/usr/local && echo -n "/local" || echo -n ""
LOCAL ?= $(shell test -d $(DESTDIR)/usr/local && echo "/local" || echo "")
PREFIX ?= /usr$(LOCAL)

build:
	go build ./cmd/tyde_runner
	go build ./cmd/tyde_ctl
	go build -tags migrated_fynedo ./cmd/tyde

install:
	install -Dm00755 tyde_runner $(DESTDIR)$(PREFIX)/bin/tyde_runner
	install -Dm00755 tyde_ctl $(DESTDIR)$(PREFIX)/bin/tyde_ctl
	install -Dm00755 tyde $(DESTDIR)$(PREFIX)/bin/tyde
	install -Dm00644 theme/assets/icon.png $(DESTDIR)$(PREFIX)/share/pixmaps/com.fyshos.tyde.png
	install -Dm00644 tyde.desktop $(DESTDIR)$(PREFIX)/share/xsessions/tyde.desktop
	install -Dm00644 tyde-welcome.desktop $(DESTDIR)$(PREFIX)/share/applications/tyde-welcome.desktop
	install -Dm00644 tyde-fathom.desktop $(DESTDIR)$(PREFIX)/share/applications/tyde-fathom.desktop

uninstall:
	-rm $(DESTDIR)$(PREFIX)/bin/tyde_runner
	-rm $(DESTDIR)$(PREFIX)/bin/tyde_ctl
	-rm $(DESTDIR)$(PREFIX)/bin/tyde
	-rm $(DESTDIR)$(PREFIX)/share/xsessions/tyde.desktop
	-rm $(DESTDIR)$(PREFIX)/share/applications/tyde-welcome.desktop
	-rm $(DESTDIR)$(PREFIX)/share/applications/tyde-fathom.desktop

embed:
	Xephyr :5 -screen 1280x720 &
	DISPLAY=:5 go run -tags migrated_fynedo ./cmd/tyde

# === FyshOS packaging =========================================================
DEB_VERSION     ?=
DEB_NAME        ?= tyde
DEB_SECTION     ?= x11
DEB_DESCRIPTION ?= FyshOS desktop environment
DEB_HOMEPAGE    ?= https://fyshos.com
DEB_SUDO        ?= -sudo
DEB_BUILD_DEPS  ?= libgl1-mesa-dev xorg-dev libpam0g-dev libwayland-dev \
                   libxkbcommon-dev libglib2.0-dev libgbm-dev

repo:
	fyshpkg make \
		-name "$(DEB_NAME)" \
		$(if $(DEB_VERSION),-version "$(DEB_VERSION)") \
		-section "$(DEB_SECTION)" \
		-description "$(DEB_DESCRIPTION)" \
		-homepage "$(DEB_HOMEPAGE)" \
		-build-deps "$(DEB_BUILD_DEPS)" \
		$(DEB_SUDO) $(FYSHPKG_FLAGS) \
		.

.PHONY: build install uninstall embed repo
