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
	"github.com/google/uuid"
	"os"
	"path/filepath"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/jackblack369/dingofs-csi/pkg/csicommon"
	"github.com/jackblack369/dingofs-csi/pkg/util"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	"k8s.io/utils/mount"
)

type nodeServer struct {
	*csicommon.DefaultNodeServer
	mounter     mount.Interface
	mountRecord map[string]string // targetPath -> a uuid
}

func (ns *nodeServer) NodePublishVolume(
	ctx context.Context,
	req *csi.NodePublishVolumeRequest,
) (*csi.NodePublishVolumeResponse, error) {
	mountUUID := uuid.New().String()
	volumeContext := req.GetVolumeContext()
	klog.V(5).Infof("%s: called with args %+v", util.GetCurrentFuncName(), *req)
	volumeID := req.GetVolumeId()
	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path is missing")
	}
	if !util.ValidateCharacter([]string{targetPath}) {
		return nil, status.Errorf(codes.InvalidArgument, "Illegal TargetPath: %s", targetPath)
	}
	mountPath := filepath.Join(PodMountBase, mountUUID, volumeID)
	isNotMounted, _ := mount.IsNotMountPoint(ns.mounter, mountPath)
	if !isNotMounted {
		klog.V(5).Infof("%s is already mounted", mountPath)
		return &csi.NodePublishVolumeResponse{}, nil
	}
	err := util.CreatePath(mountPath)
	if err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"Failed to create mount point path %s, err: %v",
			mountPath,
			err,
		)
	}

	secrets := req.Secrets

	curvefsTool := NewCurvefsTool()
	err = curvefsTool.CreateFs(volumeContext, secrets)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Create fs failed: %v", err)
	}

	curvefsMounter := NewCurvefsMounter()

	klog.V(1).Infof("mountPath: %s", mountPath)
	mountOption := req.GetVolumeCapability().GetMount()
	err = curvefsMounter.MountFs(mountPath, volumeContext, mountOption, mountUUID, secrets)
	if err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"Failed to mount dingofs by mount point [ %s ], err: %v",
			volumeID,
			err,
		)
	}

	isNotMounted, _ = mount.IsNotMountPoint(ns.mounter, mountPath)
	if isNotMounted {
		return nil, status.Errorf(
			codes.Internal,
			"Mount check failed, mountPath: %s",
			mountPath,
		)
	}

	err = util.CreatePath(targetPath)
	if err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"Failed to create target path %s, err: %v",
			targetPath,
			err,
		)
	}

	ns.mountRecord[targetPath] = mountUUID

	// bind mount point to target path
	if err := ns.mounter.Mount(mountPath, targetPath, "none", []string{"bind"}); err != nil {
		err := os.Remove(targetPath)
		if err != nil {
			return nil, err
		}
		return nil, status.Errorf(
			codes.Internal,
			"Failed to mount %s to %s, err: %v",
			mountPath,
			targetPath,
			err,
		)
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

func (ns *nodeServer) NodeUnpublishVolume(
	ctx context.Context,
	req *csi.NodeUnpublishVolumeRequest,
) (*csi.NodeUnpublishVolumeResponse, error) {
	klog.V(5).Infof("%s: called with args %+v", util.GetCurrentFuncName(), *req)
	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path is missing")
	}
	if !util.ValidateCharacter([]string{targetPath}) {
		return nil, status.Errorf(codes.InvalidArgument, "Illegal TargetPath: %s", targetPath)
	}

	isNotMounted, _ := mount.IsNotMountPoint(ns.mounter, targetPath)
	if isNotMounted {
		klog.V(5).Infof("%s is not mounted", targetPath)
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}

	curvefsMounter := NewCurvefsMounter()
	mountUUID := ns.mountRecord[targetPath]
	err := curvefsMounter.UmountFs(targetPath, mountUUID)
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"Failed to umount %s, err: %v",
			targetPath,
			err,
		)
	}

	isNotMounted, _ = mount.IsNotMountPoint(ns.mounter, targetPath)
	if !isNotMounted {
		return nil, status.Errorf(
			codes.Internal,
			"Umount check failed, targetPath: %s",
			targetPath,
		)
	}
	delete(ns.mountRecord, targetPath)
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (ns *nodeServer) NodeGetInfo(
	ctx context.Context,
	req *csi.NodeGetInfoRequest,
) (*csi.NodeGetInfoResponse, error) {
	klog.V(5).Infof("%s: called with args %+v", util.GetCurrentFuncName(), *req)
	return &csi.NodeGetInfoResponse{
		NodeId: ns.Driver.NodeID,
	}, nil
}

func (ns *nodeServer) NodeGetCapabilities(
	ctx context.Context,
	req *csi.NodeGetCapabilitiesRequest,
) (*csi.NodeGetCapabilitiesResponse, error) {
	klog.V(5).Infof("%s: called with args %+v", util.GetCurrentFuncName(), *req)
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{},
	}, nil
}
