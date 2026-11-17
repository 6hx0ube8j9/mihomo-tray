//go:build windows

package sys

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

var (
	ErrInvalidPath         = errors.New("无效路径")
	ErrSystemProtectedDir  = errors.New("系统保护目录")
	ErrOtherUserProfileDir = errors.New("其他用户目录")
	ErrDirPermissionDenied = errors.New("无写入权限")
)

func ValidateDirWritable(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return ErrInvalidPath
	}

	cleanDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidPath, dir)
	}

	if IsOtherUserProfileDir(cleanDir) {
		return fmt.Errorf("%w: %s", ErrOtherUserProfileDir, cleanDir)
	}

	if IsSystemProtectedDir(cleanDir) {
		return fmt.Errorf("%w: %s", ErrSystemProtectedDir, cleanDir)
	}

	if err := ProbeDirWritableZeroIO(cleanDir); err != nil {
		return fmt.Errorf("%w: %s (%v)", ErrDirPermissionDenied, cleanDir, err)
	}

	return nil
}

func ProbeDirWritableZeroIO(dir string) error {
	targetDir := dir
	for {
		if _, err := os.Stat(targetDir); err == nil {
			break
		}
		parent := filepath.Dir(targetDir)
		if parent == targetDir {
			break
		}
		targetDir = parent
	}

	pUtf16, err := windows.UTF16PtrFromString(targetDir)
	if err != nil {
		return err
	}

	handle, err := windows.CreateFile(
		pUtf16,
		windows.FILE_WRITE_DATA|windows.FILE_APPEND_DATA,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return err
	}

	_ = windows.CloseHandle(handle)
	return nil
}

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
	cleanDir, err := filepath.Abs(dir)
	if err != nil {
		cleanDir = filepath.Clean(dir)
	}

	vol := filepath.VolumeName(cleanDir)
	if vol == "" {
		vol = os.Getenv("SystemDrive")
		if vol == "" {
			vol = "C:"
		}
	}

	driveRoot := vol + string(filepath.Separator)

	trimmed := strings.TrimRight(cleanDir, `/\.`)
	if strings.EqualFold(trimmed, strings.TrimRight(vol, `/\.`)) {
		return true
	}

	protectedRoots := []string{
		os.Getenv("SystemRoot"),
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("ProgramW6432"),
		filepath.Join(driveRoot, "PerfLogs"),
		filepath.Join(driveRoot, "Recovery"),
		filepath.Join(driveRoot, "$Recycle.Bin"),
		filepath.Join(driveRoot, "System Volume Information"),
		filepath.Join(driveRoot, "WindowsApps"),
		filepath.Join(driveRoot, "WpSystem"),
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

	cleanDir, err := filepath.Abs(dir)
	if err != nil {
		cleanDir = filepath.Clean(dir)
	}

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
