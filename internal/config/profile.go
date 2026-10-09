package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/fs"
)

const profileFileExt = ".yaml"

var ErrProfileLimitExceeded = fmt.Errorf("配置数量已达上限 (%d 个)。", domain.MaxProfileCount)
var ErrProfileNameConflict = errors.New("配置名称已存在")

func (m *Manager) AllocateRemoteProfilePath(rawName string) (displayName string, relPath string, err error) {
	if strings.TrimSpace(rawName) == "" {
		rawName = fmt.Sprintf("%d", time.Now().Unix())
	}
	safeName := strings.ReplaceAll(rawName, "/", "_")
	safeName = strings.ReplaceAll(safeName, "\\", "_")

	fileName := safeName + profileFileExt
	targetRelPath := filepath.ToSlash(filepath.Join(domain.ProfilesDir, fileName))

	if _, exists := m.GetProfileByPath(targetRelPath); exists {
		return "", "", ErrProfileNameConflict
	}
	return safeName, targetRelPath, nil
}

func (m *Manager) CheckProfileLimit() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.data.Profiles.Items) >= domain.MaxProfileCount {
		return ErrProfileLimitExceeded
	}
	return nil
}

func IsInAppTree(appDir, targetPath string) (string, bool) {
	rel, err := filepath.Rel(appDir, targetPath)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func (m *Manager) SafeCopyUntrustedConfig(srcPath string) (string, bool, error) {
	if err := m.CheckProfileLimit(); err != nil {
		return "", false, err
	}

	absSrc, err := filepath.EvalSymlinks(srcPath)
	if err != nil {
		if absSrc, err = filepath.Abs(srcPath); err != nil {
			return "", false, fmt.Errorf("无法解析目标文件路径: %w", err)
		}
	}

	if relPath, isTree := IsInAppTree(m.baseDir, absSrc); isTree {
		if filepath.Dir(filepath.ToSlash(relPath)) == domain.ProfilesDir {
			return relPath, false, nil
		}
	}

	profilesDirAbs := m.ProfilesDirAbs()
	baseName := strings.TrimSuffix(filepath.Base(absSrc), filepath.Ext(absSrc))
	finalRelPath := resolveUniqueProfileRelPath(profilesDirAbs, baseName)
	dstAbs := m.GetProfileAbsPath(finalRelPath)

	if err := fs.CopyFileWithLimit(absSrc, dstAbs, domain.MaxProfileBytes); err != nil {
		return "", false, fmt.Errorf("文件导入受阻。\n\n%w", err)
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
	})
}

func (m *Manager) GetActivePathAbs() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.data.Profiles.Active == "" {
		return ""
	}
	return m.GetProfileAbsPath(m.data.Profiles.Active)
}

func (m *Manager) GetActivePath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.data.Profiles.Active
}

func (m *Manager) GetProfiles() []domain.ProfileItem {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make([]domain.ProfileItem, len(m.data.Profiles.Items))
	copy(res, m.data.Profiles.Items)
	return res
}

func (m *Manager) GetProfileByPath(relPath string) (domain.ProfileItem, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.data.Profiles.Items {
		if p.Path == relPath {
			return p, true
		}
	}
	return domain.ProfileItem{}, false
}

func (m *Manager) SetActiveProfile(relPath string) {
	if m.GetActivePath() == relPath {
		return
	}
	m.Update(func(cfg *domain.TrayConfig) {
		cfg.Profiles.Active = relPath
	})
}

func (m *Manager) RemoveProfile(relPath string) {
	m.Update(func(cfg *domain.TrayConfig) {
		if relPath == cfg.Profiles.Active {
			slog.Info("当前活跃配置被移除，系统切换至空转状态")
			cfg.Profiles.Active = ""
		}

		var newItems []domain.ProfileItem
		for _, item := range cfg.Profiles.Items {
			if item.Path != relPath {
				newItems = append(newItems, item)
			}
		}
		cfg.Profiles.Items = newItems
	})
}

func (m *Manager) DeleteProfile(relPath string) error {
	if relPath == "" {
		return nil
	}

	absPath := m.GetProfileAbsPath(relPath)
	var removeErr error
	if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
		removeErr = err
		slog.Warn("清理本地物理配置文件受阻", "path", absPath, "err", err)
	}

	m.RemoveProfile(relPath)
	return removeErr
}

func (m *Manager) MoveProfile(relPath string, offset int) bool {
	m.mu.RLock()
	canMove := false
	for i, p := range m.data.Profiles.Items {
		if p.Path == relPath {
			targetIdx := i + offset
			if targetIdx >= 0 && targetIdx < len(m.data.Profiles.Items) {
				canMove = true
			}
			break
		}
	}
	m.mu.RUnlock()

	if !canMove {
		return false
	}

	moved := false
	m.Update(func(cfg *domain.TrayConfig) {
		for i, p := range cfg.Profiles.Items {
			if p.Path == relPath {
				targetIdx := i + offset
				if targetIdx >= 0 && targetIdx < len(cfg.Profiles.Items) {
					cfg.Profiles.Items[i], cfg.Profiles.Items[targetIdx] = cfg.Profiles.Items[targetIdx], cfg.Profiles.Items[i]
					moved = true
				}
				return
			}
		}
	})
	return moved
}

func (m *Manager) ValidatePhysicalFile(relPath string) error {
	if relPath == "" {
		return fmt.Errorf("未指定配置文件路径")
	}
	absPath := m.GetProfileAbsPath(relPath)
	fi, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("本地配置文件已丢失或被移除")
		}
		return fmt.Errorf("无法读取本地配置文件，请检查系统权限。\n\n%w", err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("配置文件已损坏 (文件内容为空)")
	}
	return nil
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
	return filepath.ToSlash(filepath.Join(domain.ProfilesDir, candidate+".yaml"))
}
