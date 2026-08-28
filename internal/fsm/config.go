package fsm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	ConfigFileName = "mihomo-tray.json"

	DefaultAutostart          = "false"
	DefaultProxy              = "false"
	DefaultTun                = "false"
	DefaultMode               = "rule"
	DefaultMixedPort          = "7890"
	DefaultSocksPort          = "7891"
	DefaultHTTPPort           = "7892"
	DefaultExternalController = "127.0.0.1:9090"
	DefaultSecret             = ""
	DefaultTunDevice          = "Meta"

	DefaultExternalUI    = "ui"
	DefaultExternalUIURL = ""
	DefaultTunStack      = "mixed"
	DefaultTunAutoRoute  = true
)

type TrayConfig struct {
	Autostart          string `json:"autostart"`
	ExternalController string `json:"external-controller"`
	Mode               string `json:"mode"`
	Port               string `json:"port"`
	Proxy              string `json:"proxy"`
	Secret             string `json:"secret"`
	Tun                string `json:"tun"`
	TunDevice          string `json:"tun_device"`
}

type Manager struct {
	baseDir string
	exePath string
	mu      sync.RWMutex
	yamlMu  sync.Mutex
	data    TrayConfig

	State *RuntimeState
}

func NewManager(baseDir, exePath string) *Manager {
	return &Manager{
		baseDir: baseDir,
		exePath: exePath,
		State:   NewRuntimeState(),
	}
}

func (m *Manager) EnsureDefault() {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfgPath := filepath.Join(m.baseDir, ConfigFileName)

	if f, err := os.Open(cfgPath); err == nil {
		_ = json.NewDecoder(f).Decode(&m.data)
		_ = f.Close()
	}

	if m.data.Proxy == "" {
		m.data.Proxy = DefaultProxy
	}
	if m.data.Tun == "" {
		m.data.Tun = DefaultTun
	}
	if m.data.Autostart == "" {
		m.data.Autostart = DefaultAutostart
	}
	if m.data.Mode == "" {
		m.data.Mode = DefaultMode
	}
	if m.data.Port == "" {
		m.data.Port = DefaultMixedPort
	}
	if m.data.TunDevice == "" {
		m.data.TunDevice = DefaultTunDevice
	}
	if m.data.ExternalController == "" {
		m.data.ExternalController = DefaultExternalController
	}
	if m.data.Secret == "" {
		m.data.Secret = DefaultSecret
	}

	m.lockedSave()
}

func (m *Manager) Get(key string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	switch key {
	case "autostart": return m.data.Autostart
	case "external-controller": return m.data.ExternalController
	case "mode": return m.data.Mode
	case "port": return m.data.Port
	case "proxy": return m.data.Proxy
	case "secret": return m.data.Secret
	case "tun": return m.data.Tun
	case "tun_device": return m.data.TunDevice
	default: return ""
	}
}

func (m *Manager) Set(key, value string) {
	m.UpdateBatch(map[string]string{key: value})
}

func (m *Manager) UpdateBatch(updates map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	changed := false
	for key, value := range updates {
		if value == "" {
			switch key {
			case "tun_device":
				value = DefaultTunDevice
			case "mode":
				value = DefaultMode
			case "port":
				value = DefaultMixedPort
			case "external-controller":
				value = DefaultExternalController
			}
		}

		switch key {
		case "autostart":
			if m.data.Autostart != value {
				m.data.Autostart = value
				changed = true
			}
		case "external-controller":
			if m.data.ExternalController != value {
				m.data.ExternalController = value
				changed = true
			}
		case "mode":
			if m.data.Mode != value {
				m.data.Mode = value
				changed = true
			}
		case "port":
			if m.data.Port != value {
				m.data.Port = value
				changed = true
			}
		case "proxy":
			if m.data.Proxy != value {
				m.data.Proxy = value
				changed = true
			}
		case "secret":
			if m.data.Secret != value {
				m.data.Secret = value
				changed = true
			}
		case "tun":
			if m.data.Tun != value {
				m.data.Tun = value
				changed = true
			}
		case "tun_device":
			if m.data.TunDevice != value {
				m.data.TunDevice = value
				changed = true
			}
		}
	}

	if changed {
		m.lockedSave()
	}
}

