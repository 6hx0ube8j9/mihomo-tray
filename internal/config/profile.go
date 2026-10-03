package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"mihomo-tray/internal/domain"
)

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
	m.mu.RLock()
	isOverLimit := len(m.data.Profiles.Items) >= domain.MaxProfileCount
	m.mu.RUnlock()
	if isOverLimit {
		return "", false, fmt.Errorf("配置数量达到上限 (%d)", domain.MaxProfileCount)
	}

	absSrc, err := filepath.EvalSymlinks(srcPath)
	if err != nil {
		if absSrc, err = filepath.Abs(srcPath); err != nil {
			return "", false, err
		}
	}

	if relPath, isTree := IsInAppTree(m.baseDir, absSrc); isTree {
		if filepath.Dir(filepath.ToSlash(relPath)) == ProfilesDir {
			return relPath, false, nil
		}
	}

	profilesDirAbs := filepath.Join(m.baseDir, ProfilesDir)
	if err := os.MkdirAll(profilesDirAbs, 0755); err != nil {
		return "", false, err
	}

	baseName := strings.TrimSuffix(filepath.Base(absSrc), filepath.Ext(absSrc))
	finalRelPath := resolveUniqueProfileRelPath(profilesDirAbs, baseName)
	dstAbs := filepath.Join(m.baseDir, filepath.FromSlash(finalRelPath))

	if err := copyFileWithLimit(absSrc, dstAbs, domain.MaxProfileBytes); err != nil {
		return "", false, err
	}

	return finalRelPath, true, nil
}

func (m *Manager) RegisterNewProfile(relPath string) {
	m.Update(func(cfg *domain.TrayConfig) {
		for _, item := range cfg.Profiles.Items {
			if item.Path == relPath {
				return
			}
		}

		baseName := filepath.Base(relPath)
		displayName := strings.TrimSuffix(baseName, filepath.Ext(baseName))

		cfg.Profiles.Items = append(cfg.Profiles.Items, domain.ProfileItem{
			Name: displayName,
			Path: relPath,
		})
		enforceProfileLimit(cfg)
	})
}

func (m *Manager) UpsertProfile(item domain.ProfileItem) {
	m.Update(func(cfg *domain.TrayConfig) {
		for i, p := range cfg.Profiles.Items {
			if p.Path == item.Path {
				cfg.Profiles.Items[i] = item
				return
			}
		}

		cfg.Profiles.Items = append(cfg.Profiles.Items, item)
		enforceProfileLimit(cfg)
	})
}

func resolveUniqueProfileRelPath(profilesDirAbs, baseName string) string {
	candidate := baseName
	for i := 1; ; i++ {
		target := filepath.Join(profilesDirAbs, candidate+".yaml")
		if _, err := os.Stat(target); os.IsNotExist(err) {
			break
		}
		candidate = fmt.Sprintf("%s_%d", baseName, i)
	}
	return filepath.ToSlash(filepath.Join(ProfilesDir, candidate+".yaml"))
}

func copyFileWithLimit(srcPath, dstPath string, maxBytes int64) error {
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	targetDir := filepath.Dir(dstPath)
	_ = os.MkdirAll(targetDir, 0755)

	tmpFile, err := os.CreateTemp(targetDir, "profile.*.tmp")
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

	if _, err := io.Copy(tmpFile, io.LimitReader(srcFile, maxBytes)); err != nil {
		return err
	}

	var extra [1]byte
	if n, _ := srcFile.Read(extra[:]); n > 0 {
		return fmt.Errorf("文件体积超限")
	}

	if err := tmpFile.Sync(); err != nil {
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	cleaned = true

	return os.Rename(tmpName, dstPath)
}

func enforceProfileLimit(cfg *domain.TrayConfig) {
	if len(cfg.Profiles.Items) > domain.MaxProfileCount {
		cfg.Profiles.Items = append(cfg.Profiles.Items[:1], cfg.Profiles.Items[2:]...)
	}
}
