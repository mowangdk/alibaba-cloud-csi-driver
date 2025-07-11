/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package utils

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/unix"
	"k8s.io/klog/v2"
)

const (
	// list retry times
	maxListTries = 10
	// Location of the mount file to use
	procMountsPath = "/proc/mounts"
	// Number of fields per line in /proc/mounts as per the fstab man page.
	expectedNumFieldsPerLine = 6
)

// MountPoint represents a single line in /proc/mounts or /etc/fstab.
type MountPoint struct { // nolint: golint
	Device string
	Path   string
	Type   string
	Opts   []string // Opts may contain sensitive mount options (like passwords) and MUST be treated as such (e.g. not logged).
	Freq   int
	Pass   int
}

// Mounter is responsible for formatting and mounting volumes
type Mounter interface {
	// If the block doesn't exist, create it
	EnsureBlock(target string) error
	// Format formats the source with the given filesystem type
	Format(source, fsType string) error

	// Mount mounts source to target with the given fstype and options.
	Mount(source, target, fsType string, options ...string) error

	// Mount mounts source to target for block file.
	MountBlock(source, target string, options ...string) error
	// Unmount unmounts the given target
	Unmount(target string) error

	HasMountRefs(mountPath string, mountRefs []string) bool
}

// TODO(arslan): this is Linux only for now. Refactor this into a package with
// architecture specific code in the future, such as mounter_darwin.go,
// mounter_linux.go, etc..
type mounter struct {
}

// NewMounter returns a new mounter instance
func NewMounter() Mounter {
	return &mounter{}
}

func (m *mounter) EnsureBlock(target string) error {
	fi, err := os.Lstat(target)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && fi.IsDir() {
		os.Remove(target)
	}
	targetPathFile, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, 0750)
	if err != nil {
		klog.Infof("Failed to create block:%s with error: %v", target, err)
		return fmt.Errorf("create block error: %v", err)
	}
	if err := targetPathFile.Close(); err != nil {
		klog.Infof("Failed to close targetPath:%s with error: %v", target, err)
		return fmt.Errorf("close block error: %v", err)
	}
	return nil
}

func (m *mounter) Format(source, fsType string) error {
	mkfsCmd := fmt.Sprintf("mkfs.%s", fsType)

	_, err := exec.LookPath(mkfsCmd)
	if err != nil {
		if err == exec.ErrNotFound {
			return fmt.Errorf("%q executable not found in $PATH", mkfsCmd)
		}
		return err
	}

	mkfsArgs := []string{}
	if fsType == "" {
		return errors.New("fs type is not specified for formatting the volume")
	}
	if source == "" {
		return errors.New("source is not specified for formatting the volume")
	}
	mkfsArgs = append(mkfsArgs, source)
	if fsType == "ext4" || fsType == "ext3" {
		mkfsArgs = []string{"-F", source}
	}

	klog.Infof("Format %s with fsType %s, the command is %s %v", source, fsType, mkfsCmd, mkfsArgs)
	out, err := exec.Command(mkfsCmd, mkfsArgs...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("formatting disk failed: %v cmd: '%s %s' output: %q",
			err, mkfsCmd, strings.Join(mkfsArgs, " "), string(out))
	}

	return nil
}

func (m *mounter) MountBlock(source, target string, opts ...string) error {
	mountCmd := "mount"
	mountArgs := []string{}

	if source == "" {
		return errors.New("source is not specified for mounting the volume")
	}
	if target == "" {
		return errors.New("target is not specified for mounting the volume")
	}

	if len(opts) > 0 {
		mountArgs = append(mountArgs, "-o", strings.Join(opts, ","))
	}
	mountArgs = append(mountArgs, source)
	mountArgs = append(mountArgs, target)
	// create target, os.Mkdirall is noop if it exists
	_, err := os.Create(target)
	if err != nil {
		return err
	}

	klog.Infof("Mount %s to %s, the command is %s %v", source, target, mountCmd, mountArgs)
	out, err := exec.Command(mountCmd, mountArgs...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mounting failed: %v cmd: '%s %s' output: %q",
			err, mountCmd, strings.Join(mountArgs, " "), string(out))
	}
	return nil
}

func (m *mounter) Mount(source, target, fsType string, opts ...string) error {
	mountCmd := "mount"
	mountArgs := []string{}

	if fsType == "" {
		return errors.New("fs type is not specified for mounting the volume")
	}

	if source == "" {
		return errors.New("source is not specified for mounting the volume")
	}

	if target == "" {
		return errors.New("target is not specified for mounting the volume")
	}

	mountArgs = append(mountArgs, "-t", fsType)

	if len(opts) > 0 {
		mountArgs = append(mountArgs, "-o", strings.Join(opts, ","))
	}

	mountArgs = append(mountArgs, source)
	mountArgs = append(mountArgs, target)

	// create target, os.Mkdirall is noop if it exists
	err := os.MkdirAll(target, 0750)
	if err != nil {
		return err
	}

	klog.Infof("Mount %s to %s with fsType %s, the command is %s %v", source, target, fsType, mountCmd, mountArgs)

	out, err := exec.Command(mountCmd, mountArgs...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mounting failed: %v cmd: '%s %s' output: %q",
			err, mountCmd, strings.Join(mountArgs, " "), string(out))
	}

	return nil
}

func (m *mounter) Unmount(target string) error {
	umountCmd := "umount"
	if target == "" {
		return errors.New("target is not specified for unmounting the volume")
	}

	umountArgs := []string{target}

	klog.Infof("Unmount %s, the command is %s %v", target, umountCmd, umountArgs)

	out, err := exec.Command(umountCmd, umountArgs...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("unmounting failed: %v cmd: '%s %s' output: %q",
			err, umountCmd, target, string(out))
	}

	return nil
}

const kubernetesPluginPathPrefix = "/plugins/kubernetes.io/"

func (m *mounter) HasMountRefs(mountPath string, mountRefs []string) bool {
	// Copied from https://github.com/kubernetes/kubernetes/blob/53902ce5ede4/pkg/volume/util/util.go#L680-L706
	pathToFind := mountPath
	if i := strings.Index(mountPath, kubernetesPluginPathPrefix); i > -1 {
		pathToFind = mountPath[i:]
	}
	for _, ref := range mountRefs {
		if !strings.Contains(ref, pathToFind) {
			return true
		}
	}
	return false
}

// IsDirEmpty return status of dir empty or not
func IsDirEmpty(name string) (bool, error) {
	f, err := os.Open(name)
	if err != nil {
		return false, err
	}
	defer f.Close()

	// read in ONLY one file
	_, err = f.Readdir(1)
	// and if the file is EOF... well, the dir is empty.
	if err == io.EOF {
		return true, nil
	}
	return false, err
}

// CleanupSimpleMount umount and remove the path.
// It does not invoke mount helper and use syscall directly.
func CleanupSimpleMount(path string) error {
	err := unix.Unmount(path, 0)
	if err != nil {
		switch {
		case errors.Is(err, unix.ENOENT):
			return nil
		case errors.Is(err, unix.EINVAL):
			// Maybe not mounted, proceed to remove it. If not, remove will report error.
		default:
			return err
		}
	}

	errRm := os.Remove(path)
	if errRm == nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to remove %s: %w", path, errRm)
	} else {
		return fmt.Errorf("failed to unmount %s: %w; then failed to remove: %w", path, err, errRm)
	}
}
