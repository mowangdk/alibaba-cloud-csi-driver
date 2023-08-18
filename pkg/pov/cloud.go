package pov

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/pov/cloud"
)

const (
	MetadataURL = "http://100.100.100.200/latest/meta-data/"
	DocumentURL = "http://100.100.100.200/latest/dynamic/instance-identity/document"

	defaultRetryCount = 5
)

type MetadataService interface {
	GetDoc() (*InstanceDocument, error)
}

// MetadataService ...
type metadataService struct {
	retryCount int
}

func NewMetadataService() MetadataService {
	return metadataService{
		retryCount: defaultRetryCount,
	}
}

type InstanceDocument struct {
	RegionID   string `json:"region-id"`
	InstanceID string `json:"instance-id"`
	ZoneID     string `json:"zone-id"`
}

func (ms metadataService) getDoc() (*InstanceDocument, error) {
	resp, err := http.Get(DocumentURL)
	if err != nil {
		return &InstanceDocument{}, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return &InstanceDocument{}, err
	}

	result := &InstanceDocument{}
	if err = json.Unmarshal(body, result); err != nil {
		return &InstanceDocument{}, err
	}
	return result, nil
}

func (ms metadataService) GetDoc() (*InstanceDocument, error) {
	var err error
	var doc *InstanceDocument
	for i := 0; i < ms.retryCount; i++ {
		doc, err = ms.getDoc()
		if err != nil {
			continue
		}
		return doc, nil
	}
	return doc, err
}

type Cloud interface {
	CreateVolume(ctx context.Context, volumeName string, diskOptions *cloud.PovOptions) (fsId, requestID string, err error)
	DeleteVolume(ctx context.Context, volumeName string) (reuqestID string, err error)
	CreateVolumeMountPoint(ctx context.Context, filesystemID string) (mpId string, err error)
	AttachVscMountPoint(ctx context.Context, mpId, fsId, instanceID string) (requestID string, err error)
	DescribeFilesystemMountPoints(ctx context.Context, fsId string) (dvmpr *cloud.VscMountPointResp, err error)
	DescribeMountPointVscIds(ctx context.Context, fsId, mpId string) (dvmpr *cloud.VscMountPointResp, err error)
	DetachVscMountPoint(ctx context.Context, mpId, filesystemID, instanceID, vscType, vscId string) (requestID string, err error)
}

func newCloud(povStorage PovStorage) (Cloud, error) {
	switch povStorage {
	case PovStorageDFS:
		return cloud.NewDFSCloud(GlobalConfigVar.regionID)
	case PovStorageCPFS:
		return cloud.NewCPFSCloud(GlobalConfigVar.regionID)
	default:
		return nil, fmt.Errorf("not support pov storage type: %s", povStorage)
	}
}
