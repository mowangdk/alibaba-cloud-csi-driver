package cloud

import (
	"context"
	"encoding/json"
	aliyunep "github.com/aliyun/alibaba-cloud-sdk-go/sdk/endpoints"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/dfs"
	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/log"
	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/utils"
	"os"
)

type dfsCloud struct {
	regionID string
	dbfc     *dfs.Client
	rc       Converter
}

type PovOptions struct {
	ZoneID                       string
	DataRedundancyType           string
	ProtocolType                 string
	StorageType                  string
	Capacity                     int64
	FsName                       string
	ThroughputMode               string
	ProvisionedThroughputInmibps string
}

func NewDFSCloud(regionID string) (*dfsCloud, error) {
	// Init ECS Client
	ac := utils.GetAccessControl()
	log.Log.Infof("newCloud: ac: %+v", ac)
	dfsClient, err := dfs.NewClientWithOptions(regionID, ac.Config, ac.Credential)
	if err != nil {
		return nil, err
	}
	setDFSEndPoint(regionID)
	return &dfsCloud{dbfc: dfsClient, regionID: regionID, rc: dfsConvert{}}, nil
}

func (c *dfsCloud) updateToken() error {
	ac := utils.GetAccessControl()
	dfsClient, err := dfs.NewClientWithOptions(c.regionID, ac.Config, ac.Credential)
	if err != nil {
		return err
	}
	c.dbfc = dfsClient
	return nil
}

// setPovEndPoint Set Endpoint for pov
func setDFSEndPoint(regionID string) {
	aliyunep.AddEndpointMapping(regionID, "Dfs", "dfs-vpc."+regionID+".aliyuncs.com")

	// use environment endpoint setting first;
	if ep := os.Getenv("POV_ENDPOINT"); ep != "" {
		aliyunep.AddEndpointMapping(regionID, "Dfs", ep)
	}
}

func (c *dfsCloud) CreateVolume(ctx context.Context, volumeName string, diskOptions *PovOptions) (fsId, requestID string, err error) {
	cfsr := dfs.CreateCreateFileSystemRequest()
	cfsr.FileSystemName = volumeName
	cfsr.InputRegionId = c.regionID
	cfsr.ZoneId = diskOptions.ZoneID
	cfsr.DataRedundancyType = diskOptions.DataRedundancyType
	cfsr.ProtocolType = diskOptions.ProtocolType
	cfsr.StorageType = diskOptions.StorageType
	cfsr.SpaceCapacity = requests.NewInteger64(diskOptions.Capacity)

	resp, err := c.dbfc.CreateFileSystem(cfsr)
	if err != nil {
		c.updateToken()
		resp, err = c.dbfc.CreateFileSystem(cfsr)
		if err != nil {
			return "", "", err
		}
	}
	return resp.FileSystemId, resp.RequestId, nil
}

func (c *dfsCloud) DeleteVolume(ctx context.Context, filesystemID string) (reqeustID string, err error) {

	cdfsr := dfs.CreateDeleteFileSystemRequest()
	cdfsr.FileSystemId = filesystemID
	cdfsr.InputRegionId = c.regionID

	resp, err := c.dbfc.DeleteFileSystem(cdfsr)
	if err != nil {
		c.updateToken()
		resp, err = c.dbfc.DeleteFileSystem(cdfsr)
		if err != nil {
			return "", err
		}
	}
	return resp.RequestId, nil
}

func (c *dfsCloud) CreateVolumeMountPoint(ctx context.Context, filesystemID string) (mpId string, err error) {
	cmp := dfs.CreateCreateVscMountPointRequest()
	cmp.FileSystemId = filesystemID
	cmp.InputRegionId = c.regionID
	resp, err := c.dbfc.CreateVscMountPoint(cmp)
	if err != nil {
		c.updateToken()
		resp, err = c.dbfc.CreateVscMountPoint(cmp)
		if err != nil {
			return "", err
		}
	}
	return resp.MountPointId, nil
}

func (c *dfsCloud) AttachVscMountPoint(ctx context.Context, mpId, fsId, instanceID string) (requestID string, err error) {
	cavmpr := dfs.CreateAttachVscMountPointRequest()
	jStr, err := json.Marshal([]string{instanceID})
	if err != nil {
		return "", err
	}
	cavmpr.InstanceIds = string(jStr)
	cavmpr.MountPointId = mpId
	cavmpr.FileSystemId = fsId
	cavmpr.InputRegionId = c.regionID
	resp, err := c.dbfc.AttachVscMountPoint(cavmpr)
	if err != nil {
		c.updateToken()
		resp, err = c.dbfc.AttachVscMountPoint(cavmpr)
		if err != nil {
			return "", err
		}
	}

	return resp.RequestId, nil
}

func (c *dfsCloud) DetachVscMountPoint(ctx context.Context, mpId, filesystemID, instanceID, vscType, vscId string) (requestID string, err error) {

	cdvmpr := dfs.CreateDetachVscMountPointRequest()
	cdvmpr.InputRegionId = c.regionID
	cdvmpr.MountPointId = mpId
	cdvmpr.FileSystemId = filesystemID
	jStr, err := json.Marshal([]string{instanceID})
	if err != nil {
		return "", err
	}
	cdvmpr.InstanceIds = string(jStr)

	resp, err := c.dbfc.DetachVscMountPoint(cdvmpr)
	if err != nil {
		c.updateToken()
		resp, err = c.dbfc.DetachVscMountPoint(cdvmpr)
		if err != nil {
			return "", err
		}
	}
	return resp.RequestId, nil
}

func (c *dfsCloud) DescribeFilesystemMountPoints(ctx context.Context, fsId string) (dvmpr *VscMountPointResp, err error) {
	dvmp := dfs.CreateDescribeVscMountPointsRequest()
	dvmp.InputRegionId = c.regionID
	dvmp.FileSystemId = fsId

	resp, err := c.dbfc.DescribeVscMountPoints(dvmp)
	if err != nil {
		c.updateToken()
		resp, err = c.dbfc.DescribeVscMountPoints(dvmp)
		if err != nil {
			return &VscMountPointResp{}, err
		}
	}
	result := c.rc.Convert2VscMountPointResp(resp)
	return result, nil
}

func (c *dfsCloud) DescribeMountPointVscIds(ctx context.Context, fsId, mpId string) (*VscMountPointResp, error) {

	dvmp := dfs.CreateDescribeVscMountPointsRequest()
	dvmp.InputRegionId = c.regionID
	dvmp.FileSystemId = fsId
	dvmp.MountPointId = mpId

	resp, err := c.dbfc.DescribeVscMountPoints(dvmp)
	if err != nil {
		c.updateToken()
		resp, err = c.dbfc.DescribeVscMountPoints(dvmp)
		if err != nil {
			return &VscMountPointResp{}, err
		}
	}
	result := c.rc.Convert2VscMountPointResp(resp)
	return result, nil
}

type VscMountPointResp struct {
	RequestID  string
	TotalCount string
	NextToken  string
	VscInfos   []*FlatVscMountPoint
}

type FlatVscMountPoint struct {
	InstanceId       *string
	MountPointDomain *string
	Status           PovStatus
	VscId            *string
	VscType          *string
}

type PovStatus int

const (
	NORMAL   PovStatus = iota // NORMAL = 0
	CREATING                  // CREATING = 1
	INVALID                   // INVALID = 2
)

func (vs PovStatus) String() string {
	return []string{"NORMAL", "CREATING", "INVALID"}[vs]
}
