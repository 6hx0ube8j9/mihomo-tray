package browser

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolvePath expands environment variables and resolves relative paths against baseDir.
func ResolvePath(raw, baseDir string) string {
	s := expandEnvVars(trimQuotesAndSpace(raw))
	if s == "" {
		return ""
	}

	s = filepath.FromSlash(s)
	if isRelativePath(s) {
		if base := strings.TrimSpace(baseDir); base != "" {
			return filepath.Clean(filepath.Join(filepath.FromSlash(base), s))
		}
	}
	return filepath.Clean(s)
}

// ToPortablePath converts a path inside baseDir to a relative ".\..." form.
// Paths outside baseDir are returned unchanged (trimmed).
func ToPortablePath(raw, baseDir string) string {
	trimmed := trimQuotesAndSpace(raw)
	if trimmed == "" {
		return ""
	}

	resolved := ResolvePath(trimmed, baseDir)
	if portable, ok := relInsideBaseDir(resolved, baseDir); ok {
		return portable
	}
	return trimmed
}

// ResolveNearestDir returns the nearest existing directory for rawPath resolved against baseDir.
func ResolveNearestDir(rawPath, baseDir string) string {
	resolved := ResolvePath(rawPath, baseDir)
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

func trimQuotesAndSpace(s string) string {
	s = strings.TrimSpace(s)
	for len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

func expandEnvVars(s string) string {
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

func isRelativePath(p string) bool {
	if p == "" {
		return false
	}
	return !filepath.IsAbs(p) &&
		filepath.VolumeName(p) == "" &&
		!strings.HasPrefix(p, string(filepath.Separator))
}

func relInsideBaseDir(targetPath, baseDir string) (string, bool) {
	base := strings.TrimSpace(baseDir)
	if base == "" || targetPath == "" {
		return "", false
	}

	absBase, err := filepath.Abs(filepath.FromSlash(base))
	if err != nil {
		return "", false
	}
	absTarget, err := filepath.Abs(filepath.FromSlash(targetPath))
	if err != nil {
		return "", false
	}

	cleanBase := filepath.Clean(absBase)
	cleanTarget := filepath.Clean(absTarget)

	prefix := cleanBase
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}

	if len(cleanTarget) <= len(prefix) || !strings.EqualFold(cleanTarget[:len(prefix)], prefix) {
		return "", false
	}

	rel := cleanTarget[len(prefix):]
	if rel == "" {
		return "", false
	}
	return "." + string(filepath.Separator) + rel, true
}
