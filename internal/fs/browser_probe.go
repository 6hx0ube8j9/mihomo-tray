package fs

import (
	"bytes"
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

type chromiumCandidate struct {
	path string
	tag  string
}

var (
	chromiumCacheMu sync.RWMutex
	chromiumCache   = make(map[chromiumCacheKey]bool, 16)

	candidatesOnce     sync.Once
	standardCandidates []chromiumCandidate

	detectOnce          sync.Once
	defaultChromiumPath string
	defaultChromiumTag  string
)

func getStandardCandidates() []chromiumCandidate {
	candidatesOnce.Do(func() {
		pf86 := os.Getenv("ProgramFiles(x86)")
		pf := os.Getenv("ProgramFiles")
		localApp := os.Getenv("LocalAppData")

		raw := []struct {
			root string
			sub  string
			tag  string
		}{
			{pf86, `Microsoft\Edge\Application\msedge.exe`, "edge"},
			{pf, `Microsoft\Edge\Application\msedge.exe`, "edge"},
			{localApp, `Microsoft\Edge\Application\msedge.exe`, "edge"},
			{pf, `Google\Chrome\Application\chrome.exe`, "chrome"},
			{pf86, `Google\Chrome\Application\chrome.exe`, "chrome"},
			{localApp, `Google\Chrome\Application\chrome.exe`, "chrome"},
			{pf, `BraveSoftware\Brave-Browser\Application\brave.exe`, "brave"},
			{localApp, `BraveSoftware\Brave-Browser\Application\brave.exe`, "brave"},
			{localApp, `Vivaldi\Application\vivaldi.exe`, "vivaldi"},
			{pf, `Vivaldi\Application\vivaldi.exe`, "vivaldi"},
			{pf86, `Vivaldi\Application\vivaldi.exe`, "vivaldi"},
		}

		standardCandidates = make([]chromiumCandidate, 0, len(raw))
		for _, c := range raw {
			if c.root != "" {
				standardCandidates = append(standardCandidates, chromiumCandidate{
					path: filepath.Clean(filepath.Join(c.root, c.sub)),
					tag:  c.tag,
				})
			}
		}
	})
	return standardCandidates
}

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

func ResolveNearestDir(rawPath string) string {
	resolved := ResolveBrowserPath(rawPath)
	if resolved == "" {
		return ""
	}

	candidate := filepath.Clean(filepath.FromSlash(resolved))
	if !filepath.IsAbs(candidate) {
		return ""
	}

	if fi, err := os.Stat(candidate); err != nil || !fi.IsDir() {
		candidate = filepath.Dir(candidate)
	}

	for candidate != "" && candidate != "." {
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			return candidate
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			break
		}
		candidate = parent
	}

	return ""
}

func IsValidBrowserExecutable(exePath string) bool {
	_, ok := statBrowserExecutable(ResolveBrowserPath(exePath))
	return ok
}

func IsChromiumBrowserExecutable(exePath string) bool {
	resolved := ResolveBrowserPath(exePath)
	fi, ok := statBrowserExecutable(resolved)
	if !ok {
		return false
	}

	if isChromium, decided := classifyBrowserBase(filepath.Base(resolved)); decided {
		return isChromium
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

func DetectChromiumBrowser() (path string, tag string) {
	detectOnce.Do(func() {
		for _, c := range getStandardCandidates() {
			if _, ok := statBrowserExecutable(c.path); ok {
				defaultChromiumPath = c.path
				defaultChromiumTag = c.tag
				return
			}
		}
	})
	return defaultChromiumPath, defaultChromiumTag
}

func MatchStandardChromium(exePath string) (string, bool) {
	resolved := ResolveBrowserPath(exePath)
	if resolved == "" {
		return "", false
	}

	candidates := getStandardCandidates()
	cleanTarget := filepath.Clean(resolved)

	for _, c := range candidates {
		if strings.EqualFold(cleanTarget, c.path) {
			return c.tag, true
		}
	}

	if target, err := filepath.EvalSymlinks(cleanTarget); err == nil && target != "" {
		realTarget := filepath.Clean(target)
		if !strings.EqualFold(realTarget, cleanTarget) {
			for _, c := range candidates {
				if strings.EqualFold(realTarget, c.path) {
					return c.tag, true
				}
			}
		}
	}

	return "", false
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

func classifyBrowserBase(baseName string) (isChromium bool, decided bool) {
	lower := strings.ToLower(baseName)
	if _, blocked := nonChromiumOrHelperExes[lower]; blocked {
		return false, true
	}
	if _, known := knownChromiumMainExes[lower]; known {
		return true, true
	}
	return false, false
}

func inspectChromiumExecutable(resolvedPath string) bool {
	realPath := resolvedPath
	if target, err := filepath.EvalSymlinks(resolvedPath); err == nil && target != "" {
		realPath = target
	}

	if isChromium, decided := classifyBrowserBase(filepath.Base(realPath)); decided {
		return isChromium
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