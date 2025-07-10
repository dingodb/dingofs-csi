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

package main

import (
	"flag"
	"fmt"
	_ "net/http/pprof"
	"os"
	"strings"
	"time"

	"github.com/jackblack369/dingofs-csi/pkg/config"
	"github.com/jackblack369/dingofs-csi/pkg/util"
	"github.com/spf13/cobra"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
)

var (
	endpoint    string
	version     bool
	nodeID      string
	formatInPod bool

	provisioner        bool
	cacheConf          bool
	podManager         bool
	localPV            bool
	reconcilerInterval int

	leaderElection              bool
	leaderElectionNamespace     string
	leaderElectionLeaseDuration time.Duration

	log = klog.NewKlogr().WithName("main")
)

func main() {

	var cmd = &cobra.Command{
		Use:   "dingofs-csi",
		Short: "dingofs csi driver",
		Run: func(cmd *cobra.Command, args []string) {
			if version {
				info, err := util.GetVersionJSON()
				if err != nil {
					log.Error(err, "fail to get version info")
					os.Exit(1)
				}
				fmt.Println(info)
				os.Exit(0)
			}

			run()
		},
	}

	cmd.PersistentFlags().StringVar(&endpoint, "endpoint", "unix://tmp/csi.sock", "CSI endpoint")
	cmd.PersistentFlags().BoolVar(&version, "version", false, "Print the version and exit.")
	cmd.PersistentFlags().StringVar(&nodeID, "nodeid", "", "Node ID")
	cmd.PersistentFlags().BoolVar(&formatInPod, "format-in-pod", false, "Put format/auth in pod")

	cmd.PersistentFlags().BoolVar(&leaderElection, "leader-election", false, "Enables leader election. If leader election is enabled, additional RBAC rules are required. ")
	cmd.PersistentFlags().StringVar(&leaderElectionNamespace, "leader-election-namespace", config.Namespace, "Namespace where the leader election resource lives. Defaults to the pod namespace if not set.")
	cmd.PersistentFlags().DurationVar(&leaderElectionLeaseDuration, "leader-election-lease-duration", 15*time.Second, "Duration, in seconds, that non-leader candidates will wait to force acquire leadership. Defaults to 15 seconds.")

	// controller flags
	cmd.Flags().BoolVar(&provisioner, "provisioner", false, "Enable provisioner in controller. default false.")
	cmd.Flags().BoolVar(&cacheConf, "cache-client-conf", false, "Cache client config file. default false.")

	// node flags
	cmd.Flags().BoolVar(&podManager, "enable-manager", false, "Enable pod manager in csi node. default false.")
	cmd.Flags().IntVar(&reconcilerInterval, "reconciler-interval", 5, "interval (default 5s) for reconciler")
	cmd.Flags().BoolVar(&localPV, "local-pv", false, "Enable local pv mode. default false.")

	goFlag := flag.CommandLine
	klog.InitFlags(goFlag)
	cmd.PersistentFlags().AddGoFlagSet(goFlag)

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run() {

	ctx := ctrl.SetupSignalHandler()
	podName := os.Getenv("POD_NAME")
	if strings.Contains(podName, "csi-controller") {
		log.Info("Run CSI controller")
		controllerRun(ctx)
	}
	if strings.Contains(podName, "csi-node") {
		log.Info("Run CSI node")
		nodeRun(ctx)
	}
}
