package config

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"mihomo-tray/internal/domain"
)

var ErrProfileLimitExceeded = fmt.Errorf("配置数量已达系统上限 (%d 个)，请先清理不需要的配置", domain.MaxProfileCount)

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

func TruncateMiddle(name string) string {
	r := []rune(name)
	if len(r) <= 14 {
		return name
	}
	return string(r[:8]) + "..." + string(r[len(r)-6:])
}

func (m *Manager) SafeCopyUntrustedConfig(srcPath string) (string, bool, error) {
	absSrc, err := filepath.EvalSymlinks(srcPath)
	if err != nil {
		if absSrc, err = filepath.Abs(srcPath); err != nil {
			return "", false, fmt.Errorf("无法解析目标文件路径: %w", err)
		}
	}

	if relPath, isTree := IsInAppTree(m.baseDir, absSrc); isTree {
		if filepath.Dir(filepath.ToSlash(relPath)) == ProfilesDir {
			return relPath, false, nil
		}
	}

	profilesDirAbs := filepath.Join(m.baseDir, ProfilesDir)
	if err := os.MkdirAll(profilesDirAbs, 0755); err != nil {
		return "", false, fmt.Errorf("无法创建配置存放目录，请检查系统权限: %w", err)
	}

	baseName := strings.TrimSuffix(filepath.Base(absSrc), filepath.Ext(absSrc))
	finalRelPath := resolveUniqueProfileRelPath(profilesDirAbs, baseName)
	dstAbs := filepath.Join(m.baseDir, filepath.FromSlash(finalRelPath))

	if err := copyFileWithLimit(absSrc, dstAbs, domain.MaxProfileBytes); err != nil {
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
	return filepath.Join(m.baseDir, filepath.FromSlash(m.data.Profiles.Active))
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

func (m *Manager) MoveProfile(relPath string, offset int) bool {
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
	absPath := filepath.Join(m.baseDir, filepath.FromSlash(relPath))
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
	return filepath.ToSlash(filepath.Join(ProfilesDir, candidate+".yaml"))
}

func copyFileWithLimit(srcPath, dstPath string, maxBytes int64) error {
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("源文件已被删除或无读取权限: %w", err)
	}
	defer srcFile.Close()

	targetDir := filepath.Dir(dstPath)
	_ = os.MkdirAll(targetDir, 0755)

	tmpFile, err := os.CreateTemp(targetDir, "profile.*.tmp")
	if err != nil {
		return fmt.Errorf("无法在系统目录创建缓存文件: %w", err)
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
		return fmt.Errorf("数据传输过程中发生异常: %w", err)
	}

	var extra [1]byte
	if n, _ := srcFile.Read(extra[:]); n > 0 {
		return fmt.Errorf("配置文件体积超出上限 (最大允许 %d MB)", maxBytes/(1024*1024))
	}

	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("文件落盘失败: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("文件系统占有释放失败: %w", err)
	}
	cleaned = true

	if err := os.Rename(tmpName, dstPath); err != nil {
		return fmt.Errorf("最终配置文件生成失败: %w", err)
	}
	return nil
}
