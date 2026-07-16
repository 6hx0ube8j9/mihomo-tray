package fsm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const ConfigFileName = "mihomo-tray.json"

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

	if m.data.Proxy == "" { m.data.Proxy = "false" }
	if m.data.Tun == "" { m.data.Tun = "false" }
	if m.data.Autostart == "" { m.data.Autostart = "false" }
	if m.data.Mode == "" { m.data.Mode = "rule" }
	if m.data.Port == "" { m.data.Port = "7890" }
	if m.data.TunDevice == "" { m.data.TunDevice = "Meta" }
	if m.data.ExternalController == "" { m.data.ExternalController = "127.0.0.1:9090" }
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
			case "tun_device": value = "Meta"
			case "mode": value = "rule"
			case "port": value = "7890"
			case "external-controller": value = "127.0.0.1:9090"
			}
		}

		switch key {
		case "autostart": if m.data.Autostart != value { m.data.Autostart = value; changed = true }
		case "external-controller": if m.data.ExternalController != value { m.data.ExternalController = value; changed = true }
		case "mode": if m.data.Mode != value { m.data.Mode = value; changed = true }
		case "port": if m.data.Port != value { m.data.Port = value; changed = true }
		case "proxy": if m.data.Proxy != value { m.data.Proxy = value; changed = true }
		case "secret": if m.data.Secret != value { m.data.Secret = value; changed = true }
		case "tun": if m.data.Tun != value { m.data.Tun = value; changed = true }
		case "tun_device": if m.data.TunDevice != value { m.data.TunDevice = value; changed = true }
		}
	}

	if changed {
		m.lockedSave()
	}
}

