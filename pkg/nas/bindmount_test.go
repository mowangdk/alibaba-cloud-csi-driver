//go:build !windows

package nas

import (
	"context"
	"errors"
	"testing"

	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/mounter"
	"github.com/stretchr/testify/assert"
	mountutils "k8s.io/mount-utils"
)

// fallbackMounter fails the first ExtendedMount with an NFS "path not found"
// error to trigger the subpath-creation fallback, succeeds later ExtendedMounts
// (the temporary root mount), and records local Mount (bind) calls.
type fallbackMounter struct {
	mountutils.FakeMounter
	extendedCalls int
	bindOptions   [][]string
}

func (m *fallbackMounter) ExtendedMount(_ context.Context, _ *mounter.MountOperation) error {
	m.extendedCalls++
	if m.extendedCalls == 1 {
		return errors.New("reason given by server: No such file or directory")
	}
	return nil
}

func (m *fallbackMounter) Mount(_, _, _ string, options []string) error {
	m.bindOptions = append(m.bindOptions, options)
	return nil
}

func TestDoMount_PlainNFSFallbackUsesBind(t *testing.T) {
	m := &fallbackMounter{}
	opt := &Options{
		Server:        "1.2.3.4",
		Path:          "/subdir",
		Vers:          "3",
		MountProtocol: MountProtocolNFS,
	}
	err := doMount(m, opt, t.TempDir(), "vol-123", "pod-uid", false)
	assert.NoError(t, err)
	// One failed target mount + one temporary root mount, then bind (no second
	// NFS mount of the target).
	assert.Equal(t, 2, m.extendedCalls)
	assert.Equal(t, [][]string{{"bind"}}, m.bindOptions)
}

func TestDoMount_PlainNFSFallbackReadOnlyRemounts(t *testing.T) {
	m := &fallbackMounter{}
	opt := &Options{
		Server:        "1.2.3.4",
		Path:          "/subdir",
		Vers:          "3",
		Options:       []string{"ro"},
		MountProtocol: MountProtocolNFS,
	}
	err := doMount(m, opt, t.TempDir(), "vol-123", "pod-uid", false)
	assert.NoError(t, err)
	assert.Equal(t, [][]string{{"bind"}, {"bind", "remount", "ro"}}, m.bindOptions)
}
