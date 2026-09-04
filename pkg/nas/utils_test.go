//go:build !windows

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

package nas

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/mounter"
	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/utils"
	"github.com/stretchr/testify/assert"
	mountutils "k8s.io/mount-utils"
)

func TestParseMountFlags(t *testing.T) {
	tests := []struct {
		name         string
		mntOptions   []string
		wantVers     string
		wantAkID     string
		wantAkSecret string
		wantOptions  []string
	}{
		{
			"vers=3.0 normalized to 3",
			[]string{"mnt=/test", "vers=3.0"},
			"3", "", "", []string{"mnt=/test"},
		},
		{
			"vers=4.1 not normalized",
			[]string{"mnt=/test", "vers=4.1"},
			"4.1", "", "", []string{"mnt=/test"},
		},
		{
			"no vers",
			[]string{"mnt=/test", "a=b", "c=d"},
			"", "", "", []string{"mnt=/test", "a=b", "c=d"},
		},
		{
			"access_key_id and access_key_secret extracted",
			[]string{"mnt=/test", "access_key_id=myak", "access_key_secret=mysk", "vers=3"},
			"3", "myak", "mysk", []string{"mnt=/test"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVers, gotAkID, gotAkSecret, gotOptions := ParseMountFlags(tt.mntOptions)
			assert.Equal(t, tt.wantVers, gotVers)
			assert.Equal(t, tt.wantAkID, gotAkID)
			assert.Equal(t, tt.wantAkSecret, gotAkSecret)
			assert.Equal(t, tt.wantOptions, gotOptions)
		})
	}
}

func Test_addTLSMountOptions(t *testing.T) {
	type args struct {
		baseOptions []string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			"already set tls",
			args{[]string{"vers=3", "tls"}},
			[]string{"vers=3", "tls"},
		},
		{
			"tls not set",
			args{[]string{"vers=3", "nolock"}},
			[]string{"vers=3", "nolock", "tls"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, addTLSMountOptions(tt.args.baseOptions))
		})
	}
}

func TestGetMountRootAndRelPath(t *testing.T) {
	tests := []struct {
		name             string
		mountFsType      string
		opt              *Options
		expectedRootPath string
		expectedRelpath  string
	}{
		{
			name: "nil options",
		},
		{
			name:             "general nas",
			mountFsType:      "nfs",
			opt:              &Options{FSType: "general", Path: "/test", Server: "test-server"},
			expectedRootPath: "test-server:/",
			expectedRelpath:  "test",
		},
		{
			name:             "extreme nas with share-prefixed path",
			mountFsType:      "nfs",
			opt:              &Options{FSType: "extreme", Path: "/share/test", Server: "test-server"},
			expectedRootPath: "test-server:/",
			expectedRelpath:  "test",
		},
		{
			name:             "extreme nas with plain path",
			mountFsType:      "nfs",
			opt:              &Options{FSType: "extreme", Path: "/test", Server: "test-server"},
			expectedRootPath: "test-server:/",
			expectedRelpath:  "test",
		},
		{
			name:             "cpfs with share-prefixed path",
			mountFsType:      "cpfs-nfs",
			opt:              &Options{FSType: "cpfs", Path: "/share/test", Server: "test-server"},
			expectedRootPath: "test-server:/share",
			expectedRelpath:  "test",
		},
		{
			name:             "cpfs with root path",
			mountFsType:      "cpfs-nfs",
			opt:              &Options{FSType: "cpfs", Path: "/", Server: "test-server"},
			expectedRootPath: "",
			expectedRelpath:  "",
		},
		{
			name:             "cpfs with plain path",
			mountFsType:      "cpfs-nfs",
			opt:              &Options{FSType: "cpfs", Path: "/test", Server: "test-server"},
			expectedRootPath: "",
			expectedRelpath:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualRootPath, actualRelpath := getMountRootAndRelPath(tt.mountFsType, tt.opt)
			assert.Equal(t, tt.expectedRootPath, actualRootPath)
			assert.Equal(t, tt.expectedRelpath, actualRelpath)
		})
	}
}

