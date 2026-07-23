package interceptors

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/mounter"
	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/mounter/proxy/server"
	mounterutils "github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/mounter/utils"
	"k8s.io/klog/v2"
	mountutils "k8s.io/mount-utils"
)

var _ mounter.MountInterceptor = OssfsSecretInterceptor

// rawMounter is a shared mount.Interface instance for checking mount points.
// It's safe to reuse because mount.Interface implementations are stateless.
// We use a separate instance from ossfs_monitor.go to avoid import cycles,
// but both are created the same way and can be used interchangeably.
var rawMounter = mountutils.NewWithoutSystemd("")

func OssfsSecretInterceptor(ctx context.Context, op *mounter.MountOperation, handler mounter.MountHandler) error {
	return ossfsSecretInterceptor(ctx, op, handler, mounterutils.OssFsType)
}

func Ossfs2SecretInterceptor(ctx context.Context, op *mounter.MountOperation, handler mounter.MountHandler) error {
	return ossfsSecretInterceptor(ctx, op, handler, mounterutils.OssFs2Type)
}

func ossfsSecretInterceptor(ctx context.Context, op *mounter.MountOperation, handler mounter.MountHandler, fuseType string) error {
	return ossfsSecretInterceptorWithMounter(ctx, op, handler, fuseType, rawMounter)
}

// ossfsSecretInterceptorWithMounter is the internal implementation that accepts a mounter parameter.
// This allows tests to inject a fake mounter to simulate different mount point states.
func ossfsSecretInterceptorWithMounter(ctx context.Context, op *mounter.MountOperation, handler mounter.MountHandler, fuseType string, mountInterface mountutils.Interface) error {
	if op == nil || op.Secrets == nil {
		return handler(ctx, op)
	}

	passwdFile, tokenDir, err := prepareCredentialFiles(fuseType, op.Target, op.Secrets)
	if err != nil {
		return fmt.Errorf("prepare credential files failed: %w", err)
	}

	// Check if mount point already exists for token rotation scenario.
	// In token rotation (republish), we only need to update the token files,
	// and the running ossfs client will automatically reload the new token.
	// We skip the mount operation to avoid creating duplicate mount points.
	//
	// Note: IsNotMountPoint handles path not existing by creating the directory
	// and returning (true, nil). If it returns an error, it's a real error
	// (e.g., permission denied, mkdir failed, unmount failed) that should be returned.
	// Only check mount point if mountInterface is available (not nil).
	if mountInterface != nil {
		notMnt, err := mounterutils.IsNotMountPoint(mountInterface, op.Target)
		if err != nil {
			return fmt.Errorf("failed to check if target %s is a mountpoint: %w", op.Target, err)
		}
		if !notMnt {
			// Mount point already exists, this is a token rotation scenario.
			// Token files have already been updated by rotateTokenFiles above.
			// Skip mount operation and let the existing ossfs client reload the new token.
			klog.V(4).InfoS("mount point already exists, skipping mount for token rotation", "target", op.Target)
			return mounter.ErrSkipMount
		}
	}

	if passwdFile != "" {
		klog.V(4).InfoS("created ossfs passwd file", "path", passwdFile)
		if fuseType == mounterutils.OssFsType {
			op.Options = append(op.Options, "passwd_file="+passwdFile)
		} else {
			// ossfs2
			op.Args = append(op.Args, []string{"-c", passwdFile}...)
		}
	}
	if tokenDir != "" {
		klog.V(4).InfoS("created ossfs token directory", "dir", tokenDir)
		if fuseType == mounterutils.OssFsType {
			op.Options = append(op.Options, "passwd_file="+tokenDir)
		} else {
			// ossfs2
			// For ossfs2, file-path is a common option configuration after -o, so append to op.Options
			op.Options = append(op.Options,
				fmt.Sprintf("oss_sts_multi_conf_ak_file=%s", mounterutils.TokenFilePath(tokenDir, mounterutils.KeyAccessKeyId)),
				fmt.Sprintf("oss_sts_multi_conf_sk_file=%s", mounterutils.TokenFilePath(tokenDir, mounterutils.KeyAccessKeySecret)),
				fmt.Sprintf("oss_sts_multi_conf_token_file=%s", mounterutils.TokenFilePath(tokenDir, mounterutils.KeySecurityToken)),
			)
		}
	}

	if err = handler(ctx, op); err != nil {
		return err
	}

	if (passwdFile == "" && tokenDir == "") || op.MountResult == nil {
		return nil
	}
	result, ok := op.MountResult.(server.OssfsMountResult)
	if !ok {
		klog.ErrorS(
			errors.New("failed to assert ossfs mount result"),
			"skipping cleanup of passwd file", "mountpoint", op.Target, "path", passwdFile,
		)
		return nil
	}

	go func() {
		<-result.ExitChan
		// Clean up passwd file and all temporary files
		cleanupPasswdFile(passwdFile)
		// Clean up token directory (symlink, actual directory, and any temporary files)
		mounterutils.CleanupTokenFiles(tokenDir)
	}()
	return nil
}

