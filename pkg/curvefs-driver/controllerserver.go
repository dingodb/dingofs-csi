/*
Copyright 2022 The Curve Authors

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

package curvefsdriver

import (
	"context"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/jackblack369/dingofs-csi/pkg/csicommon"
	"github.com/jackblack369/dingofs-csi/pkg/util"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"strconv"
)

type controllerServer struct {
	*csicommon.DefaultControllerServer
	driver     *CurvefsDriver
	kubeClient kubernetes.Interface
}

var vols = map[string]*csi.Volume{}

func (cs *controllerServer) CreateVolume(
	ctx context.Context,
	req *csi.CreateVolumeRequest,
) (*csi.CreateVolumeResponse, error) {
	klog.V(5).Infof("%s: called with args %+v", util.GetCurrentFuncName(), *req)
	name := req.GetName()
	if len(name) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume Name is missing")
	}
	volCaps := req.GetVolumeCapabilities()
	if len(volCaps) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume capabilities is missing")
	}
	volumeID := name
	if value, ok := vols[volumeID]; ok && value != nil {
		klog.Infof("%s has beed already created: %v", volumeID, value)
		return &csi.CreateVolumeResponse{Volume: value}, nil
	}
	capacity := req.GetCapacityRange().GetRequiredBytes()
	// call curvefs_tool create-fs
	//curvefsTool := NewCurvefsTool()
	//err := curvefsTool.CreateFs(volumeID, capacity, req.GetParameters())
	//if err != nil {
	//	return nil, status.Errorf(codes.Internal, "Create fs failed: %v", err)
	//}

	// set volume context
	volumeContext := make(map[string]string)
	for k, v := range req.Parameters {
		volumeContext[k] = v
	}

	volumeContext["capacity"] = strconv.FormatInt(capacity, 10)

	volume := csi.Volume{
		VolumeId:      volumeID,
		CapacityBytes: capacity,
		VolumeContext: volumeContext,
	}
	vols[volumeID] = &volume
	return &csi.CreateVolumeResponse{Volume: &volume}, nil
}

func (cs *controllerServer) DeleteVolume(
	ctx context.Context,
	req *csi.DeleteVolumeRequest,
) (*csi.DeleteVolumeResponse, error) {
	klog.V(5).Infof("%s: called with args %+v", util.GetCurrentFuncName(), *req)
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID is missing")
	}
	// call curvefs_tool delete-fs
	//curvefsTool := NewCurvefsTool()
	//pvInfo, err := cs.kubeClient.CoreV1().
	//	PersistentVolumes().
	//	Get(context.Background(), req.VolumeId, metav1.GetOptions{})
	//params := pvInfo.Spec.CSI.VolumeAttributes
	//err = curvefsTool.DeleteFs(volumeID, params) // todo replace delete volume with delete fs
	//if err != nil {
	//	return nil, status.Errorf(codes.Internal, "Delete fs failed: %v", err)
	//}
	//if _, ok := vols[req.VolumeId]; ok {
	//	delete(vols, req.VolumeId)
	//}
	return &csi.DeleteVolumeResponse{}, nil
}

func (cs *controllerServer) ValidateVolumeCapabilities(
	ctx context.Context,
	req *csi.ValidateVolumeCapabilitiesRequest,
) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	klog.V(5).Infof("%s: called with args %+v", util.GetCurrentFuncName(), *req)
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID is missing")
	}
	volCaps := req.GetVolumeCapabilities()
	if len(volCaps) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume capabilities is missing")
	}
	var confirmed *csi.ValidateVolumeCapabilitiesResponse_Confirmed
	if cs.isValidVolumeCapabilities(volCaps) {
		confirmed = &csi.ValidateVolumeCapabilitiesResponse_Confirmed{VolumeCapabilities: volCaps}
	}
	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: confirmed,
	}, nil
}

func (cs *controllerServer) isValidVolumeCapabilities(volCaps []*csi.VolumeCapability) bool {
	hasSupport := func(cap *csi.VolumeCapability) bool {
		if cap.GetBlock() != nil {
			return false
		}
		for _, c := range cs.driver.GetVolumeCapabilityAccessModes() {
			if c.GetMode() == cap.AccessMode.GetMode() {
				return true
			}
		}
		return false
	}

	allSupport := true
	for _, c := range volCaps {
		if !hasSupport(c) {
			allSupport = false
		}
	}
	return allSupport
}
