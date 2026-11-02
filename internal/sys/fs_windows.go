//go:build windows

package sys

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func IsSubOrSameDir(parent, target string) bool {
	if parent == "" || target == "" {
		return false
	}
	p := strings.ToLower(filepath.Clean(parent))
	t := strings.ToLower(filepath.Clean(target))

	rel, err := filepath.Rel(p, t)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func IsSystemProtectedDir(dir string) bool {
	cleanDir := filepath.Clean(dir)
	sysDrive := os.Getenv("SystemDrive")
	if sysDrive == "" {
		sysDrive = "C:"
	}

	trimmed := strings.TrimRight(cleanDir, `\.`)
	if strings.EqualFold(trimmed, sysDrive) {
		return true
	}

	protectedRoots := []string{
		os.Getenv("SystemRoot"), 
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"), 
		os.Getenv("ProgramW6432"),                            
		filepath.Join(sysDrive, "PerfLogs"), 
		filepath.Join(sysDrive, "Recovery"), 
		filepath.Join(sysDrive, "$Recycle.Bin"), 
		filepath.Join(sysDrive, "System Volume Information"),
	}

	for _, root := range protectedRoots {
		if root != "" && IsSubOrSameDir(root, cleanDir) {
			return true
		}
	}
	return false
}

func IsOtherUserProfileDir(dir string) bool {
	currentUserProfile := os.Getenv("USERPROFILE")
	if currentUserProfile == "" {
		return false
	}

	cleanDir := filepath.Clean(dir)
	usersRoot := filepath.Dir(currentUserProfile)

	if !IsSubOrSameDir(usersRoot, cleanDir) {
		return false
	}

	if IsSubOrSameDir(currentUserProfile, cleanDir) {
		return false
	}
	publicDir := os.Getenv("PUBLIC")
	if publicDir != "" && IsSubOrSameDir(publicDir, cleanDir) {
		return false
	}

	return true
}

func IsPermissionError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, os.ErrPermission) {
		return true
	}

	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case windows.ERROR_ACCESS_DENIED,
			windows.ERROR_WRITE_PROTECT,
			windows.ERROR_SHARING_VIOLATION,
			windows.ERROR_CANNOT_MAKE,
			windows.ERROR_PRIVILEGE_NOT_HELD:
			return true
		}
	}
	return false
}

func ProbeDirWritable(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	probePath := filepath.Join(dir, fmt.Sprintf(".probe_%d_%d", os.Getpid(), time.Now().UnixNano()))
	probeFile, err := os.OpenFile(probePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	defer func() {
		_ = probeFile.Close()
		_ = os.Remove(probePath)
	}()

	return nil
}
