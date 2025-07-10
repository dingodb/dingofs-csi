/*
 * Copyright (c) 2024 dingodb.com, Inc. All Rights Reserved
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package dingofsdriver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	k8sexec "k8s.io/utils/exec"

	"github.com/jackblack369/dingofs-csi/pkg/config"
	"github.com/jackblack369/dingofs-csi/pkg/k8sclient"
	"github.com/jackblack369/dingofs-csi/pkg/util"
	"k8s.io/mount-utils"
)

var (
	nodeCaps = []csi.NodeServiceCapability_RPC_Type{csi.NodeServiceCapability_RPC_GET_VOLUME_STATS}
)

const defaultCheckTimeout = 2 * time.Second

type nodeService struct {
	mount.SafeFormatAndMount
	provider  Provider
	nodeID    string
	k8sClient *k8sclient.K8sClient
}

func newNodeService(nodeID string, k8sClient *k8sclient.K8sClient) (*nodeService, error) {
	parseNodeConfig()
	mounter := &mount.SafeFormatAndMount{
		Interface: mount.New(""),
		Exec:      k8sexec.New(),
	}
	dfsProvider := NewDfsProvider(mounter, k8sClient)
	return &nodeService{
		SafeFormatAndMount: *mounter,
		provider:           dfsProvider,
		nodeID:             nodeID,
		k8sClient:          k8sClient,
	}, nil
}

// A map for locking/unlocking a target path for NodePublish/NodeUnpublish
// calls. The key is target path and value is a boolean true in case there
// is any NodePublishVolume or NodeUnpublishVolume request in progress for
// the target path.
var nodePublishUnpublishLock map[string]bool

// a mutex var used to make sure certain code blocks are executed by
// only one goroutine at a time.
var mutex sync.Mutex

func lock(targetPath string, ctx context.Context) bool {
	mutex.Lock()
	defer mutex.Unlock()

	if len(nodePublishUnpublishLock) == 0 {
		nodePublishUnpublishLock = make(map[string]bool)
	}

	if _, exists := nodePublishUnpublishLock[targetPath]; exists {
		return false
	}
	nodePublishUnpublishLock[targetPath] = true
	klog.Infof("The target path is locked for NodePublish/NodeUnpublish: [%s]", targetPath)
	return true
}

func unlock(targetPath string, ctx context.Context) {
	mutex.Lock()
	defer mutex.Unlock()
	delete(nodePublishUnpublishLock, targetPath)
	klog.Infof("The target path is unlocked for NodePublish/NodeUnpublish: [%s]", targetPath)
}

// NodeStageVolume is called by the CO prior to the volume being consumed by any workloads on the node by `NodePublishVolume`
func (d *nodeService) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// NodeUnstageVolume is a reverse operation of `NodeStageVolume`
func (d *nodeService) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// NodePublishVolume is called by the CO when a workload that wants to use the specified volume is placed (scheduled) on a node
func (d *nodeService) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	volCtx := req.GetVolumeContext()
	klog.Infof("get volume context, volCtx:%#v", volCtx)

	log := klog.NewKlogr().WithName("NodePublishVolume")
	if volCtx != nil && volCtx[config.PodInfoName] != "" {
		log = log.WithValues("appName", volCtx[config.PodInfoName])
	}
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volumeID must be provided")
	}
	log = log.WithValues("volumeId", volumeID)

	ctx = util.WithLog(ctx, log)

	// WARNING: debug only, secrets included
	reqJson, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		log.Error(err, "Failed to marshal req to JSON")
	} else {
		log.Info("req JSON:", string(reqJson))
	}

	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path not provided")
	}

	lockSuccess := lock(targetPath, ctx)
	if !lockSuccess {
		message := fmt.Sprintf("NodePublishVolume - another NodePublish/NodeUnpublish is in progress for the targetPath: [%s]", targetPath)
		klog.Errorf("[%s] ", message)
		return nil, status.Error(codes.Internal, message)
	} else {
		defer unlock(targetPath, ctx)
	}

	volCap := req.GetVolumeCapability()
	if volCap == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capability not provided")
	}
	log.Info("get volume_capability", "volCap", *volCap)

	if !isValidVolumeCapabilities([]*csi.VolumeCapability{volCap}) {
		return nil, status.Error(codes.InvalidArgument, "Volume capability not supported")
	}

	log.Info("creating dir", "target", targetPath)
	if err := d.provider.CreateTarget(ctx, targetPath); err != nil {
		return nil, status.Errorf(codes.Internal, "Could not create dir %q: %v", targetPath, err)
	}

	secrets := req.Secrets
	if secrets == nil || config.LocalPV {

		hostVolPath := ""
		if secrets != nil {
			subPath := strings.TrimPrefix(volumeID, "pvc-")
			hostVolPath = filepath.Join(config.DefaultHostDfsPath, secrets["name"], subPath)
		} else {
			dfsVol, err := util.GetDfsInfo(volumeID)
			if err != nil {
				return nil, status.Error(codes.InvalidArgument, "NodePublishVolume : volumeID is not in proper format")
			}
			hostVolPath = dfsVol.MountPath
		}
		// use local pv
		klog.Infof("use local pv mode, volumeID: %s, targetPath: %s, hostPath: %s", volumeID, targetPath, hostVolPath)

		volPathInContainer := config.HostContainerDir + hostVolPath
		//_, err = os.Lstat(volPathInContainer)
		//if err != nil {
		//	klog.Errorf("NodePublishVolume - lstat [%s] failed with error [%v]", volPathInContainer, err)
		//	return nil, fmt.Errorf("NodePublishVolume - lstat [%s] failed with error [%v]", volPathInContainer, err)
		//}
		if _, err := os.Stat(volPathInContainer); os.IsNotExist(err) {
			klog.Infof("NodePublishVolume - creating directory [%s] for local pv", volPathInContainer)
			if err := os.MkdirAll(volPathInContainer, os.FileMode(0755)); err != nil {
				return nil, fmt.Errorf("NodePublishVolume - create [%s] failed with error [%v]", volPathInContainer, err)
			}
		}

		mounter := &mount.Mounter{}
		mntPoint, err := mounter.IsMountPoint(targetPath)
		if err != nil {
			if os.IsNotExist(err) {
				if err = os.Mkdir(targetPath, 0750); err != nil {
					klog.Errorf("NodePublishVolume - targetPath [%s] creation failed with error [%v]", targetPath, err)
					return nil, fmt.Errorf("NodePublishVolume - targetPath [%s] creation failed with error [%v]", targetPath, err)
				} else {
					klog.Infof("NodePublishVolume - the target directory [%s] is created successfully", targetPath)
				}
			} else {
				klog.Errorf("NodePublishVolume - targetPath [%s] check failed with error [%v]", targetPath, err)
				return nil, fmt.Errorf("NodePublishVolume - targetPath [%s] check failed with error [%v]", targetPath, err)
			}
		}
		if mntPoint {
			klog.Infof("NodePublishVolume - [%s] is already a mount point", targetPath)
			return &csi.NodePublishVolumeResponse{}, nil
		}

		// check current host is running current dingofs mount system service , if not, bootstrap this dingofs mount system service used by job
		// TODO

		// create bind mount
		options := []string{"bind"}
		klog.Infof("NodePublishVolume - creating bind mount [%v] -> [%v]", targetPath, hostVolPath)
		if err := mounter.Mount(volPathInContainer, targetPath, "", options); err != nil {
			klog.Errorf("NodePublishVolume - mounting [%s] at [%s] failed with error [%v]", hostVolPath, targetPath, err)
			return nil, fmt.Errorf("NodePublishVolume - mounting [%s] at [%s] failed with error [%v]", hostVolPath, targetPath, err)
		}

		//check for the dingofs type again, if not dingofs type, unmount and return error.
		err = util.CheckDfsType(ctx, volPathInContainer)
		if err != nil {
			uerr := mounter.Unmount(targetPath)
			if uerr != nil {
				klog.Errorf("NodePublishVolume - unmount [%s] failed with error [%v]", targetPath, uerr)
				return nil, fmt.Errorf("NodePublishVolume - unmount [%s] failed with error [%v]", targetPath, uerr)
			}
			return nil, err
		}
		klog.Infof("NodePublishVolume - successfully mounted [%s]", targetPath)
		return &csi.NodePublishVolumeResponse{}, nil
	}

	options := []string{}
	if req.GetReadonly() || req.VolumeCapability.AccessMode.GetMode() == csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY {
		options = append(options, "ro")
	}

	// get mountOptions from PV.spec.mountOptions or StorageClass.mountOptions
	if m := volCap.GetMount(); m != nil {
		klog.Infof("volCap.getMount result:%#v", m)
		options = append(options, m.MountFlags...)
	}

	// get mountOptions from PV.volumeAttributes or StorageClass.parameters
	mountOptions := []string{}
	if opts, ok := volCtx["mountOptions"]; ok {
		klog.Infof("get mountOptions from StorageClass.parameters, mountOptions:%s", opts)
		mountOptions = strings.Split(opts, ",")
	}
	mountOptions = append(mountOptions, options...)

	// mound pod to mounting dingofs. e.g
	// /usr/local/bin/dingofs redis://:xxx /dfs/pvc-7175fc74-d52d-46bc-94b3-ad9296b726cd-alypal -o metrics=0.0.0.0:9567
	// /dingofs/client/sbin/dingo-fuse \
	// -f \
	// -o default_permissions \
	// -o allow_other \
	// -o fsname=test \
	// -o fstype=s3 \
	// -o user=curvefs \
	// -o conf=/dingofs/client/conf/client.conf \
	// /dingofs/client/mnt/mnt/mp-1

	//check mountOptions does have DefaultS3LogPrefixKey or not
	foundLogPrefix := false
	foundLogDir := false
	for _, item := range mountOptions {
		if strings.HasPrefix(item, config.DefaultS3LogPrefixKey+"=") {
			foundLogPrefix = true
		}
		if strings.HasPrefix(item, config.DefaultClientCommonLogDirKey+"=") {
			foundLogPrefix = true
		}
	}

	if !foundLogPrefix {
		mountOptions = append(mountOptions, fmt.Sprintf("%s=%s/%s", config.DefaultS3LogPrefixKey, config.DefaultS3LogPrefixVal, volumeID))
	}
	if !foundLogDir {
		mountOptions = append(mountOptions, fmt.Sprintf("%s=%s/%s", config.DefaultClientCommonLogDirKey, config.DefaultClientCommonLogDirVal, volumeID))
	}

	log.Info("mounting dingofs", "mountOptions", mountOptions)
	dfs, err := d.provider.DfsMount(ctx, volumeID, targetPath, secrets, volCtx, mountOptions)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not mount dingofs: %v", err)
	}

	bindSource, err := dfs.CreateVol(ctx, volumeID, volCtx["subPath"])
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not create volume: %s, %v", volumeID, err)
	}

	if err := dfs.BindTarget(ctx, bindSource, targetPath); err != nil {
		return nil, status.Errorf(codes.Internal, "Could not bind %q at %q: %v", bindSource, targetPath, err)
	}

	// below code which is used to set quota is implemented in CreateFS
	//if cap, exist := volCtx["capacity"]; exist {
	//	capacity, err := strconv.ParseInt(cap, 10, 64)
	//	if err != nil {
	//		return nil, status.Errorf(codes.Internal, "invalid capacity %s: %v", cap, err)
	//	}
	//	settings := dfs.GetSetting()
	//	if settings.PV != nil {
	//		capacity = settings.PV.Spec.Capacity.Storage().Value()
	//	}
	//	quotaPath := settings.SubPath
	//	var subdir string
	//	for _, o := range settings.MountOptions {
	//		pair := strings.Split(o, "=")
	//		if len(pair) != 2 {
	//			continue
	//		}
	//		if pair[0] == "subdir" {
	//			subdir = path.Join("/", pair[1])
	//		}
	//	}

	//	go func() {
	//		err := retry.OnError(retry.DefaultRetry, func(err error) bool { return true }, func() error {
	//			return d.provider.SetQuota(context.Background(), secrets, settings, path.Join(subdir, quotaPath), capacity)
	//		})
	//		if err != nil {
	//			log.Error(err, "set quota failed")
	//		}
	//	}()
	//}

	log.Info("dingofs volume mounted", "volumeId", volumeID, "target", targetPath)
	return &csi.NodePublishVolumeResponse{}, nil
}

// NodeUnpublishVolume is a reverse operation of NodePublishVolume. This RPC is typically called by the CO when the workload using the volume is being moved to a different node, or all the workload using the volume on a node has finished.
func (d *nodeService) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	log := klog.NewKlogr().WithName("NodeUnpublishVolume")
	// marshal req to json
	reqJson, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		log.Error(err, "Failed to marshal req to JSON")
	} else {
		log.Info("req JSON:", string(reqJson))
	}

	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path not provided")
	}

	volumeId := req.GetVolumeId()
	log.Info("get volume_id", "volumeId", volumeId)

	lockSuccess := lock(targetPath, ctx)
	if !lockSuccess {
		message := fmt.Sprintf("NodeUnpublishVolume - another NodePublish/NodeUnpublish is in progress for the targetPath: [%s]", targetPath)
		klog.Errorf(message)
		return nil, status.Error(codes.Internal, message)
	} else {
		defer unlock(targetPath, ctx)
	}

	_, err = util.GetDfsInfo(volumeId)
	// umount local pv
	if err == nil {
		//Check if target is bind mount and cleanup accordingly
		_, err := os.Lstat(targetPath)
		if err != nil {
			//Handling for target path is already deleted/not present
			if os.IsNotExist(err) {
				klog.Infof("NodeUnpublishVolume - targetPath [%s] is not found, returning success ", targetPath)
				return &csi.NodeUnpublishVolumeResponse{}, nil
			}
			//Handling for bindmount if filesystem is unmounted
			if strings.Contains(err.Error(), "stale NFS file handle") {
				klog.Warningf("NodeUnpublishVolume - unmount [%s] failed with error [%v]. trying forceful unmount", targetPath, err)
				needReturn, response, error := d.provider.UnmountAndDelete(ctx, targetPath, true)
				if needReturn {
					return response, error
				}
				klog.Infof("NodeUnpublishVolume - forced unmount [%s] is successful", targetPath)
				return &csi.NodeUnpublishVolumeResponse{}, nil
			} else {
				klog.Errorf("NodeUnpublishVolume - lstat [%s] failed with error [%v]", targetPath, err)
				return nil, status.Error(codes.Internal, fmt.Sprintf("NodeUnpublishVolume - lstat [%s] failed with error [%v]", targetPath, err))
			}
		}

		needReturn, response, error := d.provider.UnmountAndDelete(ctx, targetPath, false)
		if needReturn {
			return response, error
		}

		klog.Infof("NodeUnpublishVolume - successfully unpublished [%s]", targetPath)
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}

	err = d.provider.DfsUnmount(ctx, volumeId, targetPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not unmount %q: %v", targetPath, err)
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeGetCapabilities response node capabilities to CO
func (d *nodeService) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	log := klog.NewKlogr().WithName("NodeGetCapabilities")
	log.V(1).Info("called with args", "args", req)
	var caps []*csi.NodeServiceCapability
	for _, cap := range nodeCaps {
		c := &csi.NodeServiceCapability{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: cap,
				},
			},
		}
		caps = append(caps, c)
	}
	return &csi.NodeGetCapabilitiesResponse{Capabilities: caps}, nil
}

// NodeGetInfo is called by CO for the node at which it wants to place the workload. The result of this call will be used by CO in ControllerPublishVolume.
func (d *nodeService) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	log := klog.NewKlogr().WithName("NodeGetInfo")
	log.V(1).Info("called with args", "args", req)

	return &csi.NodeGetInfoResponse{
		NodeId: d.nodeID,
	}, nil
}

// NodeExpandVolume unimplemented
func (d *nodeService) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (d *nodeService) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	log := klog.NewKlogr().WithName("NodeGetVolumeStats")
	log.V(1).Info("called with args", "args", req)

	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}

	volumePath := req.GetVolumePath()
	if len(volumePath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume path not provided")
	}

	var exists bool

	err := util.DoWithTimeout(ctx, defaultCheckTimeout, func() (err error) {
		exists, err = mount.PathExists(volumePath)
		return
	})
	if err == nil {
		if !exists {
			log.Info("Volume path not exists", "volumePath", volumePath)
			return nil, status.Error(codes.NotFound, "Volume path not exists")
		}
		if d.SafeFormatAndMount.Interface != nil {
			var notMnt bool
			err := util.DoWithTimeout(ctx, defaultCheckTimeout, func() (err error) {
				notMnt, err = mount.IsNotMountPoint(d.SafeFormatAndMount.Interface, volumePath)
				return err
			})
			if err != nil {
				log.Info("Check volume path is mountpoint failed", "volumePath", volumePath, "error", err)
				return nil, status.Errorf(codes.Internal, "Check volume path is mountpoint failed: %s", err)
			}
			if notMnt { // target exists but not a mountpoint
				log.Info("volume path not mounted", "volumePath", volumePath)
				return nil, status.Error(codes.Internal, "Volume path not mounted")
			}
		}
	} else {
		log.Info("Check volume path %s, err: %s", "volumePath", volumePath, "error", err)
		return nil, status.Errorf(codes.Internal, "Check volume path, err: %s", err)
	}

	totalSize, freeSize, totalInodes, freeInodes := util.GetDiskUsage(volumePath)
	usedSize := int64(totalSize) - int64(freeSize)
	usedInodes := int64(totalInodes) - int64(freeInodes)

	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			{
				Available: int64(freeSize),
				Total:     int64(totalSize),
				Used:      usedSize,
				Unit:      csi.VolumeUsage_BYTES,
			},
			{
				Available: int64(freeInodes),
				Total:     int64(totalInodes),
				Used:      usedInodes,
				Unit:      csi.VolumeUsage_INODES,
			},
		},
	}, nil
}
