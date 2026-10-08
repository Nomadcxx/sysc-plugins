PLUGINS := \
	sysc-plugin-screen-recorder:screen-recorder \
	sysc-plugin-screenshot:screenshot \
	sysc-plugin-notes:notes \
	sysc-plugin-timer:timer \
	sysc-plugin-world-clock:world-clock \
	sysc-plugin-calendar:calendar \
	sysc-plugin-github-notifications:github-notifications \
	sysc-plugin-mini-docker:mini-docker \
	sysc-plugin-wallpaper-depth:wallpaper-depth \
	sysc-plugin-aiusage:aiusage \
	sysc-plugin-kdeconnect:kdeconnect \
	sysc-plugin-faith:faith \
	sysc-plugin-cat:cat \
	sysc-plugin-games:games \
	sysc-plugin-protonvpn:protonvpn \
	sysc-plugin-moonbit:moonbit \
	sysc-plugin-updates:updates

# Calendar is the only cgo plugin; skip it when its pkg-config modules are
# missing so one optional plugin's headers do not block the other 15.
PKG_CONFIG ?= pkg-config
CALENDAR_PC := libecal-2.0 json-glib-1.0
ifeq ($(shell $(PKG_CONFIG) --exists $(CALENDAR_PC) 2>/dev/null && echo yes),)
  ifeq ($(STRICT),)
    $(info note: skipping calendar; it needs $(CALENDAR_PC) via pkg-config (evolution-data-server headers))
    PLUGINS := $(filter-out sysc-plugin-calendar:calendar,$(PLUGINS))
  endif
endif
USER_PLUGIN_ROOT := $(or $(XDG_CONFIG_HOME),$(HOME)/.config)/sysc-shell/plugins

.PHONY: build install link test vet fmt validate catalog-validate clean

build:
	@set -e; for entry in $(PLUGINS); do \
		cmd=$${entry%%:*}; dir=$${entry##*:}; \
		echo "go build -o plugins/$$dir/bin/$$cmd ./cmd/$$cmd"; \
		go build -trimpath -o "plugins/$$dir/bin/$$cmd" "./cmd/$$cmd"; \
	done

install: build link

link:
	@mkdir -p "$(USER_PLUGIN_ROOT)"
	@set -e; for entry in $(PLUGINS); do \
		dir=$${entry##*:}; \
		id=$$(sed -n 's/.*"id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "plugins/$$dir/manifest.json" | head -1); \
		[ -n "$$id" ] || { echo "install: no id in plugins/$$dir/manifest.json" >&2; exit 1; }; \
		dest="$(USER_PLUGIN_ROOT)/$$id"; \
		if [ -e "$$dest" ] && [ ! -L "$$dest" ]; then \
			echo "install: $$dest is a real directory (catalog install); rm -rf it to link the checkout" >&2; exit 1; \
		fi; \
		ln -sfn "$$PWD/plugins/$$dir" "$$dest"; \
		if [ -L "$(USER_PLUGIN_ROOT)/$$dir" ]; then rm -f "$(USER_PLUGIN_ROOT)/$$dir"; fi; \
	done
	@echo "Linked $(words $(PLUGINS)) plugins into $(USER_PLUGIN_ROOT)"

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

validate:
	go run ./tools/validate-manifests

catalog-validate:
	go run ./tools/catalog validate

clean:
	rm -rf plugins/*/bin
