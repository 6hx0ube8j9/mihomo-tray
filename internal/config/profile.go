package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ProfileItem struct {
	Name string `json:"name"`
	Path string `json:"path"`

	URL        string `json:"url,omitempty"`
	AutoUpdate bool   `json:"auto_update,omitempty"`
	Interval   int    `json:"interval,omitempty"`
	LastUpdate int64  `json:"last_update,omitempty"`

	Upload   int64 `json:"upload,omitempty"`
	Download int64 `json:"download,omitempty"`
	Total    int64 `json:"total,omitempty"`
	Expire   int64 `json:"expire,omitempty"`
}

func (p *ProfileItem) NeedUpdate() bool {
	if p.URL == "" || !p.AutoUpdate || p.Interval <= 0 {
		return false
	}
	
	nextUpdate := p.LastUpdate + int64(p.Interval*24*3600)
	
	return time.Now().Unix() >= nextUpdate
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
	lowerName := strings.ToLower(baseName)
	if lowerName == "config" || lowerName == "default" {
		baseName = baseName + "_1"
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

	cacheDir := filepath.Join(m.baseDir, ".cache")
	_ = os.MkdirAll(cacheDir, 0755)
	tmpFile, err := os.CreateTemp(cacheDir, "profile.*.tmp")
	
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

func (m *Manager) UpsertProfile(item ProfileItem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, p := range m.data.Items {
		if p.Path == item.Path {
			m.data.Items[i] = item
			m.lockedSave()
			return
		}
	}
	m.data.Items = append(m.data.Items, item)
	if len(m.data.Items) > 5 {  
		m.data.Items = append(m.data.Items[:1], m.data.Items[2:]...)
	}
	m.lockedSave()
}