// rotatePasswdFile rotates (or initializes) a passwd file atomically.
// It first checks if the file exists and has the same content to avoid redundant writes.
// If content is different or file doesn't exist, it writes to a temporary file
// and atomically renames it to the target file. This avoids the need for locks
// and ensures atomic updates even with concurrent writes.
func rotatePasswdFile(path string, data []byte, perm os.FileMode) (done bool, err error) {
	// First check if file exists and has the same content
	exists, sameContent, err := mounterutils.CheckFileContent(path, data)
	if err != nil {
		return false, err
	}
	if exists && sameContent {
		return false, nil
	}

	// Content is different or file doesn't exist, need to write
	// Create temporary file in the same directory
	parentDir, baseName := filepath.Dir(path), filepath.Base(path)
	tmpFile := getTempPasswdFilePathWithTimestamp(parentDir, baseName)

	// Use defer to ensure temporary file is cleaned up on any error or panic
	defer mounterutils.RemoveIgnoreNotExist(tmpFile)

	// Write to temporary file
	if err = os.WriteFile(tmpFile, data, perm); err != nil {
		return false, fmt.Errorf("failed to write temporary file: %w", err)
	}

	// Atomically rename temporary file to target file
	// This is atomic on most filesystems and handles concurrent writes gracefully
	if err = os.Rename(tmpFile, path); err != nil {
		return false, fmt.Errorf("failed to atomically replace file: %w", err)
	}

	return true, nil
}

// prepareCredentialFiles returns:
//  1. file:    path of ossfs credential file for fixed AKSK
//  2. dir:     directory of ossfs credential files for token
//  4. error
func prepareCredentialFiles(fuseType, target string, secrets map[string]string) (file, dir string, err error) {
	// fixed credentials
	hashDir := mounterutils.GetPasswdHashDir(target)

	if passwd := secrets[mounterutils.GetPasswdFileName(fuseType)]; passwd != "" {
		err = os.MkdirAll(hashDir, 0o755)
		if err != nil {
			klog.Errorf("mkdirall hashdir failed %v", err)
			return
		}
		filePath := filepath.Join(hashDir, mounterutils.GetPasswdFileName(fuseType))
		_, err = rotatePasswdFile(filePath, []byte(passwd), 0o600)
		if err != nil {
			return
		}
		file = filePath
		return
	}

	// token
	if token := secrets[mounterutils.KeySecurityToken]; token != "" {
		tokenDir := filepath.Join(hashDir, mounterutils.GetPasswdFileName(fuseType))
		err = os.MkdirAll(tokenDir, 0o755)
		if err != nil {
			klog.Errorf("mkdirall tokenDir failed %v", err)
			return
		}
		// Use tokenDir/sts as the symlink path to ensure it doesn't exist on first call
		// This avoids the need to handle directory-to-symlink conversion
		stsDir := filepath.Join(tokenDir, "sts")
		_, err = mounterutils.RotateTokenFiles(stsDir, secrets)
		if err != nil {
			return
		}
		dir = stsDir
		return
	}
	return
}
