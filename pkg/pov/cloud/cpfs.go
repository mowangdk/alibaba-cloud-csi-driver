package cloud

import (
	"context"
	http "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	cpfs "github.com/alibabacloud-go/nas-20170626/v3/client"
	"github.com/alibabacloud-go/tea/tea"
	aliyunep "github.com/aliyun/alibaba-cloud-sdk-go/sdk/endpoints"
	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/log"
	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/utils"
	"os"
)

type cpfsCloud struct {
	regionID string
	cpfsc    *cpfs.Client
	rc       Converter
}

func NewCPFSCloud(regionID string) (*cpfsCloud, error) {
	accessControl := utils.GetAccessControl()

	config := new(http.Config)

	config.AccessKeyId = &accessControl.AccessKeyID
	config.AccessKeySecret = &accessControl.AccessKeySecret
	config.SecurityToken = &accessControl.StsToken

	// config.Endpoint = tea.String("ens.aliyuncs.com")
	cpfsClient, err := cpfs.NewClient(config)
	if err != nil {
		log.Log.Fatalf("NewCPFSCloud: failed to create cpfs cloud: %+v", err)
	}
	setCPFSEndPoint(regionID)
	return &cpfsCloud{cpfsc: cpfsClient, regionID: regionID, rc: &cpfsConvert{}}, nil
}

// setPovEndPoint Set Endpoint for pov
func setCPFSEndPoint(regionID string) {
	aliyunep.AddEndpointMapping(regionID, "Nas", "nas-vpc."+regionID+".aliyuncs.com")

	// use environment endpoint setting first;
	if ep := os.Getenv("POV_ENDPOINT"); ep != "" {
		aliyunep.AddEndpointMapping(regionID, "cpfs", ep)
	}
}

func (c *cpfsCloud) updateToken() error {
	accessControl := utils.GetAccessControl()

	config := new(http.Config)

	config.AccessKeyId = &accessControl.AccessKeyID
	config.AccessKeySecret = &accessControl.AccessKeySecret
	config.SecurityToken = &accessControl.StsToken
	ec, err := cpfs.NewClient(config)
	if err != nil {
		log.Log.Errorf("newENSClient: failed to update cpfs cloud: %+v", err)
		return err
	}
	c.cpfsc = ec
	return nil
}

func (c *cpfsCloud) CreateVolume(ctx context.Context, volumeName string, diskOptions *PovOptions) (fsId, requestID string, err error) {
	return "", "", nil
}

func (c *cpfsCloud) DeleteVolume(ctx context.Context, filesystemID string) (reqeustID string, err error) {

	return "", nil
}

func (c *cpfsCloud) CreateVolumeMountPoint(ctx context.Context, filesystemID string) (mpId string, err error) {
	cvmp := &cpfs.CreateVscMountPointRequest{
		FileSystemId: tea.String(filesystemID),
	}
	resp, err := c.cpfsc.CreateVscMountPoint(cvmp)
	if err != nil {
		c.updateToken()
		resp, err = c.cpfsc.CreateVscMountPoint(cvmp)
		if err != nil {
			return "", err
		}
	}
	return *resp.Body.MountPointDomain, nil
}

func (c *cpfsCloud) AttachVscMountPoint(ctx context.Context, mpId, fsId, instanceID string) (requestID string, err error) {

	defaultMountInfo := &cpfs.AttachVscMountPointRequestVscAttachInfos{
		InstanceId: tea.String(instanceID),
		VscType:    tea.String(""),
		VscId:      tea.String(""),
	}

	avmpr := &cpfs.AttachVscMountPointRequest{
		FileSystemId:     tea.String(fsId),
		MountPointDomain: tea.String(mpId),
		VscAttachInfos:   []*cpfs.AttachVscMountPointRequestVscAttachInfos{defaultMountInfo},
	}

	resp, err := c.cpfsc.AttachVscMountPoint(avmpr)
	if err != nil {
		c.updateToken()
		resp, err = c.cpfsc.AttachVscMountPoint(avmpr)
		if err != nil {
			return "", err
		}
	}

	return *resp.Body.RequestId, nil
}

