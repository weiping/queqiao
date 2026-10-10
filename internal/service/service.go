// Package service runs queqiaod at login (SP8 §5.7): a launchd user agent
// on macOS, a systemd user unit on Linux, a logon-triggered scheduled task
// on Windows. Each runs `queqiao serve`.
package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/weiping/queqiao/internal/fsutil"
	"github.com/weiping/queqiao/internal/runcmd"
)

// Label is the launchd label; Name the systemd unit's and the scheduled
// task's name.
const (
	Label = "io.github.weiping.queqiao"
	Name  = "queqiao"
)

// Manager installs the unit for one platform; tests set its fields.
type Manager struct {
	GOOS      string
	Home      string
	ConfigDir string
	UID       int
	Run       func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// New is the Manager for this computer.
func New() *Manager {
	home, _ := os.UserHomeDir()
	return &Manager{GOOS: runtime.GOOS, Home: home, ConfigDir: fsutil.ConfigDir(), UID: os.Getuid(), Run: run}
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return runcmd.CommandContext(ctx, name, args...).CombinedOutput()
}

// Unit is the file Install writes for goos, and where.
func Unit(goos, home, configDir, exe string) (path string, content []byte) {
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", Label+".plist"), []byte(fmt.Sprintf(plist, Label, xmlEscape(exe)))
	case "windows":
		return filepath.Join(configDir, Name+"-task.xml"), []byte(fmt.Sprintf(taskXML, xmlEscape(exe)))
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
Description=queqiaod, the queqiao model router beside magpie
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
    <Description>queqiaod, the queqiao model router beside magpie</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
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
      <Arguments>serve</Arguments>
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
	path, content := Unit(m.GOOS, m.Home, m.ConfigDir, exe)
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
		return m.must(ctx, "schtasks", "/Run", "/TN", Name)
	default:
		if err := m.must(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		return m.must(ctx, "systemctl", "--user", "enable", "--now", Name+".service")
	}
}

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

// Status says whether the service manager has queqiaod running, with its
// own words for it.
func (m *Manager) Status(ctx context.Context) (running bool, detail string, err error) {
	path, _ := Unit(m.GOOS, m.Home, m.ConfigDir, "")
	if _, err := os.Stat(path); err != nil {
		return false, "not installed (queqiao service install)", nil
	}
	var out []byte
	switch m.GOOS {
	case "darwin":
		out, err = m.Run(ctx, "launchctl", "print", "gui/"+strconv.Itoa(m.UID)+"/"+Label)
		return err == nil && strings.Contains(string(out), "state = running"), firstLine(out, "state ="), nil
	case "windows":
		out, err = m.Run(ctx, "schtasks", "/Query", "/TN", Name, "/FO", "LIST")
		return err == nil && strings.Contains(string(out), "Running"), firstLine(out, "Status"), nil
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