func TestIsSubDir(t *testing.T) {
	tests := []struct {
		name            string
		parent          string
		child           string
		expected        bool
		expectedRelPath string
	}{
		{
			name:            "child is subdir of parent",
			parent:          "/parent",
			child:           "/parent/child",
			expected:        true,
			expectedRelPath: "child",
		},
		{
			name:            "child is subdir of parent with trailing slash",
			parent:          "/parent/",
			child:           "/parent/child",
			expected:        true,
			expectedRelPath: "child",
		},
		{
			name:            "child is not subdir of parent",
			parent:          "/parent",
			child:           "/child",
			expected:        false,
			expectedRelPath: "",
		},
		{
			name:            "parent equals child",
			parent:          "/parent",
			child:           "/parent",
			expected:        false,
			expectedRelPath: "",
		},
		{
			name:            "parent and child has the same prefix",
			parent:          "/parent",
			child:           "/parent-child",
			expected:        false,
			expectedRelPath: "",
		},
		{
			name:            "parent equals child with trailing slash",
			parent:          "/parent/",
			child:           "/parent",
			expected:        false,
			expectedRelPath: "",
		},
		{
			name:            "child with relative path",
			parent:          "/parent",
			child:           "parent",
			expected:        false,
			expectedRelPath: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualRelpath, actual := extractSubDir(tt.parent, tt.child)
			assert.Equal(t, tt.expected, actual)
			assert.Equal(t, tt.expectedRelPath, actualRelpath)
		})
	}
}

type recordingMounter struct {
	mountutils.FakeMounter
	lastOp *mounter.MountOperation
}

func (m *recordingMounter) ExtendedMount(_ context.Context, op *mounter.MountOperation) error {
	m.lastOp = op
	return nil
}

// pathNotFoundMounter records every mount operation and fails the first one with
// the given error, which lets tests drive the subpath auto-creation path.
type pathNotFoundMounter struct {
	mountutils.FakeMounter
	firstErr error
	ops      []*mounter.MountOperation
}

func (m *pathNotFoundMounter) ExtendedMount(_ context.Context, op *mounter.MountOperation) error {
	m.ops = append(m.ops, op)
	if len(m.ops) == 1 {
		return m.firstErr
	}
	return nil
}

// The subpath is created through a temporary mount of the filesystem root, which
// already exposes it: the target must then be bind mounted from that temporary
// mount instead of paying for a second remote mount, and "ro" (dropped to be
// able to create the directory) must be restored on the bind.
func TestDoMount_SubpathCreationBindsCreatedDirectory(t *testing.T) {
	kubeletRootDir := utils.KubeletRootDir
	utils.KubeletRootDir = t.TempDir()
	defer func() { utils.KubeletRootDir = kubeletRootDir }()

	m := &pathNotFoundMounter{
		firstErr: errors.New("mount.nfs: mounting test-server:/sub/dir failed, reason given by server: No such file or directory"),
	}
	opt := &Options{
		FSType:        "standard",
		Server:        "test-server",
		Path:          "/sub/dir",
		Vers:          "3",
		Options:       []string{"nolock", "ro"},
		MountProtocol: MountProtocolNFS,
	}
	assert.NoError(t, doMount(m, opt, "/mnt/target", "vol-123", "pod-uid", false))

	assert.Len(t, m.ops, 3)
	// the failed mount of the volume itself
	assert.Equal(t, "test-server:/sub/dir", m.ops[0].Source)
	// the temporary read-write mount of the filesystem root
	tmpPath := m.ops[1].Target
	assert.Equal(t, "test-server:/", m.ops[1].Source)
	assert.Contains(t, tmpPath, filepath.Join(utils.KubeletRootDir, "csi-plugins"))
	assert.NotContains(t, m.ops[1].Options, "ro")
	// the bind mount of the created subpath, replacing the second remote mount
	assert.Equal(t, filepath.Join(tmpPath, "sub/dir"), m.ops[2].Source)
	assert.Equal(t, "/mnt/target", m.ops[2].Target)
	assert.Empty(t, m.ops[2].FsType)
	assert.Equal(t, []string{"bind", "ro"}, m.ops[2].Options)
	assert.Empty(t, m.ops[2].Secrets)
}

