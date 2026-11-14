package domain

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var nonChromiumOrHelperExes = map[string]struct{}{
	"firefox.exe":   {},
	"librewolf.exe": {},
	"waterfox.exe":  {},
	"zen.exe":       {},
	"floorp.exe":    {},
	"palemoon.exe":  {},
	"basilisk.exe":  {},
	"tor.exe":       {},
	"iexplore.exe":  {},

	"chrome_proxy.exe":        {},
	"chrome_pwa_launcher.exe": {},
	"msedge_proxy.exe":        {},
	"msedgewebview2.exe":      {},
	"ie_to_edge_stub.exe":     {},
	"elevation_service.exe":   {},
	"notification_helper.exe": {},
	"crashpad_handler.exe":    {},
	"chrmstp.exe":             {},
	"opera_autoupdate.exe":    {},
	"opera_crashreporter.exe": {},
	"setup.exe":               {},
	"updater.exe":             {},
}

var knownChromiumMainExes = map[string]struct{}{
	"chrome.exe":        {},
	"msedge.exe":        {},
	"brave.exe":         {},
	"vivaldi.exe":       {},
	"chromium.exe":      {},
	"opera.exe":         {},
	"thorium.exe":       {},
	"supermium.exe":     {},
	"centbrowser.exe":   {},
	"catsxp.exe":        {},
	"whale.exe":         {},
	"yandex.exe":        {},
	"arc.exe":           {},
	"360chrome.exe":     {},
	"360se.exe":         {},
	"qqbrowser.exe":     {},
	"sogouexplorer.exe": {},
	"twinkstar.exe":     {},
	"maxthon.exe":       {},
}

var chromiumRuntimeArtifacts = []string{
	"icudtl.dat",
	"v8_context_snapshot.bin",
	"chrome_elf.dll",
	"msedge_elf.dll",
	"opera_elf.dll",
	"chrome.dll",
	"msedge.dll",
	"chrome_100_percent.pak",
	"msedge_100_percent.pak",
}

func IsValidBrowserExecutable(exePath string) bool {
	cleanPath := strings.TrimSpace(exePath)
	if cleanPath == "" || !strings.EqualFold(filepath.Ext(cleanPath), ".exe") {
		return false
	}
	fi, err := os.Stat(cleanPath)
	if err != nil || fi.IsDir() {
		return false
	}
	return true
}

func IsChromiumBrowserExecutable(exePath string) bool {
	if !IsValidBrowserExecutable(exePath) {
		return false
	}

	cleanPath := strings.TrimSpace(exePath)
	if _, blocked := nonChromiumOrHelperExes[strings.ToLower(filepath.Base(cleanPath))]; blocked {
		return false
	}

	realPath := cleanPath
	if resolved, err := filepath.EvalSymlinks(cleanPath); err == nil && resolved != "" {
		realPath = resolved
	}

	realBase := strings.ToLower(filepath.Base(realPath))
	if _, blocked := nonChromiumOrHelperExes[realBase]; blocked {
		return false
	}
	if _, ok := knownChromiumMainExes[realBase]; ok {
		return true
	}

	if hasChromiumDirArtifacts(filepath.Dir(realPath)) {
		return true
	}

	return hasChromiumBinarySignature(realPath)
}

func hasChromiumDirArtifacts(dir string) bool {
	if dir == "" {
		return false
	}

	if checkArtifactsInDir(dir) {
		return true
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}

	const maxSubDirsToInspect = 16
	inspected := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		inspected++
		if inspected > maxSubDirsToInspect {
			break
		}
		if checkArtifactsInDir(filepath.Join(dir, entry.Name())) {
			return true
		}
	}

	return false
}

func checkArtifactsInDir(dir string) bool {
	for _, artifact := range chromiumRuntimeArtifacts {
		if fi, err := os.Stat(filepath.Join(dir, artifact)); err == nil && !fi.IsDir() {
			return true
		}
	}
	return false
}

func hasChromiumBinarySignature(exePath string) bool {
	f, err := os.Open(exePath)
	if err != nil {
		return false
	}
	defer f.Close()

	const maxScanBytes = 8 * 1024 * 1024
	buf, err := io.ReadAll(io.LimitReader(f, maxScanBytes))
	if err != nil || len(buf) == 0 {
		return false
	}

	return bytes.Contains(buf, []byte("Chrome_WidgetWin")) ||
		bytes.Contains(buf, []byte("chrome_elf")) ||
		bytes.Contains(buf, []byte("msedge_elf")) ||
		bytes.Contains(buf, []byte("--user-data-dir"))
}

func ResolveCustomBrowserDataDir(exePath string) string {
	cleaned := strings.TrimSpace(exePath)
	if cleaned == "" {
		return "browser_custom"
	}

	base := filepath.Base(cleaned)
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	base = strings.ToLower(base)

	var sb strings.Builder
	for i := 0; i < len(base); i++ {
		c := base[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			sb.WriteByte(c)
		}
	}

	prefix := sb.String()
	if prefix == "" {
		prefix = "browser"
	}
	return prefix + "_custom"
}