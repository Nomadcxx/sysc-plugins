package updates

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func modulesFS(dirs map[string]string, log string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for dir, base := range dirs {
		if base != "" {
			fsys["usr/lib/modules/"+dir+"/pkgbase"] = &fstest.MapFile{Data: []byte(base + "\n")}
		} else {
			fsys["usr/lib/modules/"+dir] = &fstest.MapFile{Mode: 0o755}
		}
	}
	if log != "" {
		fsys["var/log/pacman.log"] = &fstest.MapFile{Data: []byte(log)}
	}
	return fsys
}

func TestRebootNotNeededWhenRunningKernelIsInstalled(t *testing.T) {
	fsys := modulesFS(map[string]string{"6.16.9-arch1-1": "linux"}, "")
	needed, detail := RebootNeeded(fsys, "6.16.9-arch1-1", time.Time{})
	if needed {
		t.Fatalf("unexpected reboot: %s", detail)
	}
}

func TestRebootWhenRunningKernelHasNoModuleTree(t *testing.T) {
	fsys := modulesFS(map[string]string{"6.17.2.arch1-1": "linux"}, "")
	needed, detail := RebootNeeded(fsys, "6.16.9-arch1-1", time.Time{})
	if !needed {
		t.Fatal("want reboot when the running kernel's modules are gone")
	}
	if !strings.Contains(detail, "6.17.2.arch1-1 is installed") || !strings.Contains(detail, "6.16.9-arch1-1 is running") {
		t.Fatalf("detail = %q", detail)
	}
}

func TestRebootWhenNewerTreeSharesPkgbase(t *testing.T) {
	fsys := modulesFS(map[string]string{
		"6.16.9-arch1-1": "linux",
		"6.17.2.arch1-1": "linux",
	}, "")
	needed, detail := RebootNeeded(fsys, "6.16.9-arch1-1", time.Time{})
	if !needed {
		t.Fatal("want reboot when a newer linux tree is on disk")
	}
	if !strings.Contains(detail, "linux 6.17.2.arch1-1 is installed") {
		t.Fatalf("detail = %q", detail)
	}
}

func TestRebootNotNeededForUnrelatedKernelPackage(t *testing.T) {
	fsys := modulesFS(map[string]string{
		"6.16.9-arch1-1": "linux",
		"6.16.9-zen1-1":  "linux-zen",
	}, "")
	needed, detail := RebootNeeded(fsys, "6.16.9-arch1-1", time.Time{})
	if needed {
		t.Fatalf("unexpected reboot for another kernel package: %s", detail)
	}
}

func TestRebootFromPacmanLogAfterBoot(t *testing.T) {
	boot := time.Date(2026, 10, 8, 2, 0, 0, 0, time.UTC)
	fsys := modulesFS(map[string]string{"6.16.9-arch1-1": "linux"},
		"[2026-10-08T03:00:00+0000] [ALPM] upgraded linux (6.16.9-arch1-1 -> 6.17.1-arch1-1)\n")
	needed, detail := RebootNeeded(fsys, "6.16.9-arch1-1", boot)
	if !needed {
		t.Fatal("want reboot after a core upgrade that happened mid-session")
	}
	if !strings.Contains(detail, "linux was upgraded after boot") {
		t.Fatalf("detail = %q", detail)
	}
}

func TestRebootLogRuleSkipsPreBootUpgrades(t *testing.T) {
	boot := time.Date(2026, 10, 8, 2, 0, 0, 0, time.UTC)
	fsys := modulesFS(map[string]string{"6.16.9-arch1-1": "linux"},
		"[2026-10-07T23:00:00+0000] [ALPM] upgraded linux (6.16.8-arch1-1 -> 6.16.9-arch1-1)\n")
	needed, _ := RebootNeeded(fsys, "6.16.9-arch1-1", boot)
	if needed {
		t.Fatal("an upgrade before boot that the running system already loaded is not a reboot case")
	}
}

func TestRebootLogRuleIgnoresNonCorePackages(t *testing.T) {
	boot := time.Date(2026, 10, 8, 2, 0, 0, 0, time.UTC)
	fsys := modulesFS(map[string]string{"6.16.9-arch1-1": "linux"},
		"[2026-10-08T03:00:00+0000] [ALPM] upgraded firefox (143.0.1-1 -> 143.0.2-1)\n")
	needed, _ := RebootNeeded(fsys, "6.16.9-arch1-1", boot)
	if needed {
		t.Fatal("firefox does not need a reboot")
	}
}

func TestRebootLogRuleDisabledWithZeroBootTime(t *testing.T) {
	fsys := modulesFS(map[string]string{"6.16.9-arch1-1": "linux"},
		"[2026-10-08T03:00:00+0000] [ALPM] upgraded linux (6.16.9-arch1-1 -> 6.17.1-arch1-1)\n")
	needed, _ := RebootNeeded(fsys, "6.16.9-arch1-1", time.Time{})
	if needed {
		t.Fatal("log rule must stay off without a boot time")
	}
}
