package pov

import (
	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/cloud/metadata"
	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/common"
	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/utils"
)

const (
	DriverName = "povplugin.csi.alibabacloud.com"
)

func NewServers(_ *metadata.Metadata, _ string, _ utils.ServiceType) []common.NamedServer {
	panic("POV driver is not supported on Windows")
}
