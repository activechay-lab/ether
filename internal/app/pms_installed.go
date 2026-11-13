package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var installedPMS = sync.OnceValue(pmsInstalled)

// Probe only at startup, never on a client request.
func pmsInstalled() bool {
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	run := func(name string, args ...string) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, name, args...).Run() == nil
	}
	switch runtime.GOOS {
	case "windows":
		for _, root := range []string{"HKCU", "HKLM"} {
			if run("reg.exe", "query", root+"\\Software\\Plex, Inc.\\Plex Media Server") {
				return true
			}
		}
		// The service runs as SYSTEM: also inspect loaded user registry hives.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		out, _ := exec.CommandContext(ctx, "reg.exe", "query", "HKU").Output()
		for _, hive := range strings.Fields(string(out)) {
			if strings.HasPrefix(hive, "HKEY_USERS\\") && run("reg.exe", "query", hive+"\\Software\\Plex, Inc.\\Plex Media Server") {
				return true
			}
		}
		for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
			if root != "" && exists(filepath.Join(root, "Plex", "Plex Media Server", "Plex Media Server.exe")) {
				return true
			}
		}
		return run("sc.exe", "query", "Plex Media Server")
	case "darwin":
		return exists("/Applications/Plex Media Server.app")
	case "linux":
		for _, path := range []string{"/usr/lib/plexmediaserver", "/etc/systemd/system/plexmediaserver.service", "/lib/systemd/system/plexmediaserver.service", "/usr/lib/systemd/system/plexmediaserver.service", "/etc/init.d/plexmediaserver"} {
			if exists(path) {
				return true
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		status, _ := exec.CommandContext(ctx, "dpkg-query", "-W", "-f=${Status}", "plexmediaserver").Output()
		return strings.TrimSpace(string(status)) == "install ok installed" || run("rpm", "-q", "plexmediaserver")
	}
	return false
}

func reservePMSPort(serverPort int, explicit, env bool, probe func() bool) bool {
	return serverPort == 32400 && !explicit && !env && probe()
}
