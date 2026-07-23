package interceptors

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	mounterutils "github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/mounter/utils"
)

// getTempPasswdFilePath returns the path for a temporary passwd file used during atomic rotation.
// Format: <parentDir>/.<baseName>.tmp.<timestamp>
func getTempPasswdFilePath(parentDir, baseName string, timestamp int64) string {
	return filepath.Join(parentDir, fmt.Sprintf(".%s.tmp.%d", baseName, timestamp))
}

// getTempPasswdFilePathWithTimestamp returns the path for a temporary passwd file with current timestamp.
func getTempPasswdFilePathWithTimestamp(parentDir, baseName string) string {
	return getTempPasswdFilePath(parentDir, baseName, time.Now().UnixNano())
}

// getTempPasswdFilePattern returns the pattern prefix for matching temporary passwd files.
// Format: .<baseName>.tmp.
func getTempPasswdFilePattern(baseName string) string {
	return fmt.Sprintf(".%s.tmp.", baseName)
}

// cleanupExpiredTempFiles cleans up all files matching the given pattern in the parent directory.
func cleanupExpiredTempFiles(parentDir, pattern string) {
	entries, err := os.ReadDir(parentDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), pattern) {
			tmpFile := filepath.Join(parentDir, entry.Name())
			mounterutils.RemoveIgnoreNotExist(tmpFile)
		}
	}
}

// cleanupPasswdFile cleans up the passwd file and all temporary files matching the pattern.
func cleanupPasswdFile(passwdFile string) {
	if passwdFile == "" {
		return
	}

	// Remove the passwd file itself
	mounterutils.RemoveIgnoreNotExist(passwdFile)

	// Clean up any temporary files matching the pattern returned by getTempPasswdFilePattern
	parentDir, baseName := filepath.Dir(passwdFile), filepath.Base(passwdFile)
	cleanupExpiredTempFiles(parentDir, getTempPasswdFilePattern(baseName))
}
