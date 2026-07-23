package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/klog/v2"
)

// getParentDirAndBaseName splits a path into its parent directory and base name.
func getParentDirAndBaseName(path string) (parentDir, baseName string) {
	return filepath.Dir(path), filepath.Base(path)
}

// resolveSymlinkTarget resolves a symlink target to an absolute path.
// If linkTarget is already absolute, it returns it as is.
// Otherwise, it joins it with the parent directory of symlinkPath.
func resolveSymlinkTarget(symlinkPath, linkTarget string) string {
	if filepath.IsAbs(linkTarget) {
		return linkTarget
	}
	return filepath.Join(filepath.Dir(symlinkPath), linkTarget)
}

// getTempSymlinkPath returns the path for a temporary symlink used during atomic rotation.
// Format: <parentDir>/.<baseName>.tmp
func getTempSymlinkPath(parentDir, baseName string) string {
	return filepath.Join(parentDir, fmt.Sprintf(".%s.tmp", baseName))
}

// getTempDataDirPath returns the path for a temporary data directory used during token rotation.
// Format: <parentDir>/.<baseName>.tmp_<timestamp>
func getTempDataDirPath(parentDir, baseName string, timestamp int64) string {
	return filepath.Join(parentDir, fmt.Sprintf(".%s.tmp_%d", baseName, timestamp))
}

// getTempDataDirPathWithTimestamp returns the path for a temporary data directory with current timestamp.
func getTempDataDirPathWithTimestamp(parentDir, baseName string) string {
	return getTempDataDirPath(parentDir, baseName, time.Now().UnixNano())
}

// getTempDirPattern returns the pattern prefix for matching temporary data directories.
// Format: .<baseName>.tmp_
func getTempDirPattern(baseName string) string {
	return fmt.Sprintf(".%s.tmp_", baseName)
}

// TokenFilePath returns the full path to a token file within a data directory.
func TokenFilePath(dataDir, key string) string {
	return filepath.Join(dataDir, key)
}

// getRelativeDirName returns the base name of a directory path, used for creating relative symlinks.
func getRelativeDirName(dirPath string) string {
	return filepath.Base(dirPath)
}

// getTokenKeys returns the list of token keys in the order they should be processed.
//
// TODO: In ossfs and ossfs2, the client checks if AccessKeyId has changed to determine
// whether to update credential information. Therefore, AccessKeyId must be rotated last
// to ensure atomic updates - all other files are updated before AccessKeyId, so clients
// will see either all old files or all new files, never a mixed state.
func getTokenKeys() []string {
	return []string{KeyAccessKeySecret, KeySecurityToken, KeyExpiration, KeyAccessKeyId}
}

// RemoveIgnoreNotExist removes a file or directory, ignoring "not exist" errors.
// Other errors are logged but not returned. This is useful for cleanup operations
// where the file may or may not exist.
func RemoveIgnoreNotExist(path string) {
	if err := os.Remove(path); err != nil {
		if !os.IsNotExist(err) {
			klog.V(4).Infof("failed to remove %s: %v", path, err)
		}
	}
}

// CheckFileContent checks if a file exists and has the same content as the given data.
// Returns (exists, sameContent, error).
func CheckFileContent(path string, data []byte) (exists bool, sameContent bool, err error) {
	existingData, readErr := os.ReadFile(path)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return false, false, nil
		}
		return false, false, readErr
	}
	return true, string(existingData) == string(data), nil
}

// cleanupTokenDirectory removes all token files from the given directory and then tries to remove the directory itself.
// This is used for cleaning up temporary directories on error or old data directories after rotation.
func cleanupTokenDirectory(dir string, tokenKeys []string) {
	for _, key := range tokenKeys {
		filePath := TokenFilePath(dir, key)
		RemoveIgnoreNotExist(filePath)
	}
	// Try to remove the directory if it's empty
	RemoveIgnoreNotExist(dir)
}

// CleanupTokenFiles cleans up the token directory symlink and all associated files/directories.
// It resolves the symlink to get the actual data directory, cleans up token files in that directory,
// removes the symlink itself, and also cleans up any temporary symlinks or old directories.
// Since this is called when the client has exited, all files should be cleaned up.
func CleanupTokenFiles(tokenDir string) {
	if tokenDir == "" {
		return
	}

	parentDir, baseName := getParentDirAndBaseName(tokenDir)
	tokenKeys := getTokenKeys()

	// Clean up the actual data directory if symlink exists
	if linkTarget, err := os.Readlink(tokenDir); err == nil {
		// tokenDir is a symlink, resolve to actual directory
		actualDataDir := resolveSymlinkTarget(tokenDir, linkTarget)
		// Clean up token files in the actual directory
		cleanupTokenDirectory(actualDataDir, tokenKeys)
	}

	// Remove the symlink itself
	RemoveIgnoreNotExist(tokenDir)

	// Clean up temporary symlink if it exists
	tmpLinkPath := getTempSymlinkPath(parentDir, baseName)
	RemoveIgnoreNotExist(tmpLinkPath)

	// Clean up any old temporary directories matching the pattern returned by getTempDirPattern
	// This handles cases where old directories weren't cleaned up properly
	cleanupExpiredTempDirectories(parentDir, getTempDirPattern(baseName), tokenKeys)
}

// cleanupExpiredTempDirectories cleans up all directories matching the given pattern in the parent directory.
func cleanupExpiredTempDirectories(parentDir, pattern string, tokenKeys []string) {
	entries, err := os.ReadDir(parentDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), pattern) {
			oldDir := filepath.Join(parentDir, entry.Name())
			cleanupTokenDirectory(oldDir, tokenKeys)
		}
	}
}

