// Package service runs mbridge at login (SP8 §5.7): a launchd user agent
// on macOS, a systemd user unit on Linux, a logon-triggered scheduled task
// on Windows. Each runs `mbridge serve`.
package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/weiping/magpie-bridge/internal/fsutil"
	"github.com/weiping/magpie-bridge/internal/runcmd"
)

// Label is the launchd label; Name the systemd unit's and the scheduled
// task's name.
const (
	Label = "io.github.weiping.magpie-bridge"
	Name  = "mbridge"
)

// Manager installs the unit for one platform; tests set its fields.
type Manager struct {
	GOOS      string
	Home      string
	ConfigDir string
	UID       int
	User      string // Windows: DOMAIN\user, whose logon starts the task
	Run       func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// New is the Manager for this computer.
func New() *Manager {
	home, _ := os.UserHomeDir()
	m := &Manager{GOOS: runtime.GOOS, Home: home, ConfigDir: fsutil.ConfigDir(), UID: os.Getuid(), Run: run}
	if u, err := user.Current(); err == nil {
		m.User = u.Username
	}
	return m
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return runcmd.CommandContext(ctx, name, args...).CombinedOutput()
}

// Unit is the file Install writes for goos, and where.
func Unit(goos, home, configDir, exe string) (path string, content []byte) {
	return UnitFor(goos, home, configDir, exe, "")
}

// UnitFor is Unit for a user: on Windows the task is that user's (its
// logon starts it, it runs as them).
func UnitFor(goos, home, configDir, exe, user string) (path string, content []byte) {
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", Label+".plist"), []byte(fmt.Sprintf(plist, Label, xmlEscape(exe)))
	case "windows":
		uid := ""
		if user != "" {
			uid = "\n      <UserId>" + xmlEscape(user) + "</UserId>"
		}
		return filepath.Join(configDir, Name+"-task.xml"), []byte(fmt.Sprintf(taskXML, uid, uid, xmlEscape(exe)))
	default:
		return filepath.Join(home, ".config", "systemd", "user", Name+".service"), []byte(fmt.Sprintf(systemdUnit, exe))
	}
}

const plist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>serve</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ProcessType</key>
	<string>Background</string>
</dict>
</plist>
`

const systemdUnit = `[Unit]
Description=Magpie Bridge, the model router beside magpie
After=network.target

[Service]
ExecStart=%s serve
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
`

const taskXML = `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Magpie Bridge, the model router beside magpie</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>%s
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">%s
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>999</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>serve --detach</Arguments>
    </Exec>
  </Actions>
</Task>
`

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// Install writes the unit and starts it; installing again replaces it.
func (m *Manager) Install(ctx context.Context, exe string) error {
	path, content := UnitFor(m.GOOS, m.Home, m.ConfigDir, exe, m.User)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if m.GOOS == "windows" {
		content = utf16LE(content) // schtasks reads the XML as UTF-16
	}
	if err := fsutil.WriteAtomic(path, content); err != nil {
		return err
	}
	switch m.GOOS {
	case "darwin":
		dom := "gui/" + strconv.Itoa(m.UID)
		m.Run(ctx, "launchctl", "bootout", dom+"/"+Label) // not loaded yet is fine
		return m.must(ctx, "launchctl", "bootstrap", dom, path)
	case "windows":
		if err := m.must(ctx, "schtasks", "/Create", "/TN", Name, "/XML", path, "/F"); err != nil {
			return err
		}
	default:
		if err := m.must(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		if err := m.must(ctx, "systemctl", "--user", "enable", Name+".service"); err != nil {
			return err
		}
	}
	// installing again (a new binary, a new unit) replaces what runs
	return m.Restart(ctx)
}

// Restart starts mbridge again, the binary now at the unit's path; not
// installed is nothing to do. mbridge update calls it.
func (m *Manager) Restart(ctx context.Context) error {
	if path, _ := Unit(m.GOOS, m.Home, m.ConfigDir, ""); !exists(path) {
		return nil
	}
	switch m.GOOS {
	case "darwin":
		return m.must(ctx, "launchctl", "kickstart", "-k", "gui/"+strconv.Itoa(m.UID)+"/"+Label)
	case "windows":
		m.Run(ctx, "schtasks", "/End", "/TN", Name) // not running is fine
		return m.must(ctx, "schtasks", "/Run", "/TN", Name)
	default:
		return m.must(ctx, "systemctl", "--user", "restart", Name+".service")
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// Uninstall stops the service and removes its unit. Nothing installed is
// not an error.
func (m *Manager) Uninstall(ctx context.Context) error {
	path, _ := Unit(m.GOOS, m.Home, m.ConfigDir, "")
	switch m.GOOS {
	case "darwin":
		m.Run(ctx, "launchctl", "bootout", "gui/"+strconv.Itoa(m.UID)+"/"+Label)
	case "windows":
		m.Run(ctx, "schtasks", "/End", "/TN", Name)
		m.Run(ctx, "schtasks", "/Delete", "/TN", Name, "/F")
	default:
		m.Run(ctx, "systemctl", "--user", "disable", "--now", Name+".service")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if m.GOOS == "linux" {
		m.Run(ctx, "systemctl", "--user", "daemon-reload")
	}
	return nil
}

// Status says whether the service manager has mbridge running, with its
// own words for it.
func (m *Manager) Status(ctx context.Context) (running bool, detail string, err error) {
	path, _ := Unit(m.GOOS, m.Home, m.ConfigDir, "")
	if _, err := os.Stat(path); err != nil {
		return false, "not installed (mbridge service install)", nil
	}
	var out []byte
	switch m.GOOS {
	case "darwin":
		out, err = m.Run(ctx, "launchctl", "print", "gui/"+strconv.Itoa(m.UID)+"/"+Label)
		return err == nil && strings.Contains(string(out), "state = running"), firstLine(out, "state ="), nil
	case "windows":
		// State is an enum name, the same on every Windows language
		out, err = m.Run(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", "(Get-ScheduledTask -TaskName "+Name+").State")
		s := strings.TrimSpace(string(out))
		return err == nil && s == "Running", s, nil
	default:
		out, err = m.Run(ctx, "systemctl", "--user", "is-active", Name+".service")
		s := strings.TrimSpace(string(out))
		return s == "active", s, nil
	}
}

func (m *Manager) must(ctx context.Context, name string, args ...string) error {
	out, err := m.Run(ctx, name, args...)
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func firstLine(out []byte, prefix string) string {
	for _, l := range strings.Split(string(out), "\n") {
		if strings.Contains(l, prefix) {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

func utf16LE(b []byte) []byte {
	u := utf16.Encode([]rune(string(b)))
	out := []byte{0xFF, 0xFE}
	for _, c := range u {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}
