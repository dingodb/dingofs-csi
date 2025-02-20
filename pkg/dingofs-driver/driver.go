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
	"net"
	"os"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"

	"github.com/jackblack369/dingofs-csi/pkg/config"
	"github.com/jackblack369/dingofs-csi/pkg/k8sclient"
	"github.com/jackblack369/dingofs-csi/pkg/util"
)

var (
	driverLog = klog.NewKlogr().WithName("driver")
)

// Driver struct
type Driver struct {
	controllerService
	nodeService
	provisionerService

	srv      *grpc.Server
	endpoint string
}

// NewDriver creates a new driver
func NewDriver(endpoint string, nodeID string,
	leaderElection bool,
	leaderElectionNamespace string,
	leaderElectionLeaseDuration time.Duration) (*Driver, error) {
	// klog.Infof("get version info, driver:%s, verison:%s, commit:%s, date:%s", config.DriverName, driverVersion, gitCommit, buildDate)

	var k8sClient *k8sclient.K8sClient
	var err error
	k8sClient, err = k8sclient.NewClient()
	if err != nil {
		driverLog.Error(err, "Can't get k8s client")
		return nil, err
	}
	cs, err := newControllerService(k8sClient)
	if err != nil {
		return nil, err
	}

	ns, err := newNodeService(nodeID, k8sClient)
	if err != nil {
		return nil, err
	}

	ps, err := newProvisionerService(k8sClient, leaderElection, leaderElectionNamespace, leaderElectionLeaseDuration)
	if err != nil {
		return nil, err
	}

	return &Driver{
		controllerService:  cs,
		nodeService:        *ns,
		provisionerService: ps,
		endpoint:           endpoint,
	}, nil
}

// Run runs the server
func (d *Driver) Run() error {
	if config.Provisioner {
		go d.provisionerService.Run(context.Background())
	}

	scheme, addr, err := util.ParseEndpoint(d.endpoint)
	if err != nil {
		return err
	}

	listener, err := net.Listen(scheme, addr)
	if err != nil {
		return err
	}

	logErr := func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			driverLog.Error(err, "GRPC error")
		}
		return resp, err
	}
	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(logErr),
	}
	d.srv = grpc.NewServer(opts...)

	csi.RegisterIdentityServer(d.srv, d)
	csi.RegisterControllerServer(d.srv, d)
	csi.RegisterNodeServer(d.srv, d)

	driverLog.Info("Listening for connection on address", "address", listener.Addr())
	return d.srv.Serve(listener)
}

// Stop stops server
func (d *Driver) Stop() {
	driverLog.Info("Stopped server")
	d.srv.Stop()
}

func parseNodeConfig() {
	if os.Getenv("DRIVER_NAME") != "" {
		config.DriverName = os.Getenv("DRIVER_NAME")
	}

	config.NodeName = os.Getenv("NODE_NAME")
	config.Namespace = os.Getenv("DINGOFS_MOUNT_NAMESPACE")
	config.PodName = os.Getenv("POD_NAME")

	k8sclient, err := k8sclient.NewClient()
	if err != nil {
		klog.ErrorS(err, "Can't get k8s client")
		os.Exit(1)
	}
	pod, err := k8sclient.GetPod(context.TODO(), config.PodName, config.Namespace)
	if err != nil {
		klog.ErrorS(err, "Can't get pod", "pod", config.PodName)
		os.Exit(1)
	}

	config.CSIPod = *pod
	// print csi node pod.Spec.Volumes and pod.Spec.Containers[0].VolumeMounts
	// marshal VolumeMounts to json
	volumeMountsJSON, err := json.MarshalIndent(pod.Spec.Containers[0].VolumeMounts, "", "  ")
	if err != nil {
		klog.ErrorS(err, "Failed to marshal volume mounts to JSON")
	} else {
		klog.Infof("csi-driver volume mounts JSON: %s", string(volumeMountsJSON))
	}
	volumeJSON, err := json.MarshalIndent(pod.Spec.Volumes, "", "  ")
	if err != nil {
		klog.ErrorS(err, "Failed to marshal volumes to JSON")
	} else {
		klog.Infof("csi-driver pod volumes JSON: %s", string(volumeJSON))
	}
	// err = fuse.InitGlobalFds(context.TODO(), "/tmp")
	// if err != nil {
	// 	log.Error(err, "Init global fds error")
	// 	os.Exit(1)
	// }
}
