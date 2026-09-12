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

func (m *Manager) PrepareLocalConfig(srcPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.data.Items) >= 5 {
		return fmt.Errorf("配置配额已满 (5/5)")
	}

	absSrc, err := filepath.EvalSymlinks(srcPath)
	if err != nil {
		absSrc, err = filepath.Abs(srcPath)
		if err != nil {
			return err
		}
	}

	relPath, isTree := IsInAppTree(m.baseDir, absSrc)

	for _, item := range m.data.Items {
		itemAbs := filepath.Join(m.baseDir, filepath.FromSlash(item.Path))
		if strings.EqualFold(itemAbs, absSrc) {
			m.data.Active = item.Path
			m.lockedSave()
			return nil
		}
	}

	baseName := strings.TrimSuffix(filepath.Base(absSrc), filepath.Ext(absSrc))
	finalName := baseName
	if strings.ToLower(finalName) == "config" {
		finalName = "config_1"
	}

	for i := 1; i <= 5; i++ {
		conflict := false
		for _, item := range m.data.Items {
			if strings.EqualFold(item.Name, finalName) {
				conflict = true
				break
			}
		}
		if !conflict {
			break
		}
		finalName = fmt.Sprintf("%s_%d", baseName, i)
	}

	var finalRelPath string

	if isTree {
		finalRelPath = relPath
	} else {
		profilesDir := filepath.Join(m.baseDir, "profiles")
		if err := os.MkdirAll(profilesDir, 0755); err != nil {
			return err
		}
		
		finalRelPath = filepath.ToSlash(filepath.Join("profiles", finalName+".yaml"))
		dstAbs := filepath.Join(m.baseDir, filepath.FromSlash(finalRelPath))

		srcFile, err := os.Open(absSrc)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		tmpFile, err := os.CreateTemp(m.baseDir, "profile.*.tmp")
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

		limitReader := io.LimitReader(srcFile, 15*1024*1024)
		if _, err := io.Copy(tmpFile, limitReader); err != nil {
			return err
		}

		var extra [1]byte
		if n, _ := srcFile.Read(extra[:]); n > 0 {
			return fmt.Errorf("目标文件体积超过 15MB 限制")
		}

		if err := tmpFile.Sync(); err != nil {
			return err
		}
		if err := tmpFile.Close(); err != nil {
			return err
		}
		cleaned = true

		if err := os.Rename(tmpName, dstAbs); err != nil {
			return err
		}
	}

	m.data.Items = append(m.data.Items, ProfileItem{
		Name: finalName,
		Path: finalRelPath,
	})
	m.data.Active = finalRelPath
	m.lockedSave()

	return nil
}
