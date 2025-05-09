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
	"os"
	"path/filepath"
	"strconv"

	"github.com/google/uuid"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/jackblack369/dingofs-csi/pkg/csicommon"
	"github.com/jackblack369/dingofs-csi/pkg/util"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	"k8s.io/mount-utils"
)

type nodeServer struct {
	*csicommon.DefaultNodeServer
	mounter     mount.Interface
	mountRecord map[string]map[string]string // targetPath -> a uuid : mountUUID, pid: mountPID
}

func (ns *nodeServer) NodePublishVolume(
	ctx context.Context,
	req *csi.NodePublishVolumeRequest,
) (*csi.NodePublishVolumeResponse, error) {
	mountUUID := uuid.New().String()
	volumeContext := req.GetVolumeContext()
	klog.Infof("%s: called with args %+v", util.GetCurrentFuncName(), *req)
	volumeID := req.GetVolumeId()
	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path is missing")
	}
	if !util.ValidateCharacter([]string{targetPath}) {
		return nil, status.Errorf(codes.InvalidArgument, "Illegal TargetPath: %s", targetPath)
	}
	mountPath := filepath.Join(PodMountBase, mountUUID)
	isNotMounted, _ := mount.IsNotMountPoint(ns.mounter, mountPath)
	if !isNotMounted {
		klog.V(5).Infof("%s is already mounted", mountPath)
		return &csi.NodePublishVolumeResponse{}, nil
	}

	secrets := req.Secrets

	curvefsTool := NewCurvefsTool()
	err := curvefsTool.CreateFs(volumeContext, secrets)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Create fs failed: %v", err)
	}

	curvefsMounter := NewCurvefsMounter()

	klog.V(1).Infof("mountPath: %s", mountPath)
	mountOption := req.GetVolumeCapability().GetMount()
	pid, err := curvefsMounter.MountFs(mountPath, volumeContext, mountOption, mountUUID, secrets)
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

	dataPath := filepath.Join(PodMountBase, mountUUID, volumeID)
	err = util.CreatePath(dataPath)
	if err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"Failed to create data path %s, err: %v",
			dataPath,
			err,
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

	ns.mountRecord[targetPath] = map[string]string{
		"mountUUID": mountUUID,
		"pid":       strconv.Itoa(pid),
		"cacheDirs": curvefsMounter.mounterParams["cache_dir"],
	}

	// bind data path to target path
	klog.V(1).Infof("bind %s to %s", dataPath, targetPath)
	if err := ns.mounter.Mount(dataPath, targetPath, "none", []string{"bind"}); err != nil {
		err := os.Remove(targetPath)
		if err != nil {
			return nil, err
		}
		return nil, status.Errorf(
			codes.Internal,
			"Failed to bind %s to %s, err: %v",
			dataPath,
			targetPath,
			err,
		)
	}

	// config volume quota
	if capacityStr, exist := volumeContext["capacity"]; exist {
		capacityBytes, err := strconv.ParseInt(capacityStr, 10, 64)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "invalid capacity %s: %v", capacityStr, err)
		}
		// convert byte to GB
		capacityGB := util.ByteToGB(capacityBytes)
		err = curvefsTool.SetVolumeQuota(curvefsTool.ToolParams["mdsaddr"], volumeID, secrets["name"], strconv.FormatInt(capacityGB, 10), volumeContext["inodes"])
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Set volume quota failed: %v", err)
		}
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
	mountUUID := ns.mountRecord[targetPath]["mountUUID"]
	cacheDirs := ns.mountRecord[targetPath]["cacheDirs"]
	err := curvefsMounter.UmountFs(targetPath, mountUUID, cacheDirs)
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

	// kill process by pid
	pid, err := strconv.Atoi(ns.mountRecord[targetPath]["pid"])
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to convert pid to int, err: %v", err)
	}
	err = util.KillProcess(pid)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to kill process, err: %v", err)
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
