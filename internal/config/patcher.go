package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

func processYAMLContent(lines []string, wantMode string, wantTun bool) ([]string, map[string]string, bool) {
	extracted := make(map[string]string)
	modified := false

	var (
		hasMixedPort  bool
		mixedPortVal  string
		hasPort       bool
		portVal       string
		hasMode       bool
		hasExtCtrl    bool
		extCtrlVal    string
		hasSecret     bool
		secretVal     string
		hasExtUI      bool
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

			if strings.HasPrefix(trimmed, "mixed-port:") {
				hasMixedPort = true
				if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 {
					mixedPortVal = cleanVal(parts[1])
				}
			} else if strings.HasPrefix(trimmed, "port:") {
				hasPort = true
				if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 {
					portVal = cleanVal(parts[1])
				}
			} else if strings.HasPrefix(trimmed, "mode:") {
				hasMode = true
				if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 {
					currentModeVal := strings.ToLower(cleanVal(parts[1]))
					if wantMode != "" && currentModeVal != strings.ToLower(wantMode) {
						comment := extractComment(line)
						targetLine := fmt.Sprintf("%smode: %s%s", line[:prefixLen], wantMode, comment)
						if outLines[i] != targetLine {
							outLines[i] = targetLine
							modified = true
						}
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

				if strings.Contains(trimmed, "{") && strings.Contains(trimmed, "}") {
					deviceRe := regexp.MustCompile(`device:\s*([^,}]+)`)
					if match := deviceRe.FindStringSubmatch(trimmed); len(match) > 1 {
						tunDeviceVal = cleanVal(match[1])
					}

					enableRe := regexp.MustCompile(`(?i)(enable:\s*)(true|false)`)
					if enableRe.MatchString(trimmed) {
						hasTunEnable = true
						targetEnable := fmt.Sprintf("${1}%t", wantTun)
						newTrimmed := enableRe.ReplaceAllString(line, targetEnable)
						if newTrimmed != line {
							outLines[i] = newTrimmed
							modified = true
						}
					} else {
						hasTunEnable = true
						injection := fmt.Sprintf("{enable: %t, ", wantTun)
						newTrimmed := strings.Replace(line, "{", injection, 1)
						newTrimmed = strings.Replace(newTrimmed, ", }", "}", 1)

						if newTrimmed != line {
							outLines[i] = newTrimmed
							modified = true
						}
					}
				}
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
		extracted["tun_device"] = tunDeviceVal
	}

	var prependLines []string

	if !hasMixedPort {
		prependLines = append(prependLines, fmt.Sprintf("mixed-port: %s", DefaultMixedPort))
		modified = true
		extracted["port"] = DefaultMixedPort
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
		modified = true
		extracted["tun_device"] = ""
	}

	if len(prependLines) > 0 {
		outLines = append(prependLines, outLines...)
	}

	return outLines, extracted, modified
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
