package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func WriteAtomic(targetPath string, content []byte) error {
	targetDir := filepath.Dir(targetPath)
	_ = os.MkdirAll(targetDir, 0755)

	tmpFile, err := os.CreateTemp(targetDir, ".tmp_*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时落盘文件失败: %w", err)
	}
	tmpName := tmpFile.Name()

	var writeSucceeded bool
	defer func() {
		if !writeSucceeded {
			_ = tmpFile.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmpFile.Write(content); err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("文件刷盘失败: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("关闭临时文件句柄失败: %w", err)
	}

	writeSucceeded = true
	return ReplaceAtomic(tmpName, targetPath)
}

func ReplaceAtomic(sourcePath, targetPath string) error {
	var lastErr error
	for i := 0; i < 3; i++ {
		err := os.Rename(sourcePath, targetPath)
		if err == nil {
			return nil
		}
		lastErr = err

		_ = os.Remove(targetPath)
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("原子替换文件失败 (已重试 3 次): %w", lastErr)
}