// RotateTokenFiles rotates (or initializes) token files using directory-level symlink for atomic updates.
// This function uses a simple symlink-based approach similar to Kubernetes secret volume plugin:
// 1. Create a temporary data directory using getTempDataDirPathWithTimestamp
// 2. Write all token files to the temporary directory
// 3. Atomically switch the dir symlink to point to the new directory using getTempSymlinkPath
// This ensures all files are updated atomically - readers either see all old files or all new files.
// Clients that have already opened file handles will continue to read from the old directory
// until they close and reopen, ensuring no interruption during rotation.
func RotateTokenFiles(dir string, secrets map[string]string) (rotated bool, err error) {
	// Currently, for ossfs2, expiration is not required.
	// But we still manage it (if offered) for the feature.
	tokenKeys := getTokenKeys()
	// Check nil value in advanced.
	for _, key := range tokenKeys {
		val := secrets[key]
		if val == "" {
			if key == KeyExpiration {
				continue
			}
			err = fmt.Errorf("invalid authorization. %s is empty", key)
			klog.Error(err)
			// Return false for rotated when error occurs
			return false, err
		}
	}

	// Check if any file needs update before writing
	// Read current data directory if symlink exists
	currentDataDir := ""
	linkTarget, err := os.Readlink(dir)
	if err == nil {
		// dir is a symlink, resolve to actual directory
		currentDataDir = resolveSymlinkTarget(dir, linkTarget)
	} else if os.IsNotExist(err) {
		// dir doesn't exist, this is the first call
		// currentDataDir remains empty
	} else {
		// Error reading symlink - check if dir is a regular directory
		// If it's a regular directory, we cannot safely convert it to symlink because
		// rename would change the inode, breaking file handles that clients may have opened.
		// We must return an error to prevent data inconsistency.
		fileInfo, statErr := os.Stat(dir)
		if statErr == nil && fileInfo.IsDir() {
			// dir is a regular directory, cannot safely rotate
			return false, fmt.Errorf("cannot rotate token files: %s is a regular directory, not a symlink. ", dir)
		}
		// Some other error (permission denied, etc.) - cannot proceed with rotation
		return false, fmt.Errorf("failed to read symlink %s: %w. Cannot proceed with token rotation", dir, err)
	}

	anyNeedsUpdate := false
	if currentDataDir == "" {
		// No existing data directory, need to create
		anyNeedsUpdate = true
	} else {
		// Check if any file content changed
		for _, key := range tokenKeys {
			val := secrets[key]
			if val == "" {
				continue
			}
			filePath := TokenFilePath(currentDataDir, key)
			exists, sameContent, checkErr := CheckFileContent(filePath, []byte(val))
			if checkErr != nil || !exists || !sameContent {
				// File doesn't exist, error reading, or content is different, needs update
				anyNeedsUpdate = true
				break
			}
		}
	}

	// If no files need update, return early
	if !anyNeedsUpdate {
		return false, nil
	}

	// Create temporary data directory with timestamp suffix in the parent directory
	// This ensures the symlink target is in the same filesystem for atomic rename
	parentDir, baseName := getParentDirAndBaseName(dir)
	tmpDataDir := getTempDataDirPathWithTimestamp(parentDir, baseName)
	if err = os.MkdirAll(tmpDataDir, 0o755); err != nil {
		return false, fmt.Errorf("failed to create temporary data directory: %w", err)
	}

	// Use defer to ensure temporary directory is cleaned up on any error or panic
	defer func() {
		if !rotated {
			// Clean up temporary directory on error
			cleanupTokenDirectory(tmpDataDir, tokenKeys)
		}
	}()

	// Write all token files to temporary directory
	for _, key := range tokenKeys {
		val := secrets[key]
		if val == "" {
			continue
		}
		filePath := TokenFilePath(tmpDataDir, key)
		if err = os.WriteFile(filePath, []byte(val), 0o600); err != nil {
			return false, fmt.Errorf("failed to write token file %s: %w", key, err)
		}
	}

	// Atomically switch the dir symlink
	// First, create a temporary symlink pointing to the new directory
	tmpLinkPath := getTempSymlinkPath(parentDir, baseName)
	relativeDataDir := getRelativeDirName(tmpDataDir)
	if err = os.Symlink(relativeDataDir, tmpLinkPath); err != nil {
		return false, fmt.Errorf("failed to create temporary symlink: %w", err)
	}

	// Use defer to ensure temporary symlink is cleaned up on any error or panic
	// After successful rename, the symlink will be renamed to dir, so RemoveIgnoreNotExist will safely ignore it
	defer RemoveIgnoreNotExist(tmpLinkPath)

	// Atomically rename the temporary symlink to dir
	// Note: dir should not exist on first call, so this will create the symlink
	// On subsequent calls, this will atomically replace the existing symlink
	// This is atomic on most filesystems and ensures all readers see either all old files or all new files
	// IMPORTANT: Once this rename completes, all new opens of dir/ will resolve to the new directory.
	// However, clients that have already opened file handles will continue to read from the old directory
	// (pointed to by their open file descriptors), ensuring no interruption during rotation.
	if err = os.Rename(tmpLinkPath, dir); err != nil {
		return false, fmt.Errorf("failed to atomically switch symlink: %w", err)
	}

	// Mark as rotated so defer won't clean up the new directory
	rotated = true

	// Clean up old data directory if it exists and is different from the new one
	// Only remove the token files we know about, not the entire directory
	if currentDataDir != "" && currentDataDir != tmpDataDir {
		cleanupTokenDirectory(currentDataDir, tokenKeys)
	}

	return true, nil
}
