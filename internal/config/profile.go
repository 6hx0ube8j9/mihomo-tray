package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/fs"
)

const profileFileExt = ".yaml"

var (
	ErrProfileLimitExceeded = fmt.Errorf("配置文件已达 %d 个上限", domain.MaxProfileCount)
	ErrProfileNameConflict  = errors.New("配置名称已存在")
	ErrProfilePathEmpty     = errors.New("未指定配置文件路径")
	ErrProfileNotFound      = errors.New("目标文件不存在")
	ErrProfileEmpty         = errors.New("配置文件内容为空")
)

var windowsReservedNames = map[string]struct{}{
	"CON": {}, "PRN": {}, "AUX": {}, "NUL": {},
	"COM1": {}, "COM2": {}, "COM3": {}, "COM4": {}, "COM5": {}, "COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
	"LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {}, "LPT5": {}, "LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
}

func sanitizeProfileFileStem(raw string) string {
	var sb strings.Builder
	sb.Grow(len(raw))
	for _, r := range strings.TrimSpace(raw) {
		switch {
		case r < 0x20:
			sb.WriteByte('_')
		case strings.ContainsRune(`<>:"/\|?*`, r):
			sb.WriteByte('_')
		default:
			sb.WriteRune(r)
		}
	}

	stem := strings.TrimRight(strings.TrimSpace(sb.String()), " .")
	if stem == "" {
		stem = fmt.Sprintf("%d", time.Now().Unix())
	}

	baseUpper := strings.ToUpper(stem)
	if dot := strings.IndexByte(baseUpper, '.'); dot >= 0 {
		baseUpper = baseUpper[:dot]
	}
	if _, reserved := windowsReservedNames[baseUpper]; reserved {
		stem = "_" + stem
	}
	return stem
}

func (m *Manager) AllocateRemoteProfilePath(rawName string) (displayName string, relPath string, err error) {
	if err := m.CheckProfileLimit(); err != nil {
		return "", "", err
	}

	displayName = strings.TrimSpace(rawName)
	if displayName == "" {
		displayName = fmt.Sprintf("%d", time.Now().Unix())
	}

	fileName := sanitizeProfileFileStem(displayName) + profileFileExt
	targetRelPath := filepath.ToSlash(filepath.Join(domain.ProfilesDir, fileName))

	if _, exists := m.GetProfileByPath(targetRelPath); exists {
		return "", "", ErrProfileNameConflict
	}
	return displayName, targetRelPath, nil
}

func (m *Manager) CheckProfileLimit() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.data.Profiles.Items) >= domain.MaxProfileCount {
		return ErrProfileLimitExceeded
	}
	return nil
}

func isInAppTree(appDir, targetPath string) (string, bool) {
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
			return "", false, err
		}
	}

	if relPath, isTree := isInAppTree(m.baseDir, absSrc); isTree {
		if filepath.Dir(filepath.ToSlash(relPath)) == domain.ProfilesDir {
			return relPath, false, nil
		}
	}

	profilesDirAbs := m.ProfilesDirAbs()
	baseName := strings.TrimSuffix(filepath.Base(absSrc), filepath.Ext(absSrc))
	finalRelPath := resolveUniqueProfileRelPath(profilesDirAbs, sanitizeProfileFileStem(baseName))
	dstAbs := m.GetProfileAbsPath(finalRelPath)

	if err := fs.CopyFileWithLimit(absSrc, dstAbs, domain.MaxProfileBytes); err != nil {
		return "", false, err
	}

	return finalRelPath, true, nil
}

func (m *Manager) RegisterNewProfile(relPath string) {
	m.TryUpdate(func(cfg *domain.TrayConfig) bool {
		for _, item := range cfg.Profiles.Items {
			if item.Path == relPath {
				return false
			}
		}

		baseName := filepath.Base(relPath)
		displayName := strings.TrimSuffix(baseName, filepath.Ext(baseName))

		cfg.Profiles.Items = append(cfg.Profiles.Items, domain.ProfileItem{
			Name: displayName,
			Path: relPath,
		})
		return true
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
	m.TryUpdate(func(cfg *domain.TrayConfig) bool {
		if cfg.Profiles.Active == relPath {
			return false
		}
		cfg.Profiles.Active = relPath
		return true
	})
}

func (m *Manager) RemoveProfile(relPath string) {
	m.TryUpdate(func(cfg *domain.TrayConfig) bool {
		changed := false
		if relPath == cfg.Profiles.Active {
			cfg.Profiles.Active = ""
			changed = true
		}

		var newItems []domain.ProfileItem
		for _, item := range cfg.Profiles.Items {
			if item.Path != relPath {
				newItems = append(newItems, item)
			} else {
				changed = true
			}
		}
		if !changed {
			return false
		}
		cfg.Profiles.Items = newItems
		return true
	})
}

func (m *Manager) DeleteProfile(relPath string) error {
	if relPath == "" {
		return nil
	}

	absPath := m.GetProfileAbsPath(relPath)
	var removeErr error
	if err := os.Remove(absPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		removeErr = err
	}

	m.RemoveProfile(relPath)
	return removeErr
}

func (m *Manager) MoveProfile(relPath string, offset int) bool {
	return m.TryUpdate(func(cfg *domain.TrayConfig) bool {
		for i, p := range cfg.Profiles.Items {
			if p.Path == relPath {
				targetIdx := i + offset
				if targetIdx >= 0 && targetIdx < len(cfg.Profiles.Items) {
					cfg.Profiles.Items[i], cfg.Profiles.Items[targetIdx] = cfg.Profiles.Items[targetIdx], cfg.Profiles.Items[i]
					return true
				}
				return false
			}
		}
		return false
	})
}

func (m *Manager) ValidatePhysicalFile(relPath string) error {
	cleanPath := strings.TrimSpace(relPath)
	if cleanPath == "" {
		return ErrProfilePathEmpty
	}

	fileName := filepath.Base(cleanPath)
	absPath := m.GetProfileAbsPath(cleanPath)

	fi, err := os.Stat(absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s: %w", fileName, ErrProfileNotFound)
		}
		return err
	}
	if fi.Size() == 0 {
		return fmt.Errorf("%s: %w", fileName, ErrProfileEmpty)
	}
	return nil
}

func resolveUniqueProfileRelPath(profilesDirAbs, baseName string) string {
	candidate := baseName
	for i := 1; ; i++ {
		target := filepath.Join(profilesDirAbs, candidate+profileFileExt)
		if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
			break
		}
		candidate = fmt.Sprintf("%s_%d", baseName, i)
	}
	return filepath.ToSlash(filepath.Join(domain.ProfilesDir, candidate+profileFileExt))
}
