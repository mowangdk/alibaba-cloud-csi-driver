//go:build !windows

package pov

import (
	"strings"

	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/cloud/metadata"
	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/common"
	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/options"
	"github.com/kubernetes-sigs/alibaba-cloud-csi-driver/pkg/utils"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	DriverName = "povplugin.csi.alibabacloud.com"
	// kvcsDriverName is served by the POV driver as well, until a dedicated
	// KVCS plugin exists.
	kvcsDriverName = "kvcsplugin.csi.alibabacloud.com"
)

var GlobalConfigVar GlobalConfig

// NewServers returns the CSI servers of the POV driver (Pangu Over Virtio):
// the same POV services are served under both the povplugin and the
// kvcsplugin driver names, each on its own endpoint. The kvcs endpoint is
// derived by replacing the pov driver name in the given endpoint.
func NewServers(meta *metadata.Metadata, endpoint string, serviceType utils.ServiceType) []common.NamedServer {
	newGlobalConfig(meta, serviceType)

	servers := []common.NamedServer{{
		DriverName: DriverName,
		Endpoint:   endpoint,
		Servers:    newDriverServers(meta, serviceType, DriverName),
	}}

	if !strings.Contains(endpoint, DriverName) {
		klog.Warningf("NewServers: endpoint %s does not contain %s, skip serving %s", endpoint, DriverName, kvcsDriverName)
		return servers
	}
	kvcsEndpoint := strings.ReplaceAll(endpoint, DriverName, kvcsDriverName)
	return append(servers, common.NamedServer{
		DriverName: kvcsDriverName,
		Endpoint:   kvcsEndpoint,
		Servers:    newDriverServers(meta, serviceType, kvcsDriverName),
	})
}

func newDriverServers(meta *metadata.Metadata, serviceType utils.ServiceType, driverName string) *common.Servers {
	var servers common.Servers
	servers.IdentityServer = newIdentityServer(driverName)
	if serviceType&utils.Controller != 0 {
		cs := newControllerService()
		servers.ControllerServer = &cs
	}
	if serviceType&utils.Node != 0 {
		ns := newNodeService(meta)
		servers.NodeServer = &ns
	}

	return &servers
}

func newGlobalConfig(meta *metadata.Metadata, serviceType utils.ServiceType) {
	cfg, err := options.GetRestConfig()
	if err != nil {
		klog.Fatalf("newGlobalConfig: build kubeconfig failed: %v", err)
	}
	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building kubernetes clientset: %s", err.Error())
	}

	GlobalConfigVar = GlobalConfig{
		client:   kubeClient,
		regionID: metadata.MustGet(meta, metadata.RegionID),
	}
}

type GlobalConfig struct {
	regionID          string
	controllerService bool
	client            kubernetes.Interface
}
