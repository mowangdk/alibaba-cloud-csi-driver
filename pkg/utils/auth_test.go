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
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetAccessControl(t *testing.T) {
	testAccessKey := "testkey"
	testAccessKeySecret := "testvalue"
	os.Setenv("ACCESS_KEY_ID", testAccessKey)
	os.Setenv("ACCESS_KEY_SECRET", testAccessKeySecret)
	ac := GetAccessControl()
	assert.Equal(t, testAccessKey, ac.AccessKeyID)
	assert.Equal(t, testAccessKeySecret, ac.AccessKeySecret)
	assert.Empty(t, ac.StsToken)
	os.Unsetenv("ACCESS_KEY_ID")
	os.Unsetenv("ACCESS_KEY_SECRET")
	ac = GetAccessControl()
	assert.Empty(t, ac.AccessKeyID)
	assert.Empty(t, ac.AccessKeySecret)
	assert.Empty(t, ac.StsToken)
}

func TestValidatePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantOK  bool
		wantErr string
	}{
		// Valid paths under kubelet root
		{
			name:   "valid pod volume path",
			path:   "/var/lib/kubelet/pods/uid/volumes/kubernetes.io~csi/pv/mount",
			wantOK: true,
		},
		{
			name:   "valid plugin path",
			path:   "/var/lib/kubelet/plugins/kubernetes.io/csi/pv/abc/globalmount",
			wantOK: true,
		},
		{
			name:   "kubelet root dir itself",
			path:   "/var/lib/kubelet",
			wantOK: true,
		},
		{
			name:   "path directly under kubelet root",
			path:   "/var/lib/kubelet/csi-plugins",
			wantOK: true,
		},

		// Relative path is rejected before any further processing
		{
			name:    "relative path rejected",
			path:    "var/lib/kubelet/pods/uid",
			wantOK:  false,
			wantErr: "must be an absolute path",
		},

		// Path traversal (filepath.Clean resolves these)
		{
			name:   "dot-dot-slash traversal cleaned",
			path:   "/var/lib/kubelet/../etc/passwd",
			wantOK: true,
		},
		{
			name:   "deep traversal cleaned",
			path:   "/var/lib/kubelet/pods/../../etc",
			wantOK: true,
		},
		{
			name:   "bare double dot resolves to parent",
			path:   "/var/lib/kubelet/..",
			wantOK: true,
		},
		{
			name:   "traversal that stays within root",
			path:   "/var/lib/kubelet/pods/../plugins",
			wantOK: true,
		},
		// dot-slash and slash-dot are cleaned to valid paths
		{
			name:   "dot-slash cleaned to valid path",
			path:   "/var/lib/kubelet/./pods",
			wantOK: true,
		},
		{
			name:   "trailing dot cleaned to valid path",
			path:   "/var/lib/kubelet/pods/.",
			wantOK: true,
		},

		// Sensitive system path prefix (/proc)
		{
			name:    "proc path rejected",
			path:    "/proc/self/fd/0",
			wantOK:  false,
			wantErr: "under sensitive path /proc",
		},
		{
			name:    "proc root rejected",
			path:    "/proc",
			wantOK:  false,
			wantErr: "under sensitive path /proc",
		},
		{
			name:   "proc as non-prefix component is fine",
			path:   "/var/lib/kubelet/pods/uid/proc-data/file",
			wantOK: true,
		},

		// Paths outside kubelet root are allowed (kubelet root check is done elsewhere)
		{
			name:   "tmp directory",
			path:   "/tmp/something",
			wantOK: true,
		},
		{
			name:   "home directory",
			path:   "/home/user/data",
			wantOK: true,
		},
		{
			name:   "var but not kubelet",
			path:   "/var/log/messages",
			wantOK: true,
		},
		{
			name:   "mnt directory",
			path:   "/mnt/data",
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, err := ValidatePath(tt.path)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidatePath_PATHDirs(t *testing.T) {
	origPath := os.Getenv("PATH")
	defer func() { _ = os.Setenv("PATH", origPath) }()

	_ = os.Setenv("PATH", "/usr/bin:/usr/sbin:/usr/local/bin")

	// Path under a PATH directory is rejected.
	ok, err := ValidatePath("/usr/bin/ls")
	assert.False(t, ok)
	assert.ErrorContains(t, err, "under PATH directory")

	// PATH directory itself is rejected.
	ok, err = ValidatePath("/usr/sbin")
	assert.False(t, ok)
	assert.ErrorContains(t, err, "under PATH directory")

	// Path not under any PATH directory is allowed.
	ok, err = ValidatePath("/var/lib/data")
	assert.True(t, ok)
	assert.NoError(t, err)

	// Path that shares a prefix but is not actually under a PATH dir.
	ok, err = ValidatePath("/usr/binary/file")
	assert.True(t, ok)
	assert.NoError(t, err)
}
