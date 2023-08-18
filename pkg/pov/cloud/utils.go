package cloud

import (
	cpfs "github.com/alibabacloud-go/nas-20170626/v3/client"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/dfs"
)

type Converter interface {
	Convert2VscMountPointResp(obj interface{}) *VscMountPointResp
}

type cpfsConvert struct{}

func (cpfsc *cpfsConvert) Convert2VscMountPointResp(obj interface{}) *VscMountPointResp {

	result := &VscMountPointResp{}
	cpfsList := obj.([]cpfs.DescribeVscMountPointAttachInfoResponse)

	mountPointVscIds := []*FlatVscMountPoint{}
	for _, resp := range cpfsList {
		for _, mp := range resp.Body.DescVscAttachInfos.DescVscAttachInfo {
			vmp := &FlatVscMountPoint{}
			vmp.MountPointDomain = mp.MountPointDomain
			vmp.InstanceId = mp.InstanceId
			switch *mp.Status {
			case "NORMAL":
				vmp.Status = NORMAL
			case "CREATING":
				vmp.Status = CREATING
			case "INVALID":
				vmp.Status = INVALID
			}
			vmp.VscId = mp.VscId
			vmp.VscType = mp.VscType
		}
	}
	result.VscInfos = mountPointVscIds
	return result
}

type dfsConvert struct{}

func (dfsc dfsConvert) Convert2VscMountPointResp(obj interface{}) *VscMountPointResp {

	result := &VscMountPointResp{}
	resp := obj.(dfs.DescribeVscMountPointsResponse)
	vscInfos := []*FlatVscMountPoint{}
	for _, mp := range resp.MountPoints {
		if len(mp.Instances) == 0 {
			vmp := &FlatVscMountPoint{}
			vmp.MountPointDomain = &mp.MountPointDomain
			vscInfos = append(vscInfos, vmp)
			continue
		}
		for _, i := range mp.Instances {
			if len(i.Vscs) == 0 {
				vmp := &FlatVscMountPoint{}
				vmp.MountPointDomain = &mp.MountPointDomain
				vmp.InstanceId = &i.InstanceId
				vscInfos = append(vscInfos, vmp)
				continue
			}
			for _, vsc := range i.Vscs {
				vmp := &FlatVscMountPoint{}
				vmp.MountPointDomain = &mp.MountPointDomain
				vmp.InstanceId = &i.InstanceId
				vmp.VscId = &vsc.VscId
				vmp.VscType = &vsc.VscType
				switch vsc.VscStatus {
				case "NORMAL":
					vmp.Status = NORMAL
				case "CREATING":
					vmp.Status = CREATING
				case "INVALID":
					vmp.Status = INVALID
				}
				vscInfos = append(vscInfos, vmp)
			}
		}
	}
	result.VscInfos = vscInfos
	result.RequestID = resp.RequestId
	result.TotalCount = string(resp.TotalCount)
	return result
}