func (c *cpfsCloud) DetachVscMountPoint(ctx context.Context, mpId, filesystemID, instanceID, vscType, vscId string) (requestID string, err error) {

	defaultMountInfo := &cpfs.DetachVscMountPointRequestVscAttachInfos{
		InstanceId: tea.String(instanceID),
		VscType:    tea.String(""),
		VscId:      tea.String(""),
	}

	dvmpr := &cpfs.DetachVscMountPointRequest{
		FileSystemId:     tea.String(filesystemID),
		MountPointDomain: tea.String(mpId),
		VscAttachInfos:   []*cpfs.DetachVscMountPointRequestVscAttachInfos{defaultMountInfo},
	}
	resp, err := c.cpfsc.DetachVscMountPoint(dvmpr)
	if err != nil {
		c.updateToken()
		resp, err = c.cpfsc.DetachVscMountPoint(dvmpr)
		if err != nil {
			return "", err
		}
	}
	return *resp.Body.RequestId, nil
}

func (c *cpfsCloud) DescribeMountPointVscIds(ctx context.Context, fsId, mpId string) (*VscMountPointResp, error) {
	describeVscMountPointAttachInfoList := []cpfs.DescribeVscMountPointAttachInfoResponse{}
	resp, err := c.describeMountPointVscIds(fsId, mpId, "")
	if err != nil {
		return nil, err
	}
	for resp.Body.NextToken != nil {
		describeVscMountPointAttachInfoList = append(describeVscMountPointAttachInfoList, *resp)
		resp, err = c.describeMountPointVscIds(fsId, mpId, *resp.Body.NextToken)
		if err != nil {
			return nil, err
		}
	}
	result := c.rc.Convert2VscMountPointResp(describeVscMountPointAttachInfoList)
	return result, nil
}

func (c *cpfsCloud) describeMountPointVscIds(fsId, mpId, nextToken string) (*cpfs.DescribeVscMountPointAttachInfoResponse, error) {

	dvmpr := &cpfs.DescribeVscMountPointAttachInfoRequest{
		MountPointDomain: tea.String(mpId),
	}
	if nextToken != "" {
		dvmpr.NextToken = tea.String(nextToken)
	}
	resp, err := c.cpfsc.DescribeVscMountPointAttachInfo(dvmpr)
	if err != nil {
		c.updateToken()
		resp, err = c.cpfsc.DescribeVscMountPointAttachInfo(dvmpr)
		if err != nil {
			return nil, err
		}
	}
	return resp, nil
}

func (c *cpfsCloud) DescribeFilesystemMountPoints(ctx context.Context, fsId string) (*VscMountPointResp, error) {

	result := &VscMountPointResp{}
	describeFilesystemMountPointsList := []cpfs.DescribeVscMountPointsResponse{}
	resp, err := c.describeFilesystemMountPoints(fsId, "")
	if err != nil {
		return nil, err
	}
	for resp.Body.NextToken != nil {
		describeFilesystemMountPointsList = append(describeFilesystemMountPointsList, *resp)
		resp, err = c.describeFilesystemMountPoints(fsId, *resp.Body.NextToken)
		if err != nil {
			return nil, err
		}
	}

	vscInfos := []*FlatVscMountPoint{}
	for _, describeResp := range describeFilesystemMountPointsList {
		for _, vsc := range describeResp.Body.VscMountPoints.VscMountPoint {
			vscInfo := &FlatVscMountPoint{}
			vscInfo.MountPointDomain = vsc.MountPointDomain
			vscInfos = append(vscInfos, vscInfo)
		}
	}
	result.VscInfos = vscInfos
	return result, nil
}

func (c *cpfsCloud) describeFilesystemMountPoints(fsid, nextToken string) (*cpfs.DescribeVscMountPointsResponse, error) {
	dvmpr := &cpfs.DescribeVscMountPointsRequest{}
	dvmpr.FileSystemId = tea.String(fsid)
	if nextToken != "" {
		dvmpr.NextToken = tea.String(nextToken)
	}
	resp, err := c.cpfsc.DescribeVscMountPoints(dvmpr)
	if err != nil {
		c.updateToken()
		resp, err = c.cpfsc.DescribeVscMountPoints(dvmpr)
		if err != nil {
			return nil, err
		}
	}
	return resp, nil
}