func (m *Manager) PrepareYAMLForBoot(wantMode string, wantTun bool) (bool, error) {
	m.yamlMu.Lock()
	defer m.yamlMu.Unlock()

	configPath := filepath.Join(m.baseDir, "config.yaml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		return false, err
	}

	rawStr := strings.TrimPrefix(string(content), "\xef\xbb\xbf")
	lines := strings.Split(strings.ReplaceAll(rawStr, "\r\n", "\n"), "\n")

	outLines, extracted, modified := processYAMLContent(lines, wantMode, wantTun)

	if modified {
		output := strings.Join(outLines, "\n")
		if len(output) > 0 && !strings.HasSuffix(output, "\n") {
			output += "\n"
		}

		if err := writeTmpAndRename(m.baseDir, configPath, []byte(output)); err != nil {
			return false, fmt.Errorf("failed to save config.yaml: %w", err)
		}
	}

	if len(extracted) > 0 {
		m.UpdateBatch(extracted)
	}

	return modified, nil
}

func processYAMLContent(lines []string, wantMode string, wantTun bool) ([]string, map[string]string, bool) {
	extracted := make(map[string]string)
	modified := false

	var (
		hasMixedPort bool
		mixedPortVal string
		hasSocksPort bool
		hasPort      bool
		portVal      string
		hasMode      bool
		hasExtCtrl   bool
		extCtrlVal   string
		hasSecret    bool
		secretVal    string
		hasExtUI     bool
		hasExtUIUrl  bool

		tunRootExists bool
		tunRootIndex  int = -1
		inTun         bool
		hasTunEnable  bool
		hasTunDevice  bool
		tunDeviceVal  string
	)

	outLines := make([]string, len(lines))
	copy(outLines, lines)

	for i, line := range outLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
			continue
		}

		indent := 0
		prefixLen := 0
		for _, c := range line {
			if c == ' ' {
				indent++
				prefixLen++
			} else if c == '\t' {
				indent += 4
				prefixLen++
			} else {
				break
			}
		}

		if indent == 0 {
			inTun = false

			if strings.HasPrefix(trimmed, "mixed-port:") {
				hasMixedPort = true
				if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 {
					mixedPortVal = cleanVal(parts[1])
				}
			} else if strings.HasPrefix(trimmed, "socks-port:") {
				hasSocksPort = true
			} else if strings.HasPrefix(trimmed, "port:") {
				hasPort = true
				if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 {
					portVal = cleanVal(parts[1])
				}
			} else if strings.HasPrefix(trimmed, "mode:") {
				hasMode = true
				if wantMode != "" {
					comment := extractComment(line)
					targetLine := fmt.Sprintf("%smode: %s%s", line[:prefixLen], wantMode, comment)
					if outLines[i] != targetLine {
						outLines[i] = targetLine
						modified = true
					}
				}
			} else if strings.HasPrefix(trimmed, "external-controller:") {
				hasExtCtrl = true
				if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 {
					extCtrlVal = cleanVal(parts[1])
				}
			} else if strings.HasPrefix(trimmed, "secret:") {
				hasSecret = true
				if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 {
					secretVal = cleanVal(parts[1])
				}
			} else if strings.HasPrefix(trimmed, "external-ui:") {
				hasExtUI = true
			} else if strings.HasPrefix(trimmed, "external-ui-url:") {
				hasExtUIUrl = true
			} else if strings.HasPrefix(trimmed, "tun:") {
				tunRootExists = true
				tunRootIndex = i
				inTun = true
			}
		} else if inTun && indent > 0 {
			if strings.HasPrefix(trimmed, "enable:") {
				hasTunEnable = true
				comment := extractComment(line)
				targetLine := fmt.Sprintf("%senable: %t%s", line[:prefixLen], wantTun, comment)
				if outLines[i] != targetLine {
					outLines[i] = targetLine
					modified = true
				}
			} else if strings.HasPrefix(trimmed, "device:") {
				hasTunDevice = true
				if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 {
					tunDeviceVal = cleanVal(parts[1])
				}
			}
		}
	}

	if tunRootExists && !hasTunEnable {
		enableLine := fmt.Sprintf("  enable: %t", wantTun)
		if tunRootIndex >= 0 && tunRootIndex < len(outLines) {
			outLines = append(outLines[:tunRootIndex+1], append([]string{enableLine}, outLines[tunRootIndex+1:]...)...)
			modified = true
		}
	}

	if hasMixedPort {
		extracted["port"] = mixedPortVal
	} else if hasPort {
		extracted["port"] = portVal
	}

	if hasExtCtrl {
		extracted["external-controller"] = extCtrlVal
	}

	if hasSecret {
		extracted["secret"] = secretVal
	}

	if tunRootExists {
		if hasTunDevice && tunDeviceVal != "" {
			extracted["tun_device"] = tunDeviceVal
		} else {
			extracted["tun_device"] = DefaultTunDevice
		}
	}

	var prependLines []string

	if !hasMixedPort {
		prependLines = append(prependLines, fmt.Sprintf("mixed-port: %s", DefaultMixedPort))
		modified = true
		if !hasPort {
			extracted["port"] = DefaultMixedPort
		}
	}
	if !hasSocksPort {
		prependLines = append(prependLines, fmt.Sprintf("socks-port: %s", DefaultSocksPort))
		modified = true
	}
	if !hasPort && !hasMixedPort {
		prependLines = append(prependLines, fmt.Sprintf("port: %s", DefaultHTTPPort))
		modified = true
	}

	if !hasMode {
		modeToSet := DefaultMode
		if wantMode != "" {
			modeToSet = wantMode
		}
		prependLines = append(prependLines, "mode: "+modeToSet)
		modified = true
	}

	if !hasExtCtrl {
		prependLines = append(prependLines, fmt.Sprintf("external-controller: %s", DefaultExternalController))
		modified = true
		extracted["external-controller"] = DefaultExternalController
	}

	if !hasSecret {
		prependLines = append(prependLines, fmt.Sprintf("secret: '%s'", DefaultSecret))
		modified = true
		extracted["secret"] = DefaultSecret
	}

	if !hasExtUI {
		prependLines = append(prependLines, fmt.Sprintf("external-ui: '%s'", DefaultExternalUI))
		modified = true
	}

	if !hasExtUIUrl {
		prependLines = append(prependLines, fmt.Sprintf("external-ui-url: '%s'", DefaultExternalUIURL))
		modified = true
	}

	if !tunRootExists {
		prependLines = append(prependLines, "tun:")
		prependLines = append(prependLines, fmt.Sprintf("  enable: %t", wantTun))
		prependLines = append(prependLines, fmt.Sprintf("  stack: %s", DefaultTunStack))
		prependLines = append(prependLines, fmt.Sprintf("  auto-route: %t", DefaultTunAutoRoute))
		prependLines = append(prependLines, fmt.Sprintf("  device: %s", DefaultTunDevice))
		modified = true
		extracted["tun_device"] = DefaultTunDevice
	}

	if len(prependLines) > 0 {
		outLines = append(prependLines, outLines...)
	}

	return outLines, extracted, modified
}

