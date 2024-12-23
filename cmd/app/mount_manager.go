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

package app

import (
	"context"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/jackblack369/dingofs-csi/pkg/config"
	mountctrl "github.com/jackblack369/dingofs-csi/pkg/controller"
	"github.com/jackblack369/dingofs-csi/pkg/k8sclient"
)

var (
	scheme = runtime.NewScheme()
	log    = klog.NewKlogr().WithName("manager")
)

func init() {
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
}

type MountManager struct {
	mgr    ctrl.Manager
	client *k8sclient.K8sClient
}

func NewMountManager(
	leaderElection bool,
	leaderElectionNamespace string,
	leaderElectionLeaseDuration time.Duration) (*MountManager, error) {
	conf, err := ctrl.GetConfig()
	if err != nil {
		return nil, err
	}
	// clientConfig := &
	mgr, err := ctrl.NewManager(conf, ctrl.Options{
		Scheme:           scheme,
		LeaderElectionID: "mount.dingofs.com",
		NewCache: func(restConf *rest.Config, opts cache.Options) (cache.Cache, error) {
			opts.Scheme = scheme
			opts.DefaultLabelSelector = labels.SelectorFromSet(map[string]string{
				config.PodTypeKey: config.PodTypeValue,
				config.JobTypeKey: config.JobTypeValue,
			})
			return cache.New(conf, opts)
		},
	})
	if err != nil {
		log.Error(err, "New mount controller error")
		return nil, err
	}

	// gen k8s client
	k8sClient, err := k8sclient.NewClient()
	if err != nil {
		log.Error(err, "Could not create k8s client")
		return nil, err
	}

	return &MountManager{
		mgr:    mgr,
		client: k8sClient,
	}, err
}

func (m *MountManager) Start(ctx context.Context) {
	// init Reconciler（Controller）
	if err := (mountctrl.NewMountController(m.client)).SetupWithManager(m.mgr); err != nil {
		log.Error(err, "Register mount controller error")
		return
	}
	if err := (mountctrl.NewJobController(m.client)).SetupWithManager(m.mgr); err != nil {
		log.Error(err, "Register job controller error")
		return
	}
	if config.CacheClientConf {
		if err := (mountctrl.NewSecretController(m.client)).SetupWithManager(m.mgr); err != nil {
			log.Error(err, "Register secret controller error")
			return
		}
	}
	log.Info("Mount manager started.")
	if err := m.mgr.Start(ctx); err != nil {
		log.Error(err, "Mount manager start error")
		os.Exit(1)
	}
}
