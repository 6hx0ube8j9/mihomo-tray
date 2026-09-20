package core

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mihomo-tray/internal/domain"
)

const (
	yamlHeader = "# Auto-generated launch configuration. DO NOT EDIT.\n\n"

	keyMixedPort  = "mixed-port:"
	keyPort       = "port:"
	keyMode       = "mode:"
	keyExtCtrl    = "external-controller:"
	keySecret     = "secret:"
	keyExtUI      = "external-ui:"
	keyExtUIName  = "external-ui-name:"
	keyExtUIUrl   = "external-ui-url:"
	keyTun        = "tun:"
	keyDevice     = "device:"
	keyEnable     = "enable:"

	patTunDevice = `device:\s*([^,}]+)`
	patTunEnable = `(?i)(enable:\s*)(true|false)`
)

type BuilderParams struct {
	Mode    string
	Tun     bool
	BaseDir string
	RelPath string
}

func BuildRuntimeYAML(params BuilderParams) (bool, map[string]string, error) {
	var content []byte
	var err error

	if params.RelPath != "" {
		sourcePath := filepath.Join(params.BaseDir, filepath.FromSlash(params.RelPath))
		content, err = os.ReadFile(sourcePath)
		if err != nil {
			return false, nil, fmt.Errorf("底稿文件已丢失，无法构建运行时配置: %s", sourcePath)
		}
	} else {
		slog.Info("当前无活跃配置，将使用保底参数启动内核")
		content = []byte("")
	}
	
	rawStr := strings.TrimPrefix(string(content), "\xef\xbb\xbf")
	lines := strings.Split(strings.ReplaceAll(rawStr, "\r\n", "\n"), "\n")

	outLines, extracted := processYAMLContent(lines, params.Mode, params.Tun)

	output := yamlHeader + strings.Join(outLines, "\n")
	if len(output) > 0 && !strings.HasSuffix(output, "\n") {
		output += "\n"
	}

	runtimePath := filepath.Join(params.BaseDir, domain.RuntimeConfigName)

	if existingContent, err := os.ReadFile(runtimePath); err == nil {
		if string(existingContent) == output {
			slog.Debug("运行时配置内容无变动，已跳过磁盘覆盖")
			return true, extracted, nil
		}
	}

	if err := writeTmpAndRename(params.BaseDir, runtimePath, []byte(output)); err != nil {
		return false, nil, fmt.Errorf("写入运行时配置失败: %w", err)
	}

	slog.Debug("已生成运行时配置", "source", params.RelPath, "target", domain.RuntimeConfigName)
	return true, extracted, nil
}

