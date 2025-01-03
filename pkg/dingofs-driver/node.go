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
	"path"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	k8sexec "k8s.io/utils/exec"
	"k8s.io/utils/mount"

	"github.com/jackblack369/dingofs-csi/pkg/config"
	"github.com/jackblack369/dingofs-csi/pkg/k8sclient"
	"github.com/jackblack369/dingofs-csi/pkg/util"
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
	log := klog.NewKlogr().WithName("NodePublishVolume")
	if volCtx != nil && volCtx[config.PodInfoName] != "" {
		log = log.WithValues("appName", volCtx[config.PodInfoName])
	}
	volumeID := req.GetVolumeId()
	log = log.WithValues("volumeId", volumeID)

	ctx = util.WithLog(ctx, log)

	// WARNING: debug only, secrets included
	log.V(1).Info("called with args", "args", req)

	target := req.GetTargetPath()
	if len(target) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path not provided")
	}

	volCap := req.GetVolumeCapability()
	if volCap == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capability not provided")
	}
	log.Info("get volume_capability", "volCap", *volCap)

	if !isValidVolumeCapabilities([]*csi.VolumeCapability{volCap}) {
		return nil, status.Error(codes.InvalidArgument, "Volume capability not supported")
	}

	log.Info("creating dir", "target", target)
	if err := d.provider.CreateTarget(ctx, target); err != nil {
		return nil, status.Errorf(codes.Internal, "Could not create dir %q: %v", target, err)
	}

	options := []string{}
	if req.GetReadonly() || req.VolumeCapability.AccessMode.GetMode() == csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY {
		options = append(options, "ro")
	}
	if m := volCap.GetMount(); m != nil {
		// get mountOptions from PV.spec.mountOptions or StorageClass.mountOptions
		options = append(options, m.MountFlags...)
	}
	klog.Infof("get volume context, volCtx:%#v", volCtx)

	secrets := req.Secrets

	// get mountOptions from PV.volumeAttributes or StorageClass.parameters
	mountOptions := []string{}
	if opts, ok := volCtx["mountOptions"]; ok {
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
	log.Info("mounting dingofs", "secret", reflect.ValueOf(secrets).MapKeys(), "options", mountOptions)
	dfs, err := d.provider.DfsMount(ctx, volumeID, target, secrets, volCtx, mountOptions)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not mount dingofs: %v", err)
	}

	bindSource, err := dfs.CreateVol(ctx, volumeID, volCtx["subPath"])
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not create volume: %s, %v", volumeID, err)
	}

	if err := dfs.BindTarget(ctx, bindSource, target); err != nil {
		return nil, status.Errorf(codes.Internal, "Could not bind %q at %q: %v", bindSource, target, err)
	}

	if cap, exist := volCtx["capacity"]; exist {
		capacity, err := strconv.ParseInt(cap, 10, 64)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "invalid capacity %s: %v", cap, err)
		}
		settings := dfs.GetSetting()
		if settings.PV != nil {
			capacity = settings.PV.Spec.Capacity.Storage().Value()
		}
		quotaPath := settings.SubPath
		var subdir string
		for _, o := range settings.MountOptions {
			pair := strings.Split(o, "=")
			if len(pair) != 2 {
				continue
			}
			if pair[0] == "subdir" {
				subdir = path.Join("/", pair[1])
			}
		}

		go func() {
			err := retry.OnError(retry.DefaultRetry, func(err error) bool { return true }, func() error {
				return d.provider.SetQuota(context.Background(), secrets, settings, path.Join(subdir, quotaPath), capacity)
			})
			if err != nil {
				log.Error(err, "set quota failed")
			}
		}()
	}

	log.Info("dingofs volume mounted", "volumeId", volumeID, "target", target)
	return &csi.NodePublishVolumeResponse{}, nil
}

// NodeUnpublishVolume is a reverse operation of NodePublishVolume. This RPC is typically called by the CO when the workload using the volume is being moved to a different node, or all the workload using the volume on a node has finished.
func (d *nodeService) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	log := klog.NewKlogr().WithName("NodeUnpublishVolume")
	log.V(1).Info("called with args", "args", req)

	target := req.GetTargetPath()
	if len(target) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path not provided")
	}

	volumeId := req.GetVolumeId()
	log.Info("get volume_id", "volumeId", volumeId)

	err := d.provider.DfsUnmount(ctx, volumeId, target)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not unmount %q: %v", target, err)
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
