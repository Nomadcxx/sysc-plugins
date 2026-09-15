PLUGINS := \
	sysc-plugin-screen-recorder:screen-recorder \
	sysc-plugin-notes:notes \
	sysc-plugin-timer:timer \
	sysc-plugin-world-clock:world-clock \
	sysc-plugin-calendar:calendar \
	sysc-plugin-github-notifications:github-notifications \
	sysc-plugin-mini-docker:mini-docker \
	sysc-plugin-wallpaper-depth:wallpaper-depth

USER_PLUGIN_ROOT := $(or $(XDG_CONFIG_HOME),$(HOME)/.config)/sysc-shell/plugins

.PHONY: build install test vet fmt validate clean

build:
	@set -e; for entry in $(PLUGINS); do \
		cmd=$${entry%%:*}; dir=$${entry##*:}; \
		echo "go build -o plugins/$$dir/bin/$$cmd ./cmd/$$cmd"; \
		go build -trimpath -o "plugins/$$dir/bin/$$cmd" "./cmd/$$cmd"; \
	done

install: build
	@mkdir -p "$(USER_PLUGIN_ROOT)"
	@set -e; for entry in $(PLUGINS); do \
		dir=$${entry##*:}; \
		ln -sfn "$$PWD/plugins/$$dir" "$(USER_PLUGIN_ROOT)/$$dir"; \
	done
	@echo "Installed $(words $(PLUGINS)) plugins into $(USER_PLUGIN_ROOT)"

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

validate:
	go run ./tools/validate-manifests

clean:
	rm -rf plugins/*/bin
