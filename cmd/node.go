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
	"github.com/jackblack369/dingofs-csi/pkg/fuse"
	k8s "github.com/jackblack369/dingofs-csi/pkg/k8sclient"
	"github.com/jackblack369/dingofs-csi/pkg/util"
	"k8s.io/klog/v2"
)

func parseNodeConfig() {
	config.FormatInPod = formatInPod
	if os.Getenv("DRIVER_NAME") != "" {
		config.DriverName = os.Getenv("DRIVER_NAME")
	}

	if jfsImmutable := os.Getenv("DINGOFS_IMMUTABLE"); jfsImmutable != "" {
		if immutable, err := strconv.ParseBool(jfsImmutable); err == nil {
			config.Immutable = immutable
		} else {
			klog.Error(err, "cannot parse DINGOFS_IMMUTABLE")
		}
	}
	config.NodeName = os.Getenv("NODE_NAME")
	config.Namespace = os.Getenv("DINGOFS_MOUNT_NAMESPACE")
	config.PodName = os.Getenv("POD_NAME")
	config.MountPointPath = os.Getenv("DINGOFS_MOUNT_PATH")
	config.DFSConfigPath = os.Getenv("DINGOFS_CONFIG_PATH")
	config.HostIp = os.Getenv("HOST_IP")
	config.KubeletPort = os.Getenv("KUBELET_PORT")
	jfsMountPriorityName := os.Getenv("DINGOFS_MOUNT_PRIORITY_NAME")
	jfsMountPreemptionPolicy := os.Getenv("DINGOFS_MOUNT_PREEMPTION_POLICY")
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

	if jfsMountPriorityName != "" {
		config.DFSMountPriorityName = jfsMountPriorityName
	}

	if jfsMountPreemptionPolicy != "" {
		config.DFSMountPreemptionPolicy = jfsMountPreemptionPolicy
	}

	if mountPodImage := os.Getenv("DINGOFS_CE_MOUNT_IMAGE"); mountPodImage != "" {
		config.DefaultMountImage = mountPodImage
	}

	if mountPodImage := os.Getenv("DINGOFS_MOUNT_IMAGE"); mountPodImage != "" {
		// check if it's CE or EE
		hasCE, hasEE := util.ImageResol(mountPodImage)
		if hasCE {
			config.DefaultCEMountImage = mountPodImage
		}
		if hasEE {
			config.DefaultEEMountImage = mountPodImage
		}
	}
	if os.Getenv("STORAGE_CLASS_SHARE_MOUNT") == "true" {
		config.StorageClassShareMount = true
	}

	if config.PodName == "" || config.Namespace == "" {
		klog.Info("Pod name & namespace can't be null.")
		os.Exit(1)
	}
	config.ReconcilerInterval = reconcilerInterval
	if config.ReconcilerInterval < 5 {
		config.ReconcilerInterval = 5
	}

	k8sclient, err := k8s.NewClient()
	if err != nil {
		klog.Error(err, "Can't get k8s client")
		os.Exit(1)
	}
	pod, err := k8sclient.GetPod(context.TODO(), config.PodName, config.Namespace)
	if err != nil {
		klog.Error(err, "Can't get pod", "pod", config.PodName)
		os.Exit(1)
	}
	config.CSIPod = *pod
	err = fuse.InitGlobalFds(context.TODO(), "/tmp")
	if err != nil {
		klog.Error(err, "Init global fds error")
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
				klog.Error(err, "failed to start pprof server")
			}
			port++
		}
	}()

	registerer, registry := util.NewPrometheus(config.NodeName)
	// http server for metrics
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.HandlerFor(
			registry,
			promhttp.HandlerOpts{
				// Opt into OpenMetrics to support exemplars.
				EnableOpenMetrics: true,
			},
		))
		server := &http.Server{
			Addr:    fmt.Sprintf(":%d", config.WebPort),
			Handler: mux,
		}
		if err := server.ListenAndServe(); err != nil {
			klog.Error(err, "failed to start metrics server")
		}
	}()

	// enable pod manager in csi node
	if !process && podManager {
		needStartPodManager := false
		if config.KubeletPort != "" && config.HostIp != "" {
			if err := retry.OnError(retry.DefaultBackoff, func(err error) bool { return true }, func() error {
				return controller.StartReconciler()
			}); err != nil {
				klog.Error(err, "Could not Start Reconciler of polling kubelet and fallback to watch ApiServer.")
				needStartPodManager = true
			}
		} else {
			needStartPodManager = true
		}

		if needStartPodManager {
			go func() {
				mgr, err := app.NewPodManager()
				if err != nil {
					klog.Error(err, "fail to create pod manager")
					os.Exit(1)
				}

				if err := mgr.Start(ctx); err != nil {
					klog.Error(err, "fail to start pod manager")
					os.Exit(1)
				}
			}()
		}
		klog.Info("Pod Reconciler Started")
	}

	drv, err := driver.NewDriver(endpoint, nodeID, leaderElection, leaderElectionNamespace, leaderElectionLeaseDuration, registerer)
	if err != nil {
		klog.Error(err, "fail to create driver")
		os.Exit(1)
	}

	go func() {
		<-ctx.Done()
		drv.Stop()
	}()

	if err := drv.Run(); err != nil {
		klog.Error(err, "fail to run driver")
		os.Exit(1)
	}
}