func (m *Manager) SyncWithYAML() {
	m.yamlMu.Lock()

	configPath := filepath.Join(m.baseDir, "config.yaml")
	content, err := os.ReadFile(configPath)

	var lines []string
	if err == nil && len(content) > 0 {
		text := strings.ReplaceAll(string(content), "\r\n", "\n")
		lines = strings.Split(text, "\n")
	}

	type Field struct {
		exists bool
		value  string
	}
	var (
		mixedPortF, socksPortF, portF, modeF Field
		extCtrlF, secretF, extUIF, extUIUrlF Field
		tunDevice                            Field
	)

	tunRootExists := false
	inTun := false

	cleanVal := func(s string) string { return strings.Trim(strings.TrimSpace(s), " \"'") }

	stripComment := func(s string) string {
		inSingle, inDouble := false, false
		for i, char := range s {
			if char == '\'' && !inDouble {
				inSingle = !inSingle
			} else if char == '"' && !inSingle {
				inDouble = !inDouble
			} else if char == '#' && !inSingle && !inDouble {
				return s[:i]
			}
		}
		return s
	}

	getIndent := func(s string) int {
		for i, c := range s {
			if c != ' ' && c != '\t' {
				return i
			}
		}
		return len(s)
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		raw := stripComment(line)
		if strings.TrimSpace(raw) == "" {
			continue
		}

		indent := getIndent(raw)
		trimmed := strings.TrimSpace(raw)

		if indent == 0 {
			inTun = false
			
			if strings.HasPrefix(trimmed, "mixed-port:") {
				mixedPortF.exists = true
				if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 { mixedPortF.value = cleanVal(parts[1]) }
			} else if strings.HasPrefix(trimmed, "socks-port:") {
				socksPortF.exists = true
			} else if strings.HasPrefix(trimmed, "port:") {
				portF.exists = true
				if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 { portF.value = cleanVal(parts[1]) }
			} else if strings.HasPrefix(trimmed, "mode:") {
				modeF.exists = true
				if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 { modeF.value = cleanVal(parts[1]) }
			} else if strings.HasPrefix(trimmed, "external-controller:") {
				extCtrlF.exists = true
				if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 { extCtrlF.value = cleanVal(parts[1]) }
			} else if strings.HasPrefix(trimmed, "secret:") {
				secretF.exists = true
				if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 { secretF.value = cleanVal(parts[1]) }
			} else if strings.HasPrefix(trimmed, "external-ui:") {
				extUIF.exists = true
			} else if strings.HasPrefix(trimmed, "external-ui-url:") {
				extUIUrlF.exists = true
			} else if strings.HasPrefix(trimmed, "tun:") {
				tunRootExists = true
				inTun = true
			}
		} else if inTun && indent > 0 {
			if strings.HasPrefix(trimmed, "device:") {
				tunDevice.exists = true
				if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 { tunDevice.value = cleanVal(parts[1]) }
			}
		}
	}

	var prependLines []string
	changed := false
	extracted := make(map[string]string)

	if !mixedPortF.exists { 
		prependLines = append(prependLines, "mixed-port: 7890"); changed = true; extracted["port"] = "7890"
	} else { extracted["port"] = mixedPortF.value }
	
	if !socksPortF.exists { prependLines = append(prependLines, "socks-port: 7891"); changed = true }
	if !portF.exists && !mixedPortF.exists { prependLines = append(prependLines, "port: 7892"); changed = true }
	
	if !modeF.exists { 
		prependLines = append(prependLines, "mode: rule"); changed = true; extracted["mode"] = "rule"
	} else { extracted["mode"] = modeF.value }
	
	if !extCtrlF.exists { 
		prependLines = append(prependLines, "external-controller: 127.0.0.1:9090"); changed = true; extracted["external-controller"] = "127.0.0.1:9090"
	} else { extracted["external-controller"] = extCtrlF.value }
	
	if !secretF.exists { 
		prependLines = append(prependLines, "secret: ''"); changed = true; extracted["secret"] = ""
	} else { extracted["secret"] = secretF.value }
	
	if !extUIF.exists { prependLines = append(prependLines, "external-ui: 'ui'"); changed = true }
	if !extUIUrlF.exists { prependLines = append(prependLines, "external-ui-url: ''"); changed = true }
	
	if !tunRootExists {
		prependLines = append(prependLines, "tun:")
		prependLines = append(prependLines, "  enable: false")
		prependLines = append(prependLines, "  stack: mixed")
		prependLines = append(prependLines, "  auto-route: true")
		prependLines = append(prependLines, "  device: Meta")
		changed = true
		extracted["tun_device"] = "Meta"
	} else if tunDevice.exists {
		extracted["tun_device"] = tunDevice.value
	} else {
		extracted["tun_device"] = ""
	}

	if changed {
		var finalLines []string
		finalLines = append(finalLines, prependLines...)
		finalLines = append(finalLines, lines...)

		output := strings.Join(finalLines, "\n")
		if len(output) > 0 && !strings.HasSuffix(output, "\n") {
			output += "\n"
		}

		tmpFile, err := os.CreateTemp(m.baseDir, "config.yaml.*.tmp")
		if err == nil {
			tmpName := tmpFile.Name()
			writeSuccess := false
			
			func() {
				defer func() {
					_ = tmpFile.Close()
					if !writeSuccess {
						_ = os.Remove(tmpName) 
					}
				}()
				if _, err := tmpFile.Write([]byte(output)); err != nil { return }
				if err := tmpFile.Sync(); err != nil { return }
				writeSuccess = true
			}()

			if writeSuccess {
				_ = os.Rename(tmpName, configPath)
			}
		}
	}
	
	m.yamlMu.Unlock() 
	m.UpdateBatch(extracted)
}

func (m *Manager) lockedSave() {
	b, err := json.MarshalIndent(m.data, "", "  ")
	if err != nil {
		return
	}

	cfgPath := filepath.Join(m.baseDir, ConfigFileName)
	tmpFile, err := os.CreateTemp(m.baseDir, ConfigFileName+".*.tmp")
	if err != nil {
		return
	}
	tmpName := tmpFile.Name()
	writeSuccess := false

	func() {
		defer func() {
			_ = tmpFile.Close()
			if !writeSuccess {
				_ = os.Remove(tmpName)
			}
		}()
		if _, err := tmpFile.Write(b); err != nil { return }
		if err := tmpFile.Sync(); err != nil { return }
		writeSuccess = true
	}()

	if writeSuccess {
		_ = os.Rename(tmpName, cfgPath)
	}
}

func (m *Manager) BaseDir() string { return m.baseDir }
func (m *Manager) ExePath() string { return m.exePath }