// alinas mounts (EFC/CPFS) reach the created subpath the same way: the target is
// bound from the temporary mount of the filesystem root.
func TestDoMount_SubpathCreationBindsCreatedDirectoryForAlinas(t *testing.T) {
	kubeletRootDir := utils.KubeletRootDir
	utils.KubeletRootDir = t.TempDir()
	defer func() { utils.KubeletRootDir = kubeletRootDir }()

	m := &pathNotFoundMounter{firstErr: errors.New("Failed to bind mount")}
	opt := &Options{
		FSType:        "cpfs",
		Server:        "cpfs-0123456789-vpc.cn-hangzhou.cpfs.aliyuncs.com",
		Path:          "/share/sub",
		Vers:          "3",
		Options:       []string{"nolock"},
		MountProtocol: MountProtocolEFC,
	}
	assert.NoError(t, doMount(m, opt, "/mnt/target", "vol-123", "pod-uid", true))

	assert.Len(t, m.ops, 3)
	// the temporary mount starts at /share for cpfs filesystems
	tmpPath := m.ops[1].Target
	assert.Equal(t, opt.Server+":/share", m.ops[1].Source)
	assert.Equal(t, filepath.Join(tmpPath, "sub"), m.ops[2].Source)
	assert.Equal(t, "/mnt/target", m.ops[2].Target)
	assert.Equal(t, []string{"bind"}, m.ops[2].Options)
}

func TestDoMount_AccesspointWithAkSkFromMountOptions(t *testing.T) {
	m := &recordingMounter{}
	opt := &Options{
		Accesspoint:   "ap-xxx.nas.aliyuncs.com",
		Path:          "/",
		Vers:          "3",
		Options:       []string{"nolock"},
		MountProtocol: MountProtocolNFS,
		AkID:          "test-ak-id",
		AkSecret:      "test-ak-secret",
	}
	err := doMount(m, opt, "/mnt/target", "vol-123", "pod-uid", false)
	assert.NoError(t, err)

	assert.Equal(t, "ap-xxx.nas.aliyuncs.com:/", m.lastOp.Source)
	assert.Equal(t, "alinas", m.lastOp.FsType)
	assert.Equal(t, "test-ak-id", m.lastOp.Secrets[akIDKey])
	assert.Equal(t, "test-ak-secret", m.lastOp.Secrets[akSecretKey])
	assert.Contains(t, m.lastOp.Options, "tls")
}

func TestDoMountForwardsJWTAuthOptionsFromVolumeContext(t *testing.T) {
	m := &recordingMounter{}
	opt := &Options{
		Accesspoint:             "ap-xxx.nas.aliyuncs.com",
		Path:                    "/",
		Vers:                    "3",
		Options:                 []string{"nolock", "tls"},
		MountProtocol:           MountProtocolNFS,
		AuthType:                "agent-identity",
		SandboxId:               "sandbox-abc",
		SandboxCredProviderName: "nasfs-read-write",
	}
	err := doMount(m, opt, "/mnt/target", "vol-123", "pod-uid", false)
	assert.NoError(t, err)

	assert.Contains(t, m.lastOp.Options, "authType=agent-identity")
	assert.Contains(t, m.lastOp.Options, "sandboxId=sandbox-abc")
	assert.Contains(t, m.lastOp.Options, "sandboxCredProviderName=nasfs-read-write")
}

func TestAppendJWTAuthOptions(t *testing.T) {
	tests := []struct {
		name    string
		options []string
		opt     *Options
		want    []string
	}{
		{
			name:    "all empty, nothing appended",
			options: []string{"nolock", "tls"},
			opt:     &Options{},
			want:    []string{"nolock", "tls"},
		},
		{
			name:    "append all jwtauth options",
			options: []string{"tls"},
			opt: &Options{
				AuthType:                "agent-identity",
				SandboxId:               "sb-1",
				SandboxCredProviderName: "cp-1",
			},
			want: []string{"tls", "authType=agent-identity", "sandboxId=sb-1", "sandboxCredProviderName=cp-1"},
		},
		{
			name:    "do not overwrite key already present in options",
			options: []string{"authType=preset"},
			opt:     &Options{AuthType: "agent-identity"},
			want:    []string{"authType=preset"},
		},
		{
			name:    "do not overwrite key present in comma-joined option",
			options: []string{"tls,authType=preset"},
			opt:     &Options{AuthType: "agent-identity"},
			want:    []string{"tls,authType=preset"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendJWTAuthOptions(tt.options, tt.opt)
			assert.Equal(t, tt.want, got)
		})
	}
}
