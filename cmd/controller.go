/*
 Copyright 2022 Juicedata Inc
 Copyright 2024 Dingodb.com

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

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/jackblack369/dingofs-csi/cmd/app"
	"github.com/jackblack369/dingofs-csi/pkg/config"
	dingofsdriver "github.com/jackblack369/dingofs-csi/pkg/dingofs-driver"
	k8s "github.com/jackblack369/dingofs-csi/pkg/k8sclient"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(corev1.AddToScheme(scheme))
}
func parseControllerConfig() {
	config.Provisioner = provisioner
	config.CacheClientConf = cacheConf
	if os.Getenv("DRIVER_NAME") != "" {
		config.DriverName = os.Getenv("DRIVER_NAME")
	}
	// enable mount manager by default in csi controller
	config.MountManager = true

	if dfsImmutable := os.Getenv("DINGOFS_IMMUTABLE"); dfsImmutable != "" {
		// check if running in an immutable environment
		if immutable, err := strconv.ParseBool(dfsImmutable); err == nil {
			config.Immutable = immutable
		} else {
			log.Error(err, "cannot parse DINGOFS_IMMUTABLE")
			os.Exit(1)
		}
	}

	config.NodeName = os.Getenv("NODE_NAME")
	config.Namespace = os.Getenv("DINGOFS_MOUNT_NAMESPACE")
	config.MountPointPath = os.Getenv("DINGOFS_MOUNT_PATH")
	config.DFSConfigPath = os.Getenv("DINGOFS_CONFIG_PATH")

	if mountPodImage := os.Getenv("DINGOFS_MOUNT_IMAGE"); mountPodImage != "" {
		config.DefaultMountImage = mountPodImage
	}

	// When not in sidecar mode, we should inherit attributes from CSI Node pod.
	k8sclient, err := k8s.NewClient()
	if err != nil {
		log.Error(err, "Can't get k8s client")
		os.Exit(1)
	}
	CSINodeDsName := "dingofs-csi-node"
	if name := os.Getenv("DINGOFS_CSI_NODE_DS_NAME"); name != "" {
		CSINodeDsName = name
	}
	ds, err := k8sclient.GetDaemonSet(context.TODO(), CSINodeDsName, config.Namespace)
	if err != nil {
		log.Error(err, "Can't get DaemonSet", "ds", CSINodeDsName)
		os.Exit(1)
	}
	config.CSIPod = corev1.Pod{
		Spec: ds.Spec.Template.Spec,
	}
}

func controllerRun(ctx context.Context) {
	parseControllerConfig()
	if nodeID == "" {
		log.Info("nodeID must be provided")
		os.Exit(1)
	}

	// http server for pprof
	go func() {
		port := 6060
		for {
			if err := http.ListenAndServe(fmt.Sprintf("localhost:%d", port), nil); err != nil {
				log.Error(err, "failed to start pprof server")
				os.Exit(1)
			}
			port++
		}
	}()

	// enable mount manager in csi controller
	if config.MountManager {
		go func() {
			mgr, err := app.NewMountManager(leaderElection, leaderElectionNamespace, leaderElectionLeaseDuration)
			if err != nil {
				log.Error(err, "fail to create mount manager")
				return
			}
			mgr.Start(ctx)
		}()
	}

	drv, err := dingofsdriver.NewDriver(endpoint, nodeID, leaderElection, leaderElectionNamespace, leaderElectionLeaseDuration)
	if err != nil {
		log.Error(err, "fail to create driver")
		os.Exit(1)
	}
	go func() {
		<-ctx.Done()
		drv.Stop()
	}()
	if err := drv.Run(); err != nil {
		log.Error(err, "fail to run driver")
		os.Exit(1)
	}
}