func processYAMLContent(lines []string, wantMode string, wantTun bool) ([]string, map[string]string) {
	extracted := make(map[string]string)

	var (
		hasMixedPort  bool
		mixedPortVal  string
		hasPort       bool
		portVal       string
		hasMode       bool
		hasExtCtrl    bool
		hasSecret     bool
		hasExtUI      bool
		extUIVal      string
		hasExtUIName  bool
		extUINameVal  string
		hasExtUIUrl   bool
		tunRootExists bool
		tunRootIndex  int = -1
		inTun         bool
		hasTunEnable  bool
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

			if strings.HasPrefix(trimmed, keyMixedPort) {
				hasMixedPort = true
				if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 {
					mixedPortVal = cleanVal(parts[1])
				}
			} else if strings.HasPrefix(trimmed, keyPort) {
				hasPort = true
				if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 {
					portVal = cleanVal(parts[1])
				}
			} else if strings.HasPrefix(trimmed, keyMode) {
				hasMode = true
				comment := extractComment(line)
				outLines[i] = fmt.Sprintf("%s%s %s%s", line[:prefixLen], keyMode, wantMode, comment)
			} else if strings.HasPrefix(trimmed, keyExtCtrl) {
				hasExtCtrl = true
			} else if strings.HasPrefix(trimmed, keySecret) {
				hasSecret = true
			} else if strings.HasPrefix(trimmed, keyExtUI) {
				hasExtUI = true
				if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 {
					extUIVal = cleanVal(parts[1])
					if extUIVal == "" {
						comment := extractComment(line)
						outLines[i] = fmt.Sprintf("%s%s '%s'%s", line[:prefixLen], keyExtUI, domain.DefaultExternalUI, comment)
						extUIVal = domain.DefaultExternalUI
					}
				}
			} else if strings.HasPrefix(trimmed, keyExtUIName) {
				hasExtUIName = true
				if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 {
					extUINameVal = cleanVal(parts[1])
				}
			} else if strings.HasPrefix(trimmed, keyExtUIUrl) {
				hasExtUIUrl = true
			} else if strings.HasPrefix(trimmed, keyTun) {
				tunRootExists = true
				tunRootIndex = i
				inTun = true

				if strings.Contains(trimmed, "{") && strings.Contains(trimmed, "}") {
					deviceRe := regexp.MustCompile(patTunDevice)
					if match := deviceRe.FindStringSubmatch(trimmed); len(match) > 1 {
						tunDeviceVal = cleanVal(match[1])
					}

					enableRe := regexp.MustCompile(patTunEnable)
					if enableRe.MatchString(trimmed) {
						hasTunEnable = true
						targetEnable := fmt.Sprintf("${1}%t", wantTun)
						outLines[i] = enableRe.ReplaceAllString(line, targetEnable)
					} else {
						hasTunEnable = true
						injection := fmt.Sprintf("{%s %t, ", keyEnable, wantTun)
						newTrimmed := strings.Replace(line, "{", injection, 1)
						outLines[i] = strings.Replace(newTrimmed, ", }", "}", 1)
					}
				}
			}
		} else if inTun && indent > 0 {
			if strings.HasPrefix(trimmed, keyEnable) {
				hasTunEnable = true
				comment := extractComment(line)
				outLines[i] = fmt.Sprintf("%s%s %t%s", line[:prefixLen], keyEnable, wantTun, comment)
			} else if strings.HasPrefix(trimmed, keyDevice) {
				if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 {
					tunDeviceVal = cleanVal(parts[1])
				}
			}
		}
	}

	if tunRootExists && !hasTunEnable {
		enableLine := fmt.Sprintf("  %s %t", keyEnable, wantTun)
		if tunRootIndex >= 0 && tunRootIndex < len(outLines) {
			outLines = append(outLines[:tunRootIndex+1], append([]string{enableLine}, outLines[tunRootIndex+1:]...)...)
		}
	}

	if hasMixedPort {
		extracted["port"] = mixedPortVal
	} else if hasPort {
		extracted["port"] = portVal
	}
	if hasExtUIName {
		extracted["external-ui-name"] = extUINameVal
	} else {
		extracted["external-ui-name"] = ""
	}
	if tunRootExists {
		extracted["tun_device"] = tunDeviceVal
	}

	var prependLines []string

	if !hasMixedPort {
		prependLines = append(prependLines, fmt.Sprintf("%s %s", keyMixedPort, domain.DefaultMixedPort))
		extracted["port"] = domain.DefaultMixedPort
	}
	if !hasMode {
		modeToSet := domain.DefaultMode
		if wantMode != "" {
			modeToSet = wantMode
		}
		prependLines = append(prependLines, fmt.Sprintf("%s %s", keyMode, modeToSet))
	}
	if !hasExtCtrl {
		prependLines = append(prependLines, fmt.Sprintf("%s %s", keyExtCtrl, domain.DefaultExternalController))
	}
	if !hasSecret {
		prependLines = append(prependLines, fmt.Sprintf("%s '%s'", keySecret, domain.DefaultSecret))
	}
	if !hasExtUI {
		prependLines = append(prependLines, fmt.Sprintf("%s '%s'", keyExtUI, domain.DefaultExternalUI))
	}
	if !hasExtUIUrl {
		prependLines = append(prependLines, fmt.Sprintf("%s '%s'", keyExtUIUrl, domain.DefaultExternalUIURL))
	}
	if !tunRootExists {
		prependLines = append(prependLines, keyTun)
		prependLines = append(prependLines, fmt.Sprintf("  %s %t", keyEnable, wantTun))
		extracted["tun_device"] = ""
	}

	if len(prependLines) > 0 {
		outLines = append(prependLines, outLines...)
	}

	return outLines, extracted
}

func extractComment(line string) string {
	inSingle, inDouble := false, false
	for i, char := range line {
		if char == '\'' && !inDouble {
			inSingle = !inSingle
		} else if char == '"' && !inSingle {
			inDouble = !inDouble
		} else if char == '#' && !inSingle && !inDouble {
			return " " + strings.TrimSpace(line[i:])
		}
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
	targetDir := filepath.Dir(targetPath)
	_ = os.MkdirAll(targetDir, 0755)

	tmpFile, err := os.CreateTemp(targetDir, "tmp_*.tmp")
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

func ResolveKernelEndpoint(yamlPath string) (string, string) {
	content, err := os.ReadFile(yamlPath)
	if err != nil {
		return domain.DefaultExternalController, domain.DefaultSecret
	}
	
	addr := domain.DefaultExternalController
	secret := domain.DefaultSecret
	
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, keyExtCtrl) {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				addr = cleanVal(parts[1])
			}
		} else if strings.HasPrefix(trimmed, keySecret) {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				secret = cleanVal(parts[1])
			}
		}
	}
	return addr, secret
}
