//go:build !windows

/*
Copyright 2024 The Kubernetes Authors.

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

package disk

import (
	"context"
	"errors"
	"testing"

	ecs "github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/klog/v2/ktesting"
)

// fakeBatcher implements batcher.Batcher[ecs.Disk] for tests.
type fakeBatcher struct {
	disk   *ecs.Disk
	err    error
	called bool
}

func (f *fakeBatcher) Describe(ctx context.Context, id string) (*ecs.Disk, error) {
	f.called = true
	return f.disk, f.err
}

func TestUpdatePVDiskType(t *testing.T) {
	const pvName = "test-pv"
	essdPL1Label, essdPL1Topo := diskTypeMetadata(DiskESSD, "PL1")

	newPV := func() *corev1.PersistentVolume {
		return &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:        pvName,
				Labels:      map[string]string{labelVolumeType: essdPL1Label, "keep": "me"},
				Annotations: map[string]string{annVolumeTopoKey: essdPL1Topo},
			},
		}
	}
	getPV := func(t *testing.T, ctx context.Context, client *fake.Clientset) *corev1.PersistentVolume {
		t.Helper()
		got, err := client.CoreV1().PersistentVolumes().Get(ctx, pvName, metav1.GetOptions{})
		require.NoError(t, err)
		return got
	}
	// assertUnchanged verifies the PV still carries its original disktype metadata.
	assertUnchanged := func(t *testing.T, ctx context.Context, client *fake.Clientset) {
		t.Helper()
		got := getPV(t, ctx, client)
		assert.Equal(t, essdPL1Label, got.Labels[labelVolumeType])
		assert.Equal(t, essdPL1Topo, got.Annotations[annVolumeTopoKey])
	}

	t.Run("category change updates both label and annotation", func(t *testing.T) {
		_, ctx := ktesting.NewTestContext(t)
		fb := &fakeBatcher{disk: &ecs.Disk{Category: "cloud_auto"}}
		client := fake.NewSimpleClientset(newPV())
		cs := &controllerServer{clientSet: client, cd: DiskCreateDelete{batcher: fb}}

		require.NoError(t, cs.updatePVDiskType(ctx, "d-x", pvName, ModifyParameters{Category: "cloud_auto"}))

		wantLabel, wantTopo := diskTypeMetadata("cloud_auto", "")
		got := getPV(t, ctx, client)
		assert.Equal(t, wantLabel, got.Labels[labelVolumeType])
		assert.Equal(t, wantTopo, got.Annotations[annVolumeTopoKey])
		assert.Equal(t, "me", got.Labels["keep"], "unrelated labels must be preserved")
	})

	t.Run("PL change updates label, annotation unchanged", func(t *testing.T) {
		_, ctx := ktesting.NewTestContext(t)
		// ECS reports the resulting disk is still cloud_essd, now PL2.
		fb := &fakeBatcher{disk: &ecs.Disk{Category: "cloud_essd", PerformanceLevel: "PL2"}}
		client := fake.NewSimpleClientset(newPV())
		cs := &controllerServer{clientSet: client, cd: DiskCreateDelete{batcher: fb}}

		require.NoError(t, cs.updatePVDiskType(ctx, "d-x", pvName, ModifyParameters{PerformanceLevel: "PL2"}))

		got := getPV(t, ctx, client)
		assert.Equal(t, "cloud_essd.PL2", got.Labels[labelVolumeType])
		assert.Equal(t, essdPL1Topo, got.Annotations[annVolumeTopoKey], "same category -> topology annotation unchanged")
	})

	t.Run("no category/PL change: batcher not called, PV untouched", func(t *testing.T) {
		_, ctx := ktesting.NewTestContext(t)
		fb := &fakeBatcher{disk: &ecs.Disk{Category: "cloud_auto"}}
		client := fake.NewSimpleClientset(newPV())
		cs := &controllerServer{clientSet: client, cd: DiskCreateDelete{batcher: fb}}

		require.NoError(t, cs.updatePVDiskType(ctx, "d-x", pvName, ModifyParameters{ProvisionedIops: new(int)}))

		assert.False(t, fb.called, "DescribeDisks must be skipped when category/PL unchanged")
		assertUnchanged(t, ctx, client)
	})

	t.Run("describe error: returns error, PV untouched", func(t *testing.T) {
		_, ctx := ktesting.NewTestContext(t)
		fb := &fakeBatcher{err: errors.New("ecs boom")}
		client := fake.NewSimpleClientset(newPV())
		cs := &controllerServer{clientSet: client, cd: DiskCreateDelete{batcher: fb}}

		err := cs.updatePVDiskType(ctx, "d-x", pvName, ModifyParameters{Category: "cloud_auto"})
		require.Error(t, err)
		assertUnchanged(t, ctx, client)
	})

	t.Run("patch error (non-NotFound): returns error", func(t *testing.T) {
		_, ctx := ktesting.NewTestContext(t)
		fb := &fakeBatcher{disk: &ecs.Disk{Category: "cloud_auto"}}
		client := fake.NewSimpleClientset(newPV())
		client.PrependReactor("patch", "persistentvolumes", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewInternalError(errors.New("boom"))
		})
		cs := &controllerServer{clientSet: client, cd: DiskCreateDelete{batcher: fb}}

		err := cs.updatePVDiskType(ctx, "d-x", pvName, ModifyParameters{Category: "cloud_auto"})
		require.Error(t, err)
	})

	t.Run("disk gone: swallowed, PV untouched", func(t *testing.T) {
		_, ctx := ktesting.NewTestContext(t)
		fb := &fakeBatcher{disk: nil} // batcher reports the disk no longer exists
		client := fake.NewSimpleClientset(newPV())
		cs := &controllerServer{clientSet: client, cd: DiskCreateDelete{batcher: fb}}

		require.NoError(t, cs.updatePVDiskType(ctx, "d-x", pvName, ModifyParameters{Category: "cloud_auto"}))
		assertUnchanged(t, ctx, client)
	})

	t.Run("PV gone: swallowed, no error", func(t *testing.T) {
		_, ctx := ktesting.NewTestContext(t)
		fb := &fakeBatcher{disk: &ecs.Disk{Category: "cloud_auto"}}
		client := fake.NewSimpleClientset() // no PV
		cs := &controllerServer{clientSet: client, cd: DiskCreateDelete{batcher: fb}}

		require.NoError(t, cs.updatePVDiskType(ctx, "d-x", pvName, ModifyParameters{Category: "cloud_auto"}))
	})
}