func (m *Manager) lockedSave() {
	b, err := json.MarshalIndent(m.data, "", "  ")
	if err != nil {
		return
	}
	cfgPath := filepath.Join(m.baseDir, ConfigFileName)
	_ = writeTmpAndRename(m.baseDir, cfgPath, b)
}

func (m *Manager) BaseDir() string { return m.baseDir }
func (m *Manager) ExePath() string { return m.exePath }

func extractComment(line string) string {
	if idx := strings.Index(line, "#"); idx != -1 {
		rawComment := line[idx:]
		if idx > 0 && line[idx-1] == ' ' {
			return " " + strings.TrimSpace(rawComment)
		}
		return " " + strings.TrimSpace(rawComment)
	}
	return ""
}

func cleanVal(s string) string {
	inSingle, inDouble := false, false
	for i, char := range s {
		if char == '\'' && !inDouble {
			inSingle = !inSingle
		} else if char == '"' && !inSingle {
			inDouble = !inDouble
		} else if char == '#' && !inSingle && !inDouble {
			s = s[:i]
			break
		}
	}
	return strings.Trim(strings.TrimSpace(s), " \"'")
}

func writeTmpAndRename(baseDir, targetPath string, content []byte) error {
	tmpFile, err := os.CreateTemp(baseDir, "config.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()

	cleaned := false
	defer func() {
		if !cleaned {
			_ = tmpFile.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmpFile.Write(content); err != nil {
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		return err
	}

	if err := tmpFile.Close(); err != nil {
		return err
	}
	cleaned = true

	return os.Rename(tmpName, targetPath)
}
