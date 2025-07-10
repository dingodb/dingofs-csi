package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"k8s.io/client-go/util/retry"

	"github.com/jackblack369/dingofs-csi/cmd/app"
	"github.com/jackblack369/dingofs-csi/pkg/config"
	"github.com/jackblack369/dingofs-csi/pkg/controller"
	dingofsdriver "github.com/jackblack369/dingofs-csi/pkg/dingofs-driver"
	"github.com/jackblack369/dingofs-csi/pkg/fuse"
	k8s "github.com/jackblack369/dingofs-csi/pkg/k8sclient"
	"k8s.io/klog/v2"
)

func parseNodeConfig() {
	config.LocalPV = localPV
	if os.Getenv("DRIVER_NAME") != "" {
		config.DriverName = os.Getenv("DRIVER_NAME")
	}

	if dfsImmutable := os.Getenv("DINGOFS_IMMUTABLE"); dfsImmutable != "" {
		if immutable, err := strconv.ParseBool(dfsImmutable); err == nil {
			config.Immutable = immutable
		} else {
			klog.ErrorS(err, "cannot parse DINGOFS_IMMUTABLE")
		}
	}
	config.NodeName = os.Getenv("NODE_NAME")
	config.Namespace = os.Getenv("DINGOFS_MOUNT_NAMESPACE")
	config.PodName = os.Getenv("POD_NAME")
	config.MountPointPath = os.Getenv("DINGOFS_MOUNT_PATH")
	config.DFSConfigPath = os.Getenv("DINGOFS_CONFIG_PATH")
	config.HostIp = os.Getenv("HOST_IP")
	config.KubeletPort = os.Getenv("KUBELET_PORT")
	dfsMountPriorityName := os.Getenv("DINGOFS_MOUNT_PRIORITY_NAME")
	dfsMountPreemptionPolicy := os.Getenv("DINGOFS_MOUNT_PREEMPTION_POLICY")
	if timeout := os.Getenv("DINGOFS_RECONCILE_TIMEOUT"); timeout != "" {
		duration, _ := time.ParseDuration(timeout)
		if duration > config.ReconcileTimeout {
			config.ReconcileTimeout = duration
		}
	}
	if interval := os.Getenv("DINGOFS_CONFIG_UPDATE_INTERVAL"); interval != "" {
		duration, _ := time.ParseDuration(interval)
		if duration > config.SecretReconcilerInterval {
			config.SecretReconcilerInterval = duration
		}
	}

	if dfsMountPriorityName != "" {
		config.DFSMountPriorityName = dfsMountPriorityName
	}

	if dfsMountPreemptionPolicy != "" {
		config.DFSMountPreemptionPolicy = dfsMountPreemptionPolicy
	}

	if mountPodImage := os.Getenv("DINGOFS_MOUNT_IMAGE"); mountPodImage != "" {
		config.DefaultMountImage = mountPodImage
	}

	if config.PodName == "" || config.Namespace == "" {
		klog.Info("Pod name & namespace can't be null.")
		os.Exit(1)
	}

	k8sclient, err := k8s.NewClient()
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
	err = fuse.InitGlobalFds(context.TODO(), "/tmp")
	if err != nil {
		klog.ErrorS(err, "Init global fds error")
		os.Exit(1)
	}
}

func nodeRun(ctx context.Context) {
	parseNodeConfig()
	if nodeID == "" {
		klog.Info("nodeID must be provided")
		os.Exit(1)
	}

	// http server for pprof
	go func() {
		port := 6060
		for {
			if err := http.ListenAndServe(fmt.Sprintf("localhost:%d", port), nil); err != nil {
				klog.ErrorS(err, "failed to start pprof server")
			}
			port++
		}
	}()

	// enable pod manager in csi node
	if podManager {
		needStartPodManager := false
		if config.KubeletPort != "" && config.HostIp != "" {
			if err := retry.OnError(retry.DefaultBackoff, func(err error) bool { return true }, func() error {
				return controller.StartReconciler()
			}); err != nil {
				klog.ErrorS(err, "Could not Start Reconciler of polling kubelet and fallback to watch ApiServer.")
				needStartPodManager = true
			}
		} else {
			needStartPodManager = true
		}

		if needStartPodManager {
			go func() {
				mgr, err := app.NewPodManager()
				if err != nil {
					klog.ErrorS(err, "fail to create pod manager")
					os.Exit(1)
				}

				if err := mgr.Start(ctx); err != nil {
					klog.ErrorS(err, "fail to start pod manager")
					os.Exit(1)
				}
			}()
		}
		klog.Info("Pod Reconciler Started")
	}

	drv, err := dingofsdriver.NewDriver(endpoint, nodeID, leaderElection, leaderElectionNamespace, leaderElectionLeaseDuration)
	if err != nil {
		klog.ErrorS(err, "fail to create driver")
		os.Exit(1)
	}

	go func() {
		<-ctx.Done()
		drv.Stop()
	}()

	if err := drv.Run(); err != nil {
		klog.ErrorS(err, "fail to run driver")
		os.Exit(1)
	}
}
