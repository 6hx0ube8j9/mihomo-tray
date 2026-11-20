package fs

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
	"chrome.exe":              {},
	"msedge.exe":              {},
	"brave.exe":               {},
	"vivaldi.exe":             {},
	"chromium.exe":            {},
	"opera.exe":               {},
	"thorium.exe":             {},
	"supermium.exe":           {},
	"centbrowser.exe":         {},
	"catsxp.exe":              {},
	"whale.exe":               {},
	"yandex.exe":              {},
	"arc.exe":                 {},
	"helium.exe":              {},
	"origin.exe":              {},
	"duckduckgo.exe":          {},
	"samsunginternet.exe":     {},
	"samsungbrowser.exe":      {},
	"puffin.exe":              {},
	"puffinsecurebrowser.exe": {},
	"360chrome.exe":           {},
	"360se.exe":               {},
	"qqbrowser.exe":           {},
	"sogouexplorer.exe":       {},
	"twinkstar.exe":           {},
	"maxthon.exe":             {},
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

func standardBrowserCandidates() []chromiumCandidate {
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
			{pf, `BraveSoftware\Brave-Origin\Application\brave.exe`, "brave-origin"},
			{localApp, `BraveSoftware\Brave-Origin\Application\brave.exe`, "brave-origin"},
			{localApp, `imput\Helium\Application\chrome.exe`, "helium"},
			{pf, `imput\Helium\Application\chrome.exe`, "helium"},
			{pf, `BraveSoftware\Brave-Browser\Application\brave.exe`, "brave"},
			{localApp, `BraveSoftware\Brave-Browser\Application\brave.exe`, "brave"},
			{localApp, `Vivaldi\Application\vivaldi.exe`, "vivaldi"},
			{pf, `Vivaldi\Application\vivaldi.exe`, "vivaldi"},
			{pf86, `Vivaldi\Application\vivaldi.exe`, "vivaldi"},
			{pf, `Samsung\Internet\Application\samsunginternet.exe`, "samsung"},
			{localApp, `Samsung\Internet\Application\samsunginternet.exe`, "samsung"},
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

func IsBrowserExecutable(exePath string) bool {
	_, ok := statExecutable(ResolveBrowserPath(exePath))
	return ok
}

func IsChromiumBrowser(exePath string) bool {
	resolved := ResolveBrowserPath(exePath)
	fi, ok := statExecutable(resolved)
	if !ok {
		return false
	}

	if isChromium, decided := classifyExecutableName(filepath.Base(resolved)); decided {
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

func DetectChromiumBrowser() (path, dataDir string) {
	detectOnce.Do(func() {
		for _, c := range standardBrowserCandidates() {
			if _, ok := statExecutable(c.path); ok {
				defaultChromiumPath = c.path
				defaultChromiumTag = c.tag
				return
			}
		}
	})
	return defaultChromiumPath, defaultChromiumTag
}

// ResolveBrowserDataDir returns a stable profile directory name for exePath:
// known standard browsers map to their short tag, while custom paths derive a prefix+hash name.
func ResolveBrowserDataDir(exePath string) string {
	resolved := ResolveBrowserPath(exePath)
	if resolved == "" {
		return ""
	}

	cleanPath := filepath.Clean(resolved)
	if tag, ok := matchStandardChromium(cleanPath); ok {
		return tag
	}

	prefix := sanitizeBrowserPrefix(cleanPath)
	sum := sha256.Sum256([]byte(strings.ToLower(cleanPath)))

	var hexBuf [16]byte
	hex.Encode(hexBuf[:], sum[:8])
	return prefix + "_" + string(hexBuf[:])
}

func matchStandardChromium(cleanPath string) (string, bool) {
	candidates := standardBrowserCandidates()
	for _, c := range candidates {
		if strings.EqualFold(cleanPath, c.path) {
			return c.tag, true
		}
	}

	if target, err := filepath.EvalSymlinks(cleanPath); err == nil && target != "" {
		realPath := filepath.Clean(target)
		if !strings.EqualFold(realPath, cleanPath) {
			for _, c := range candidates {
				if strings.EqualFold(realPath, c.path) {
					return c.tag, true
				}
			}
		}
	}

	return "", false
}

func sanitizeBrowserPrefix(exePath string) string {
	base := filepath.Base(exePath)
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

	if sb.Len() == 0 {
		return "browser"
	}
	return sb.String()
}

func trimQuotesAndSpace(s string) string {
	s = strings.TrimSpace(s)
	for len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

func statExecutable(resolvedPath string) (os.FileInfo, bool) {
	if resolvedPath == "" || !strings.EqualFold(filepath.Ext(resolvedPath), ".exe") {
		return nil, false
	}
	fi, err := os.Stat(resolvedPath)
	if err != nil || fi.IsDir() {
		return nil, false
	}
	return fi, true
}

func classifyExecutableName(baseName string) (isChromium, decided bool) {
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

	if isChromium, decided := classifyExecutableName(filepath.Base(realPath)); decided {
		return isChromium
	}

	if hasChromiumArtifacts(filepath.Dir(realPath)) {
		return true
	}

	return hasChromiumBinarySignature(realPath)
}

func hasChromiumArtifacts(dir string) bool {
	if dir == "" {
		return false
	}
	if dirHasChromiumArtifacts(dir) {
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
		if dirHasChromiumArtifacts(filepath.Join(dir, name)) {
			return true
		}
	}
	return false
}

func dirHasChromiumArtifacts(dir string) bool {
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