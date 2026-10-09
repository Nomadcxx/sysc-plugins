package minidocker

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/mini-docker/screenshot.png when CAPTURE=1:
// the containers tab with five invented containers, four of them running.
func TestCapturePanel(t *testing.T) {
	capture.Panel(t, "mini-docker", PanelTreeForSession(SessionSnapshot{
		Scope:        ScopeContainers,
		SelectedID:   "c3f1a9b2c4d5",
		ContainerTab: TabStatus{Available: true, RefreshedAt: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)},
		Containers: []Container{
			{ID: "a1b2c3d4e5f6", Names: "web-frontend", Image: "acme/frontend:2.4", State: "running", Status: "Up 3 hours"},
			{ID: "b2c3d4e5f6a1", Names: "api-gateway", Image: "acme/gateway:1.9", State: "running", Status: "Up 3 hours"},
			{ID: "c3f1a9b2c4d5", Names: "postgres-db", Image: "postgres:16", State: "running", Status: "Up 2 days"},
			{ID: "d4e5f6a1b2c3", Names: "redis-cache", Image: "redis:7", State: "running", Status: "Up 2 days"},
			{ID: "e5f6a1b2c3d4", Names: "nightly-backup", Image: "acme/backup:0.8", State: "exited", Status: "Exited (0) 9 hours ago"},
		},
	}))
}
