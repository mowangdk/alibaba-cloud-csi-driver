package interceptors

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetTempPasswdFilePath(t *testing.T) {
	tests := []struct {
		name      string
		parentDir string
		baseName  string
		timestamp int64
		want      string
	}{
		{
			name:      "simple case",
			parentDir: "/tmp",
			baseName:  "passwd",
			timestamp: 1234567890,
			want:      "/tmp/.passwd.tmp.1234567890",
		},
		{
			name:      "with subdirectory",
			parentDir: "/tmp/dir",
			baseName:  "file",
			timestamp: 9876543210,
			want:      "/tmp/dir/.file.tmp.9876543210",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getTempPasswdFilePath(tt.parentDir, tt.baseName, tt.timestamp)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetTempPasswdFilePathWithTimestamp(t *testing.T) {
	parentDir := "/tmp"
	baseName := "passwd"
	got := getTempPasswdFilePathWithTimestamp(parentDir, baseName)

	// Should contain the pattern
	assert.Contains(t, got, parentDir)
	assert.Contains(t, got, baseName)
	assert.Contains(t, got, ".tmp.")

	// Should have a timestamp (numeric suffix)
	require.Greater(t, len(got), len(parentDir)+len(baseName)+5, "should have timestamp suffix")
}

func TestGetTempPasswdFilePattern(t *testing.T) {
	tests := []struct {
		name     string
		baseName string
		want     string
	}{
		{
			name:     "simple case",
			baseName: "passwd",
			want:     ".passwd.tmp.",
		},
		{
			name:     "with special chars",
			baseName: "file-name",
			want:     ".file-name.tmp.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getTempPasswdFilePattern(tt.baseName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCleanupExpiredTempFiles(t *testing.T) {
	t.Run("cleanup matching files", func(t *testing.T) {
		tmpDir := t.TempDir()
		parentDir := filepath.Join(tmpDir, "parent")
		err := os.MkdirAll(parentDir, 0o755)
		require.NoError(t, err)

		// Create matching files
		file1 := filepath.Join(parentDir, ".passwd.tmp.123456")
		file2 := filepath.Join(parentDir, ".passwd.tmp.789012")
		err = os.WriteFile(file1, []byte("test1"), 0o600)
		require.NoError(t, err)
		err = os.WriteFile(file2, []byte("test2"), 0o600)
		require.NoError(t, err)

		// Create non-matching file
		otherFile := filepath.Join(parentDir, ".other.tmp.123456")
		err = os.WriteFile(otherFile, []byte("test"), 0o600)
		require.NoError(t, err)

		// Cleanup
		pattern := getTempPasswdFilePattern("passwd")
		cleanupExpiredTempFiles(parentDir, pattern)

		// Verify matching files are removed
		assert.NoFileExists(t, file1)
		assert.NoFileExists(t, file2)

		// Verify non-matching file still exists
		assert.FileExists(t, otherFile)
	})

	t.Run("cleanup with non-existent parent - should not error", func(t *testing.T) {
		tmpDir := t.TempDir()
		parentDir := filepath.Join(tmpDir, "nonexistent")

		pattern := getTempPasswdFilePattern("passwd")
		cleanupExpiredTempFiles(parentDir, pattern)
		// Should not panic
	})
}

func TestCleanupPasswdFile(t *testing.T) {
	t.Run("cleanup passwd file and temporary files", func(t *testing.T) {
		tmpDir := t.TempDir()
		parentDir := filepath.Join(tmpDir, "parent")
		err := os.MkdirAll(parentDir, 0o755)
		require.NoError(t, err)

		// Create passwd file
		passwdFile := filepath.Join(parentDir, "passwd")
		err = os.WriteFile(passwdFile, []byte("test"), 0o600)
		require.NoError(t, err)

		// Create temporary files
		tmpFile1 := filepath.Join(parentDir, ".passwd.tmp.123456")
		tmpFile2 := filepath.Join(parentDir, ".passwd.tmp.789012")
		err = os.WriteFile(tmpFile1, []byte("test1"), 0o600)
		require.NoError(t, err)
		err = os.WriteFile(tmpFile2, []byte("test2"), 0o600)
		require.NoError(t, err)

		// Create non-matching file
		otherFile := filepath.Join(parentDir, ".other.tmp.123456")
		err = os.WriteFile(otherFile, []byte("test"), 0o600)
		require.NoError(t, err)

		// Cleanup
		cleanupPasswdFile(passwdFile)

		// Verify passwd file is removed
		assert.NoFileExists(t, passwdFile)

		// Verify temporary files are removed
		assert.NoFileExists(t, tmpFile1)
		assert.NoFileExists(t, tmpFile2)

		// Verify non-matching file still exists
		assert.FileExists(t, otherFile)
	})

	t.Run("cleanup empty passwd file - should not error", func(t *testing.T) {
		cleanupPasswdFile("")
		// Should not panic
	})

	t.Run("cleanup non-existent passwd file - should not error", func(t *testing.T) {
		tmpDir := t.TempDir()
		passwdFile := filepath.Join(tmpDir, "nonexistent")

		cleanupPasswdFile(passwdFile)
		// Should not panic
	})
}
