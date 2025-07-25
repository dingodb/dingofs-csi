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
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	provisioncontroller "sigs.k8s.io/sig-storage-lib-external-provisioner/v10/controller"

	"github.com/jackblack369/dingofs-csi/pkg/config"
	k8s "github.com/jackblack369/dingofs-csi/pkg/k8sclient"
	"github.com/jackblack369/dingofs-csi/pkg/util/resource"
)

var (
	provisionerLog = klog.NewKlogr().WithName("provisioner")
)

type provisionerService struct {
	provider Provider
	*k8s.K8sClient
	leaderElection              bool
	leaderElectionNamespace     string
	leaderElectionLeaseDuration time.Duration
}

func newProvisionerService(k8sClient *k8s.K8sClient, leaderElection bool,
	leaderElectionNamespace string, leaderElectionLeaseDuration time.Duration) (provisionerService, error) {
	dfsProvider := NewDfsProvider(nil, k8sClient)

	if leaderElectionNamespace == "" {
		leaderElectionNamespace = config.Namespace
	}

	return provisionerService{
		provider:                    dfsProvider,
		K8sClient:                   k8sClient,
		leaderElection:              leaderElection,
		leaderElectionNamespace:     leaderElectionNamespace,
		leaderElectionLeaseDuration: leaderElectionLeaseDuration,
	}, nil
}

func (p *provisionerService) Run(ctx context.Context) {
	if p.K8sClient == nil {
		provisionerLog.Info("K8sClient is nil")
		os.Exit(1)
	}

	// m := metrics.New(string(uuid.NewUUID()))
	provisionerOptions := []func(*provisioncontroller.ProvisionController) error{
		//provisioncontroller.MetricsInstance(m),
		//provisioncontroller.ResyncPeriod(config.ResyncPeriod),
		//provisioncontroller.CreateProvisionedPVInterval(10 * time.Millisecond),
		//provisioncontroller.LeaseDuration(2 * config.ResyncPeriod),
		//provisioncontroller.RenewDeadline(config.ResyncPeriod),
		//provisioncontroller.RetryPeriod(config.ResyncPeriod / 2),
		provisioncontroller.LeaderElection(p.leaderElection),
		provisioncontroller.LeaderElectionNamespace(p.leaderElectionNamespace),
		provisioncontroller.LeaseDuration(p.leaderElectionLeaseDuration),
	}
	pc := provisioncontroller.NewProvisionController(
		provisionerLog,
		p.K8sClient,
		config.DriverName,
		p,
		provisionerOptions...,
	)
	pc.Run(ctx)
}

func (p *provisionerService) Provision(ctx context.Context, options provisioncontroller.ProvisionOptions) (*corev1.PersistentVolume, provisioncontroller.ProvisioningState, error) {
	provisionerLog.V(1).Info("provision options", "options", options)
	if options.PVC.Spec.Selector != nil {
		return nil, provisioncontroller.ProvisioningFinished, fmt.Errorf("claim Selector is not supported")
	}

	pvMeta := resource.NewObjectMeta(*options.PVC, options.SelectedNode)

	pvName := options.PVName
	scParams := make(map[string]string)
	for k, v := range options.StorageClass.Parameters {
		if strings.HasPrefix(k, "csi.storage.k8s.io/") {
			scParams[k] = pvMeta.ResolveSecret(v, pvName)
		} else {
			scParams[k] = pvMeta.StringParser(options.StorageClass.Parameters[k])
		}
	}
	provisionerLog.V(1).Info("Resolved StorageClass.Parameters", "params", scParams)

	subPath := pvName
	if scParams["pathPattern"] != "" {
		subPath = scParams["pathPattern"]
	}
	// return error if set readonly in dynamic provisioner
	for _, am := range options.PVC.Spec.AccessModes {
		if am == corev1.ReadOnlyMany {
			if options.StorageClass.Parameters["pathPattern"] == "" {
				return nil, provisioncontroller.ProvisioningFinished, status.Errorf(codes.InvalidArgument, "Dynamic mounting uses the sub-path named pv name as data isolation, so read-only mode cannot be used.")
			} else {
				provisionerLog.Info("Volume is set readonly, please make sure the subpath exists.", "subPath", subPath)
			}
		}
	}

	mountOptions := make([]string, 0)
	for _, mo := range options.StorageClass.MountOptions {
		parsedStr := pvMeta.StringParser(mo)
		mountOptions = append(mountOptions, strings.Split(strings.TrimSpace(parsedStr), ",")...)
	}
	provisionerLog.V(1).Info("Resolved MountOptions", "options", mountOptions)

	// set volume context
	volCtx := make(map[string]string)
	volCtx["subPath"] = subPath
	volCtx["capacity"] = strconv.FormatInt(options.PVC.Spec.Resources.Requests.Storage().Value(), 10)
	for k, v := range scParams {
		volCtx[k] = v
	}

	secret, err := p.K8sClient.GetSecret(ctx, scParams[config.ProvisionerSecretName], scParams[config.ProvisionerSecretNamespace])
	if err != nil {
		provisionerLog.Error(err, "Get Secret error")
		return nil, provisioncontroller.ProvisioningFinished, errors.New("unable to provision new pv: " + err.Error())
	}

	secretData := make(map[string]string)
	for k, v := range secret.Data {
		secretData[k] = string(v)
	}

	// create the subPath directory immediately in the filesystem
	provisionerLog.V(1).Info("Creating subPath in DingoFS", "subPath", subPath, "pvName", pvName, "secretNamespace", secret.Namespace, "secretName", secret.Name, "sercetData", secretData)

	if err := p.provider.DfsCreateVol(ctx, pvName, subPath, secretData, volCtx); err != nil {
		provisionerLog.Error(err, "Failed to create subPath in DingoFS", "subPath", subPath)
		return nil, provisioncontroller.ProvisioningFinished, err
	}
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
		},
		Spec: corev1.PersistentVolumeSpec{
			Capacity: corev1.ResourceList{
				corev1.ResourceName(corev1.ResourceStorage): options.PVC.Spec.Resources.Requests[corev1.ResourceName(corev1.ResourceStorage)],
			},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:           config.DriverName,
					VolumeHandle:     pvName,
					ReadOnly:         false,
					FSType:           "dingofs",
					VolumeAttributes: volCtx,
					NodePublishSecretRef: &corev1.SecretReference{
						Name:      scParams[config.PublishSecretName],
						Namespace: scParams[config.PublishSecretNamespace],
					},
				},
			},
			AccessModes:                   options.PVC.Spec.AccessModes,
			PersistentVolumeReclaimPolicy: *options.StorageClass.ReclaimPolicy,
			StorageClassName:              options.StorageClass.Name,
			MountOptions:                  mountOptions,
			VolumeMode:                    options.PVC.Spec.VolumeMode,
		},
	}
	if scParams[config.ControllerExpandSecretName] != "" && scParams[config.ControllerExpandSecretNamespace] != "" {
		pv.Spec.CSI.ControllerExpandSecretRef = &corev1.SecretReference{
			Name:      scParams[config.ControllerExpandSecretName],
			Namespace: scParams[config.ControllerExpandSecretNamespace],
		}
	}

	if pv.Spec.PersistentVolumeReclaimPolicy == corev1.PersistentVolumeReclaimDelete && options.StorageClass.Parameters["secretFinalizer"] == "true" {

		provisionerLog.V(1).Info("Add Finalizer", "namespace", secret.Namespace, "name", secret.Name)
		err = resource.AddSecretFinalizer(ctx, p.K8sClient, secret, config.Finalizer)
		if err != nil {
			provisionerLog.Error(err, "Fails to add a finalizer to the secret")
		}
	}
	return pv, provisioncontroller.ProvisioningFinished, nil
}

