package fs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var ErrSizeLimitExceeded = errors.New("文件体积超出系统限制")

func SaveTempWithLimit(dir, pattern string, r io.Reader, maxBytes int64) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpPath := tmp.Name()

	cleanup := true
	defer func() {
		if cleanup {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()
  
	n, err := io.Copy(tmp, io.LimitReader(r, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("数据传输写入失败: %w", err)
	}
	if n > maxBytes {
		return "", fmt.Errorf("%w (最大允许 %d MB)", ErrSizeLimitExceeded, maxBytes/(1024*1024))
	}

	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("缓存刷盘失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("释放文件句柄失败: %w", err)
	}

	cleanup = false
	return tmpPath, nil
}

func CopyFileWithLimit(srcPath, dstPath string, maxBytes int64) error {
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("无法读取源文件: %w", err)
	}
	defer srcFile.Close()

	fi, err := srcFile.Stat()
	if err != nil || fi.Size() == 0 {
		return errors.New("源文件内容为空或无法访问")
	}

	targetDir := filepath.Dir(dstPath)
	tmpPath, err := SaveTempWithLimit(targetDir, ".copy_*.tmp", srcFile, maxBytes)
	if err != nil {
		return err
	}

	if err := ReplaceAtomic(tmpPath, dstPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("原子覆盖目标文件失败: %w", err)
	}
	return nil
}
