package fs

import (
	"os"
	"path/filepath"
)

func WriteAtomic(targetPath string, content []byte) error {
	targetDir := filepath.Dir(targetPath)
	_ = os.MkdirAll(targetDir, 0755)
	
	tmpFile, err := os.CreateTemp(targetDir, "tmp_*.tmp")
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

	if _, err := tmpFile.Write(content); err != nil { return err }
	if err := tmpFile.Sync(); err != nil { return err }
	if err := tmpFile.Close(); err != nil { return err }

	cleaned = true
	return os.Rename(tmpName, targetPath)
}