func (p *provisionerService) Delete(ctx context.Context, volume *corev1.PersistentVolume) error {
	provisionerLog.V(1).Info("Delete volume", "volume", *volume)
	// If it exists and has a `delete` value, delete the directory.
	// If it exists and has a `retain` value, safe the directory.
	policy := volume.Spec.PersistentVolumeReclaimPolicy
	if policy != corev1.PersistentVolumeReclaimDelete {
		provisionerLog.V(1).Info("Volume retain, return.", "volume", volume.Name)
		return nil
	}
	// check all pvs of the same storageClass, if multiple pv using the same subPath, do not delete the subPath
	shouldDeleted, err := resource.CheckForSubPath(ctx, p.K8sClient, volume, volume.Spec.CSI.VolumeAttributes["pathPattern"])
	if err != nil {
		provisionerLog.Error(err, "check for subPath error")
		return err
	}
	if !shouldDeleted {
		provisionerLog.Info("there are other pvs using the same subPath retained, volume should not be deleted, return.", "volume", volume.Name)
		return nil
	}
	provisionerLog.V(1).Info("there are no other pvs using the same subPath, volume can be deleted.", "volume", volume.Name)
	subPath := volume.Spec.PersistentVolumeSource.CSI.VolumeAttributes["subPath"]
	secretName, secretNamespace := volume.Spec.CSI.NodePublishSecretRef.Name, volume.Spec.CSI.NodePublishSecretRef.Namespace
	secret, err := p.K8sClient.GetSecret(ctx, secretName, secretNamespace)
	if err != nil {
		provisionerLog.Error(err, "Get Secret error")
		return err
	}
	secretData := make(map[string]string)
	for k, v := range secret.Data {
		secretData[k] = string(v)
	}

	provisionerLog.Info("Deleting volume subpath", "subPath", subPath)
	if err := p.provider.DfsDeleteVol(ctx, volume.Name, subPath, secretData, volume.Spec.CSI.VolumeAttributes, volume.Spec.MountOptions); err != nil {
		provisionerLog.Error(err, "delete vol error")
		return errors.New("unable to provision delete volume: " + err.Error())
	}

	if volume.Spec.CSI.VolumeAttributes["secretFinalizer"] == "true" {
		shouldRemoveFinalizer, err := resource.CheckForSecretFinalizer(ctx, p.K8sClient, volume)
		if err != nil {
			provisionerLog.Error(err, "CheckForSecretFinalizer error")
			return err
		}
		if shouldRemoveFinalizer {
			provisionerLog.V(1).Info("Remove Finalizer", "namespace", secretNamespace, "name", secretName)
			if err = resource.RemoveSecretFinalizer(ctx, p.K8sClient, secret, config.Finalizer); err != nil {
				return err
			}
		}
	}
	return nil
}
