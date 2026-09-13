package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type ProfileItem struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func IsInAppTree(appDir, targetPath string) (string, bool) {
	rel, err := filepath.Rel(appDir, targetPath)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func TruncateMiddle(name string) string {
	r := []rune(name)
	if len(r) <= 14 {
		return name
	}
	return string(r[:8]) + "..." + string(r[len(r)-6:])
}

func (m *Manager) SafeCopyUntrustedConfig(srcPath string) (string, bool, error) {
	m.mu.Lock()
	if len(m.data.Items) >= 5 {
		m.mu.Unlock()
		return "", false, fmt.Errorf("配置配额已满 (5/5)")
	}
	m.mu.Unlock()

	absSrc, err := filepath.EvalSymlinks(srcPath)
	if err != nil {
		absSrc, err = filepath.Abs(srcPath)
		if err != nil {
			return "", false, err
		}
	}

	relPath, isTree := IsInAppTree(m.baseDir, absSrc)
	if isTree {
		return relPath, false, nil
	}

	profilesDir := filepath.Join(m.baseDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0755); err != nil {
		return "", false, err
	}

	baseName := strings.TrimSuffix(filepath.Base(absSrc), filepath.Ext(absSrc))
	if strings.ToLower(baseName) == "config" {
		baseName = "config_1"
	}

	finalName := baseName
	for i := 1; i <= 50; i++ {
		conflictPath := filepath.Join(profilesDir, finalName+".yaml")
		if _, err := os.Stat(conflictPath); os.IsNotExist(err) {
			break
		}
		finalName = fmt.Sprintf("%s_%d", baseName, i)
	}

	finalRelPath := filepath.ToSlash(filepath.Join("profiles", finalName+".yaml"))
	dstAbs := filepath.Join(m.baseDir, filepath.FromSlash(finalRelPath))

	srcFile, err := os.Open(absSrc)
	if err != nil {
		return "", false, err
	}
	defer srcFile.Close()

	tmpFile, err := os.CreateTemp(m.baseDir, "profile.*.tmp")
	if err != nil {
		return "", false, err
	}
	tmpName := tmpFile.Name()

	cleaned := false
	defer func() {
		if !cleaned {
			_ = tmpFile.Close()
			_ = os.Remove(tmpName)
		}
	}()

	limitReader := io.LimitReader(srcFile, 15*1024*1024)
	if _, err := io.Copy(tmpFile, limitReader); err != nil {
		return "", false, err
	}

	var extra [1]byte
	if n, _ := srcFile.Read(extra[:]); n > 0 {
		return "", false, fmt.Errorf("目标文件体积超过 15MB 限制")
	}

	if err := tmpFile.Sync(); err != nil {
		return "", false, err
	}
	if err := tmpFile.Close(); err != nil {
		return "", false, err
	}
	cleaned = true

	if err := os.Rename(tmpName, dstAbs); err != nil {
		return "", false, err
	}

	return finalRelPath, true, nil
}

func (m *Manager) RegisterNewProfile(relPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, item := range m.data.Items {
		if item.Path == relPath {
			m.data.Active = relPath
			m.lockedSave()
			return
		}
	}

	baseName := filepath.Base(relPath)
	displayName := strings.TrimSuffix(baseName, filepath.Ext(baseName))

	m.data.Items = append(m.data.Items, ProfileItem{
		Name: displayName,
		Path: relPath,
	})

	if len(m.data.Items) > 5 {
		m.data.Items = append(m.data.Items[:1], m.data.Items[2:]...)
	}

	m.data.Active = relPath
	m.lockedSave()
}
