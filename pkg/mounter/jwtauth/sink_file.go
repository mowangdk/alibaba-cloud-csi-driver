package jwtauth

import (
	"fmt"
	"os"
	"path/filepath"

	mounterutils "github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/mounter/utils"
)

// CredentialSink delivers a freshly fetched STS credential to the consumer of
// a mount. Apply is called for the initial credential and again on every
// rotation; Cleanup is called once after the Refresher has stopped.
type CredentialSink interface {
	Apply(cred *STSToken) error
	Cleanup()
}

// DataDirName is the sub-directory (managed via atomic symlink swap) inside
// the per-mount output directory that actually holds the STS files. Using a
// nested "sts" dir mirrors OssfsSecretInterceptor and ensures the symlink
// path never collides with a pre-existing regular directory.
const DataDirName = "sts"

// FileSink writes the credential as a set of files rotated atomically via a
// directory symlink swap, for FUSE clients that read AK/SK/token files from
// disk. All four files (AccessKeyId, AccessKeySecret, SecurityToken,
// Expiration) become visible together, so readers never observe a torn
// credential set.
type FileSink struct {
	outputDir   string // per-mount base dir; entrypoint reads from here
	dataSymlink string // outputDir/sts symlink swapped atomically on rotation
}

var _ CredentialSink = &FileSink{}

func NewFileSink(outputDir string) *FileSink {
	return &FileSink{
		outputDir:   outputDir,
		dataSymlink: filepath.Join(outputDir, DataDirName),
	}
}

// Dir returns the directory the FUSE entrypoint should read credential files
// from. It is the symlink that is swapped atomically on each rotation.
func (s *FileSink) Dir() string {
	return s.dataSymlink
}

func (s *FileSink) Apply(cred *STSToken) error {
	if err := os.MkdirAll(s.outputDir, 0o700); err != nil {
		return fmt.Errorf("create credential dir: %w", err)
	}
	secrets := map[string]string{
		mounterutils.KeyAccessKeyId:     cred.AccessKeyID,
		mounterutils.KeyAccessKeySecret: cred.AccessKeySecret,
		mounterutils.KeySecurityToken:   cred.SecurityToken,
		mounterutils.KeyExpiration:      cred.Expiration,
	}
	if _, err := mounterutils.RotateTokenFiles(s.dataSymlink, secrets); err != nil {
		return fmt.Errorf("rotate credential files: %w", err)
	}
	return nil
}

// Cleanup removes the credential files and directories. It should be called
// once the consuming mount process has exited.
func (s *FileSink) Cleanup() {
	mounterutils.CleanupTokenFiles(s.dataSymlink)
	mounterutils.RemoveIgnoreNotExist(s.outputDir)
}
