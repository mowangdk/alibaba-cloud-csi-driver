//go:build !windows

package ens

import (
	"os"

	http "github.com/alibabacloud-go/darabonba-openapi/client"
	ensCli "github.com/alibabacloud-go/ens-20171110/v3/client"
	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/credentials"
	"k8s.io/klog/v2"
)

const defaultENSEndpoint = "ens.aliyuncs.com"

type ENSClient struct {
	c *ensCli.Client
}

// ensEndpoint resolves the ENS OpenAPI endpoint. It cannot be derived from
// RegionID because RegionID is itself resolved through DescribeInstance, so the
// international site must set ENS_ENDPOINT explicitly.
func ensEndpoint() string {
	if ep := os.Getenv("ENS_ENDPOINT"); ep != "" {
		return ep
	}
	return defaultENSEndpoint
}

func newENSClient() ENSClient {
	cred, err := credentials.NewCredential()
	if err != nil {
		klog.Fatalf("newENSClient: failed to create credential: %+v", err)
	}

	endpoint := ensEndpoint()
	klog.Infof("newENSClient: using ENS endpoint %s", endpoint)

	config := http.Config{
		Endpoint:   new(endpoint),
		Credential: cred,
	}

	ec, err := ensCli.NewClient(&config)
	if err != nil {
		klog.Fatalf("newENSClient: failed to create ens client: %+v", err)
	}
	return ENSClient{c: ec}
}

func (ec *ENSClient) DescribeInstance(instanceId string) ([]*ensCli.DescribeInstancesResponseBodyInstancesInstance, error) {
	dir := &ensCli.DescribeInstancesRequest{
		InstanceId: new(instanceId),
	}
	resp, err := ec.c.DescribeInstances(dir)
	if err != nil {
		klog.Errorf("DescribeInstance: describe instance failed err: %+v", err)
		return []*ensCli.DescribeInstancesResponseBodyInstancesInstance{}, err
	}
	return resp.Body.Instances.Instance, nil
}

func (ec *ENSClient) CreateVolume(regionID, diskType, size string) (string, error) {
	cdr := &ensCli.CreateDiskRequest{
		InstanceChargeType: new("PostPaid"),
		EnsRegionId:        new(regionID),
		Category:           new(diskType),
		Size:               new(size),
	}

	resp, err := ec.c.CreateDisk(cdr)
	if err != nil {
		klog.Errorf("CreateVolume: create volume failed err: %+v, regionID: %s, category: %s, size: %s", err, regionID, diskType, size)
		return "", err
	}
	return *resp.Body.InstanceIds[0], nil
}

func (ec *ENSClient) DeleteVolume(diskID string) {

}

func (ec *ENSClient) AttachVolume(diskID, instanceID string) error {
	adr := &ensCli.AttachDiskRequest{
		DiskId:     new(diskID),
		InstanceId: new(instanceID),
	}

	resp, err := ec.c.AttachDisk(adr)
	klog.Infof("AttachVolume: attach disk resp: %+v", resp)
	if err != nil {
		klog.Errorf("AttachVolume: attach volume failed err: %+v", err)
		return err
	}
	return nil

}

func (ec *ENSClient) DescribeVolume(diskID string) (*ensCli.DescribeDisksResponseBodyDisksDisks, error) {
	ddr := &ensCli.DescribeDisksRequest{
		DiskId:      new(diskID),
		EnsRegionId: new(GlobalConfigVar.RegionID),
	}
	resp, err := ec.c.DescribeDisks(ddr)
	if err != nil {
		klog.Errorf("DescribeVolume: describe volume failed err: %+v", err)
		return nil, err
	}
	return resp.Body.Disks.Disks[0], nil
}

func (ec *ENSClient) DetachVolume(diskID, instanceID string) error {
	ddr := &ensCli.DetachDiskRequest{
		DiskId:     new(diskID),
		InstanceId: new(instanceID),
	}
	resp, err := ec.c.DetachDisk(ddr)
	klog.Infof("DetachVolume: detach disk resp: %+v", resp)
	if err != nil {
		klog.Errorf("DescribeVolume: describe volume failed err: %+v", err)
		return err
	}
	return nil
}
