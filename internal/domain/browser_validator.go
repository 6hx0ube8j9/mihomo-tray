package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

const maxChromiumCacheEntries = 64

type chromiumCacheKey struct {
	path    string
	size    int64
	modTime int64
}

var (
	chromiumCacheMu sync.RWMutex
	chromiumCache   = make(map[chromiumCacheKey]bool, 16)
)

func ResolveBrowserPath(raw string) string {
	s := trimQuotesAndSpace(raw)
	if s == "" || strings.IndexByte(s, '%') == -1 {
		return s
	}

	var sb strings.Builder
	sb.Grow(len(s) + 32)

	i := 0
	for i < len(s) {
		pct := strings.IndexByte(s[i:], '%')
		if pct == -1 {
			sb.WriteString(s[i:])
			break
		}

		pct += i
		sb.WriteString(s[i:pct])

		if pct+1 >= len(s) {
			sb.WriteByte('%')
			break
		}

		nextPct := strings.IndexByte(s[pct+1:], '%')
		if nextPct == -1 {
			sb.WriteString(s[pct:])
			break
		}

		end := pct + 1 + nextPct
		varName := s[pct+1 : end]

		if varName != "" && !strings.ContainsAny(varName, `/\`) {
			if val, ok := os.LookupEnv(varName); ok {
				sb.WriteString(val)
				i = end + 1
				continue
			}
		}

		sb.WriteString(s[pct:end])
		i = end
	}

	return trimQuotesAndSpace(sb.String())
}

func trimQuotesAndSpace(s string) string {
	s = strings.TrimSpace(s)
	for len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

func statBrowserExecutable(resolvedPath string) (os.FileInfo, bool) {
	if resolvedPath == "" || !strings.EqualFold(filepath.Ext(resolvedPath), ".exe") {
		return nil, false
	}
	fi, err := os.Stat(resolvedPath)
	if err != nil || fi.IsDir() {
		return nil, false
	}
	return fi, true
}

func IsValidBrowserExecutable(exePath string) bool {
	resolved := ResolveBrowserPath(exePath)
	_, ok := statBrowserExecutable(resolved)
	return ok
}

func IsChromiumBrowserExecutable(exePath string) bool {
	resolved := ResolveBrowserPath(exePath)
	fi, ok := statBrowserExecutable(resolved)
	if !ok {
		return false
	}

	base := strings.ToLower(filepath.Base(resolved))
	if _, blocked := nonChromiumOrHelperExes[base]; blocked {
		return false
	}
	if _, known := knownChromiumMainExes[base]; known {
		return true
	}

	cacheKey := chromiumCacheKey{
		path:    strings.ToLower(filepath.Clean(resolved)),
		size:    fi.Size(),
		modTime: fi.ModTime().UnixNano(),
	}

	chromiumCacheMu.RLock()
	cachedRes, hit := chromiumCache[cacheKey]
	chromiumCacheMu.RUnlock()
	if hit {
		return cachedRes
	}

	res := inspectChromiumExecutable(resolved)

	chromiumCacheMu.Lock()
	if len(chromiumCache) >= maxChromiumCacheEntries {
		clear(chromiumCache)
	}
	chromiumCache[cacheKey] = res
	chromiumCacheMu.Unlock()

	return res
}

func inspectChromiumExecutable(resolvedPath string) bool {
	realPath := resolvedPath
	if target, err := filepath.EvalSymlinks(resolvedPath); err == nil && target != "" {
		realPath = target
	}

	realBase := strings.ToLower(filepath.Base(realPath))
	if _, blocked := nonChromiumOrHelperExes[realBase]; blocked {
		return false
	}
	if _, known := knownChromiumMainExes[realBase]; known {
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
		name := entry.Name()
		if name == "" || name[0] < '0' || name[0] > '9' {
			continue
		}
		inspected++
		if inspected > maxSubDirsToInspect {
			break
		}
		if checkArtifactsInDir(filepath.Join(dir, name)) {
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

	fi, err := f.Stat()
	if err != nil {
		return false
	}

	const maxScanBytes = 4 * 1024 * 1024
	readSize := fi.Size()
	if readSize <= 0 {
		return false
	}
	if readSize > maxScanBytes {
		readSize = maxScanBytes
	}

	buf := make([]byte, int(readSize))
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return false
	}
	buf = buf[:n]

	return bytes.Contains(buf, []byte("Chrome_WidgetWin")) ||
		bytes.Contains(buf, []byte("chrome_elf")) ||
		bytes.Contains(buf, []byte("msedge_elf")) ||
		bytes.Contains(buf, []byte("--user-data-dir"))
}

func SanitizeBrowserPrefix(exePath string) string {
	return sanitizeBrowserPrefixResolved(ResolveBrowserPath(exePath))
}

func sanitizeBrowserPrefixResolved(resolvedPath string) string {
	if resolvedPath == "" {
		return "browser"
	}

	base := filepath.Base(resolvedPath)
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

	res := sb.String()
	if res == "" {
		return "browser"
	}
	return res
}

func ResolveCustomBrowserDataDir(exePath string) string {
	resolved := ResolveBrowserPath(exePath)
	prefix := sanitizeBrowserPrefixResolved(resolved)

	normalized := strings.ToLower(filepath.Clean(resolved))
	sum := sha256.Sum256([]byte(normalized))

	var hexBuf [16]byte
	hex.Encode(hexBuf[:], sum[:8])

	return prefix + "_" + string(hexBuf[:])
}