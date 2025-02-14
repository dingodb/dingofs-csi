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

package builder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jackblack369/dingofs-csi/pkg/config"
	"github.com/jackblack369/dingofs-csi/pkg/k8sclient"
	"github.com/jackblack369/dingofs-csi/pkg/util"
	"github.com/jackblack369/dingofs-csi/pkg/util/resource"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	k8sMount "k8s.io/utils/mount"
)

type MntInterface interface {
	k8sMount.Interface
	DMount(ctx context.Context, appInfo *config.AppInfo, dfsSetting *config.DfsSetting) error
	// DCreateVolume(ctx context.Context, dfsSetting *dfsConfig.dfsSetting) error
	DeleteVolume(ctx context.Context, dfsSetting *config.DfsSetting) error
	GetMountRef(ctx context.Context, target, podName string) (int, error)
	UmountTarget(ctx context.Context, target, podName string) error
	DUmount(ctx context.Context, target, podName string) error
	AddRefOfMount(ctx context.Context, target string, podName string) error
	CleanCache(ctx context.Context, image string, id string, volumeId string, cacheDirs []string) error
}

type PodMount struct {
	k8sMount.SafeFormatAndMount
	K8sClient *k8sclient.K8sClient
}

var _ MntInterface = &PodMount{}

func NewPodMount(client *k8sclient.K8sClient, mounter k8sMount.SafeFormatAndMount) MntInterface {
	return &PodMount{mounter, client}
}

// DMount mount dfs volume
func (p *PodMount) DMount(ctx context.Context, appInfo *config.AppInfo, dfsSetting *config.DfsSetting) error {
	hashVal := util.GenHashOfSetting(*dfsSetting)
	dfsSetting.HashVal = hashVal
	klog.Infof("config.dfsSetting:%#v", *dfsSetting)
	var mountPodName string
	var err error

	if err = func() error {
		lock := util.GetPodLock(hashVal)
		lock.Lock()
		defer lock.Unlock()

		mountPodName, err = resource.GenMountPodName(ctx, p.K8sClient, dfsSetting)
		if err != nil {
			return err
		}

		// set mount pod name in app pod
		if appInfo != nil && appInfo.Name != "" && appInfo.Namespace != "" {
			err = resource.SetMountLabel(ctx, p.K8sClient, dfsSetting.UniqueId, mountPodName, appInfo.Name, appInfo.Namespace)
			if err != nil {
				return err
			}
		}

		// bootstrap mount pod
		err = p.createPodOrAddRef(ctx, mountPodName, dfsSetting, appInfo)
		if err != nil {
			return err
		}
		return nil
	}(); err != nil {
		return err
	}

	err = p.waitUtilMountReady(ctx, dfsSetting, mountPodName)
	if err != nil {
		return err
	}
	// TODO set fsid as annotation in mount pod
	//if dfsSetting.FSID == "" {
	//	// need set fsid as label in mount pod for clean cache
	//	fsid, err := GetDfsID(ctx, dfsSetting)
	//	if err != nil {
	//		return err
	//	}
	//	err = util.SetFSIDAnnotation(ctx, mountPodName, fsid)
	//	if err != nil {
	//		return err
	//	}
	//}
	return nil
}

func (p *PodMount) createPodOrAddRef(ctx context.Context, podName string, dfsSetting *config.DfsSetting, appinfo *config.AppInfo) (err error) {
	klog.Infof("check mount pod[%s] existed or not", podName)
	dfsSetting.MountPath = dfsSetting.MountPath + podName[len(podName)-7:] // e.g. /dfs/pvc-7175fc74-d52d-46bc-94b3-ad9296b726cd-alypal
	dfsSetting.SecretName = fmt.Sprintf("dingofs-%s-secret", dfsSetting.UniqueId)
	// mkdir mountpath
	err = util.DoWithTimeout(ctx, 3*time.Second, func() error {
		exist, _ := k8sMount.PathExists(dfsSetting.MountPath)
		if !exist {
			return os.MkdirAll(dfsSetting.MountPath, 0777)
		}
		return nil
	})
	if err != nil {
		return
	}

	r := NewPodBuilder(dfsSetting, 0, p.K8sClient)
	secret := r.NewSecret()
	SetPVAsOwner(&secret, dfsSetting.PV)

	key := util.GetReferenceKey(dfsSetting.TargetPath)

	waitCtx, waitCancel := context.WithTimeout(ctx, 60*time.Second)
	defer waitCancel()
	for {
		// wait for old pod deleted
		oldPod, err := p.K8sClient.GetPod(waitCtx, podName, config.Namespace)
		klog.V(6).Infof("oldPod:%v", oldPod)
		if err == nil && oldPod.DeletionTimestamp != nil {
			klog.V(1).Info("wait for old mount pod deleted.", "podName", podName)
			time.Sleep(time.Millisecond * 500)
			continue
		} else if err != nil {
			if k8serrors.IsNotFound(err) {
				// pod not exist, create
				klog.Infof("Begin create pod:%s, config info:%v", podName, dfsSetting)
				newPod, err := r.NewMountPod(podName)
				klog.V(5).Infof("init mount pod spec:%#v", newPod.Spec)
				if err != nil {
					klog.ErrorS(err, "Make new mount pod error", "podName", podName)
					return err
				}
				newPod.Annotations[key] = dfsSetting.TargetPath
				newPod.Labels[config.PodHashLabelKey] = dfsSetting.HashVal
				// extract last 6 char of podName as uniqueId which is generate by random,
				// e.g. ingofs-sd-shangdi-ceph17-pvc-cc5cb2f5-e6fb-4a31-9476-6d16dedbc607-foogee extract foogee
				newPod.Labels[config.PodUniqueIdLabelKey] = podName[len(podName)-6:]

				nodeSelector := map[string]string{
					"kubernetes.io/hostname": newPod.Spec.NodeName,
				}
				nodes, err := p.K8sClient.ListNode(ctx, &metav1.LabelSelector{MatchLabels: nodeSelector})
				if err != nil || len(nodes) != 1 || nodes[0].Name != newPod.Spec.NodeName {
					klog.Info("cannot select node by label selector", "nodeName", newPod.Spec.NodeName, "error", err)
				} else {
					newPod.Spec.NodeName = ""
					newPod.Spec.NodeSelector = nodeSelector
					if appinfo != nil && appinfo.Name != "" {
						appPod, err := p.K8sClient.GetPod(ctx, appinfo.Name, appinfo.Namespace)
						if err != nil {
							klog.Info("get app pod", "namespace", appinfo.Namespace, "name", appinfo.Name, "error", err)
						} else {
							newPod.Spec.Affinity = appPod.Spec.Affinity
							newPod.Spec.SchedulerName = appPod.Spec.SchedulerName
							newPod.Spec.Tolerations = appPod.Spec.Tolerations
						}
					}
				}

				if err := resource.CreateOrUpdateSecret(ctx, p.K8sClient, &secret); err != nil {
					return err
				}
				_, err = p.K8sClient.CreatePod(ctx, newPod)
				if err != nil {
					klog.ErrorS(err, "Create pod err", "podName", podName)
				}
				klog.V(6).Infof("mount pod:%s create successful!", podName)
				return err
			} else if k8serrors.IsTimeout(err) {
				return fmt.Errorf("mount %v failed: mount pod %s deleting timeout", dfsSetting.VolumeId, podName)
			}
			// unexpect error
			klog.ErrorS(err, "Get pod err", "podName", podName)
			return err
		}
		// pod exist, add refs
		if err := resource.CreateOrUpdateSecret(ctx, p.K8sClient, &secret); err != nil {
			return err
		}
		return p.AddRefOfMount(ctx, dfsSetting.TargetPath, podName)
	}
}

func (p *PodMount) AddRefOfMount(ctx context.Context, target string, podName string) error {
	klog.Infof("Add target ref in mount pod, podName:%s ,target:%s", podName, target)
	// add volumeId ref in pod annotation
	key := util.GetReferenceKey(target)

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		exist, err := p.K8sClient.GetPod(ctx, podName, config.Namespace)
		if err != nil {
			return err
		}
		if exist.DeletionTimestamp != nil {
			return fmt.Errorf("addRefOfMount: Mount pod [%s] has been deleted", podName)
		}
		annotation := exist.Annotations
		if _, ok := annotation[key]; ok {
			klog.Info("Target ref in pod already exists, target:%s, podName:%s", target, podName)
			return nil
		}
		if annotation == nil {
			annotation = make(map[string]string)
		}
		annotation[key] = target
		// delete deleteDelayAt when there ars refs
		delete(annotation, config.DeleteDelayAtKey)
		return resource.ReplacePodAnnotation(ctx, p.K8sClient, exist, annotation)
	})
	if err != nil {
		klog.Error("Add target ref in mount pod[%s]error, %v ", podName, err)
		return err
	}
	return nil
}

func (p *PodMount) waitUtilMountReady(ctx context.Context, dfsSetting *config.DfsSetting, podName string) error {
	err := resource.WaitUtilMountReady(ctx, podName, dfsSetting.MountPath, config.DefaultCheckTimeout)
	if err == nil {
		return nil
	}
	// mountpoint not ready, get mount pod log for detail
	log, err := resource.GetErrContainerLog(ctx, p.K8sClient, podName)
	if err != nil {
		klog.ErrorS(err, "Get pod log error", "podName", podName)
		return fmt.Errorf("mount %v at %v failed: mount isn't ready in 30 seconds", util.StripPasswd(dfsSetting.Source), dfsSetting.MountPath)
	}
	return fmt.Errorf("mount %v at %v failed, mountpod: %s, failed log: %v", util.StripPasswd(dfsSetting.Source), dfsSetting.MountPath, podName, log)
}

func (p *PodMount) DeleteVolume(ctx context.Context, dfsSetting *config.DfsSetting) error {
	var exist *batchv1.Job
	r := NewJobBuilder(dfsSetting, 0)
	job := r.NewJobForDeleteVolume()
	exist, err := p.K8sClient.GetJob(ctx, job.Name, job.Namespace)
	if err != nil && k8serrors.IsNotFound(err) {
		klog.Info("create job", "jobName", job.Name)
		exist, err = p.K8sClient.CreateJob(ctx, job)
		if err != nil {
			klog.ErrorS(err, "create job err", "jobName", job.Name)
			return err
		}
	}
	if err != nil {
		klog.ErrorS(err, "get job err", "jobName", job.Name)
		return err
	}
	secret := r.NewSecret()
	SetJobAsOwner(&secret, *exist)
	if err := p.createOrUpdateSecret(ctx, &secret); err != nil {
		return err
	}
	err = p.waitUtilJobCompleted(ctx, job.Name)
	if err != nil {
		// fall back if err
		if e := p.K8sClient.DeleteJob(ctx, job.Name, job.Namespace); e != nil {
			klog.Error(e, "delete job error", "jobName", job.Name)
		}
	}
	return err
}

func (p *PodMount) JUmount(ctx context.Context, target, podName string) error {
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		po, err := p.K8sClient.GetPod(ctx, podName, config.Namespace)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return nil
			}
			klog.ErrorS(err, "Get mount pod err", "podName", podName)
			return err
		}

		if GetRef(po) != 0 {
			klog.Info("pod still has dingofs- refs.", "podName", podName)
			return nil
		}

		var shouldDelay bool
		shouldDelay, err = resource.ShouldDelay(ctx, po, p.K8sClient)
		if err != nil {
			return err
		}
		if !shouldDelay {
			// do not set delay delete, delete it now
			klog.Info("pod has no dingofs- refs. delete it.", "podName", podName)
			if err := p.K8sClient.DeletePod(ctx, po); err != nil {
				klog.Info("Delete pod error", "podName", podName, "error", err)
				return err
			}

			// close socket
			// if util.SupportFusePass(po.Spec.Containers[0].Image) {
			// 	fuse.GlobalFds.StopFd(ctx, po.Labels[config.PodHashLabelKey])
			// }

			// delete related secret
			secretName := po.Name + "-secret"
			klog.V(1).Info("delete related secret of pod", "podName", podName, "secretName", secretName)
			if err := p.K8sClient.DeleteSecret(ctx, secretName, po.Namespace); !k8serrors.IsNotFound(err) && err != nil {
				// do not return err if delete secret failed
				klog.V(1).Info("Delete secret error", "secretName", secretName, "error", err)
			}
		}
		return nil
	})

	return err
}

func GetRef(pod *corev1.Pod) int {
	res := 0
	for k, target := range pod.Annotations {
		if k == util.GetReferenceKey(target) {
			res++
		}
	}
	return res
}

func (p *PodMount) createOrUpdateSecret(ctx context.Context, secret *corev1.Secret) error {
	klog.Info("secret name:%s, namespace:%s", secret.Name, secret.Namespace)
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		oldSecret, err := p.K8sClient.GetSecret(ctx, secret.Name, config.Namespace)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				// secret not exist, create
				_, err := p.K8sClient.CreateSecret(ctx, secret)
				return err
			}
			// unexpected err
			return err
		}
		oldSecret.Data = nil
		oldSecret.StringData = secret.StringData
		// merge owner reference
		if len(secret.OwnerReferences) != 0 {
			newOwner := secret.OwnerReferences[0]
			exist := false
			for _, ref := range oldSecret.OwnerReferences {
				if ref.UID == newOwner.UID {
					exist = true
					break
				}
			}
			if !exist {
				oldSecret.OwnerReferences = append(oldSecret.OwnerReferences, newOwner)
			}
		}
		klog.Info("secret info:%v", oldSecret)
		return p.K8sClient.UpdateSecret(ctx, oldSecret)
	})
	if err != nil {
		klog.ErrorS(err, "create or update secret error", "secretName", secret.Name)
		return err
	}
	return nil
}

func (p *PodMount) waitUtilJobCompleted(ctx context.Context, jobName string) error {
	// Wait until the job is completed
	waitCtx, waitCancel := context.WithTimeout(ctx, 40*time.Second)
	defer waitCancel()
	for {
		job, err := p.K8sClient.GetJob(waitCtx, jobName, config.Namespace)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				klog.Info("waitUtilJobCompleted: Job is completed and been recycled", "jobName", jobName)
				return nil
			}
			if waitCtx.Err() == context.DeadlineExceeded || waitCtx.Err() == context.Canceled {
				klog.V(1).Info("job timeout", "jobName", jobName)
				break
			}
			return fmt.Errorf("waitUtilJobCompleted: Get job %v failed: %v", jobName, err)
		}
		if resource.IsJobCompleted(job) {
			klog.Info("waitUtilJobCompleted: Job is completed", "jobName", jobName)
			if resource.IsJobShouldBeRecycled(job) {
				// try to delete job
				klog.Info("job completed but not be recycled automatically, delete it", "jobName", jobName)
				if err := p.K8sClient.DeleteJob(ctx, jobName, config.Namespace); err != nil {
					klog.ErrorS(err, "delete job error", "jobName", jobName)
				}
			}
			return nil
		}
		time.Sleep(time.Millisecond * 500)
	}

	pods, err := p.K8sClient.ListPod(ctx, config.Namespace, &metav1.LabelSelector{
		MatchLabels: map[string]string{
			"job-name": jobName,
		},
	}, nil)
	if err != nil || len(pods) == 0 {
		return fmt.Errorf("waitUtilJobCompleted: get pod from job %s error %v", jobName, err)
	}
	cnlog, err := p.getNotCompleteCnLog(ctx, pods[0].Name)
	if err != nil {
		return fmt.Errorf("waitUtilJobCompleted: get pod %s log error %v", pods[0].Name, err)
	}
	return fmt.Errorf("waitUtilJobCompleted: job %s isn't completed: %v", jobName, cnlog)
}

func (p *PodMount) DUmount(ctx context.Context, target, podName string) error {
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		po, err := p.K8sClient.GetPod(ctx, podName, config.Namespace)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return nil
			}
			klog.ErrorS(err, "Get mount pod err", "podName", podName)
			return err
		}

		if GetRef(po) != 0 {
			klog.Info("pod still has dingofs- refs.", "podName", podName)
			return nil
		}

		var shouldDelay bool
		shouldDelay, err = resource.ShouldDelay(ctx, po, p.K8sClient)
		if err != nil {
			return err
		}
		if !shouldDelay {
			// do not set delay delete, delete it now
			klog.Info("pod has no dingofs- refs. delete it.", "podName", podName)
			if err := p.K8sClient.DeletePod(ctx, po); err != nil {
				klog.Info("Delete pod error", "podName", podName, "error", err)
				return err
			}

			// close socket
			// if util.SupportFusePass(po.Spec.Containers[0].Image) {
			// 	fuse.GlobalFds.StopFd(ctx, po.Labels[config.PodHashLabelKey])
			// }

			// delete related secret
			secretName := po.Name + "-secret"
			klog.V(1).Info("delete related secret of pod", "podName", podName, "secretName", secretName)
			if err := p.K8sClient.DeleteSecret(ctx, secretName, po.Namespace); !k8serrors.IsNotFound(err) && err != nil {
				// do not return err if delete secret failed
				klog.V(1).Info("Delete secret error", "secretName", secretName, "error", err)
			}
		}
		return nil
	})

	return err
}

func (p *PodMount) getNotCompleteCnLog(ctx context.Context, podName string) (log string, err error) {
	pod, err := p.K8sClient.GetPod(ctx, podName, config.Namespace)
	if err != nil {
		return
	}
	for _, cn := range pod.Status.InitContainerStatuses {
		if cn.State.Terminated == nil || cn.State.Terminated.Reason != "Completed" {
			log, err = p.K8sClient.GetPodLog(ctx, pod.Name, pod.Namespace, cn.Name)
			return
		}
	}
	for _, cn := range pod.Status.ContainerStatuses {
		if cn.State.Terminated == nil || cn.State.Terminated.Reason != "Completed" {
			log, err = p.K8sClient.GetPodLog(ctx, pod.Name, pod.Namespace, cn.Name)
			return
		}
	}
	return
}

func (p *PodMount) UmountTarget(ctx context.Context, target, podName string) error {
	// targetPath may be mount bind many times when mount point recovered.
	// umount until it's not mounted.
	klog.Info("lazy umount", "target", target)
	for {
		command := exec.Command("umount", "-l", target)
		out, err := command.CombinedOutput()
		if err == nil {
			continue
		}
		klog.V(1).Info(string(out))
		if !strings.Contains(string(out), "not mounted") &&
			!strings.Contains(string(out), "mountpoint not found") &&
			!strings.Contains(string(out), "no mount point specified") {
			klog.ErrorS(err, "Could not lazy unmount", "target", target, "out", string(out))
			return err
		}
		break
	}

	// cleanup target path
	if err := k8sMount.CleanupMountPoint(target, p.SafeFormatAndMount.Interface, false); err != nil {
		klog.Info("Clean mount point error", "error", err)
		return err
	}

	// check mount pod is need to delete
	klog.Info("Delete target ref and check mount pod is need to delete or not.", "target", target, "podName", podName)

	if podName == "" {
		// mount pod not exist
		klog.Info("Mount pod of target not exists.", "target", target)
		return nil
	}
	pod, err := p.K8sClient.GetPod(ctx, podName, config.Namespace)
	if err != nil && !k8serrors.IsNotFound(err) {
		klog.ErrorS(err, "Get pod err", "podName", podName)
		return err
	}

	// if mount pod not exists.
	if pod == nil {
		klog.Info("Mount pod not exists", "podName", podName)
		return nil
	}

	key := util.GetReferenceKey(target)
	klog.V(1).Info("Target hash of target", "target", target, "key", key)

	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		po, err := p.K8sClient.GetPod(ctx, pod.Name, pod.Namespace)
		if err != nil {
			return err
		}
		annotation := po.Annotations
		if _, ok := annotation[key]; !ok {
			klog.Info("Target ref in pod already not exists.", "target", target, "podName", pod.Name)
			return nil
		}
		return resource.DelPodAnnotation(ctx, p.K8sClient, pod, []string{key})
	})
	if err != nil {
		klog.ErrorS(err, "Remove ref of target err", "target", target)
		return err
	}
	return nil
}

func (p *PodMount) GetMountRef(ctx context.Context, target, podName string) (int, error) {
	pod, err := p.K8sClient.GetPod(ctx, podName, config.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return 0, nil
		}
		klog.ErrorS(err, "Get mount pod error", "podName", podName)
		return 0, err
	}
	return GetRef(pod), nil
}

func (p *PodMount) CleanCache(ctx context.Context, image string, id string, volumeId string, cacheDirs []string) error {
	dfsSetting, err := config.ParseSetting(map[string]string{"name": id}, nil, []string{}, nil, nil)
	if err != nil {
		klog.ErrorS(err, "parse dfs setting err")
		return err
	}
	dfsSetting.Attr.Image = image
	dfsSetting.VolumeId = volumeId
	dfsSetting.CacheDirs = cacheDirs
	dfsSetting.FSID = id
	r := NewJobBuilder(dfsSetting, 0)
	job := r.NewJobForCleanCache()
	klog.V(1).Info("Clean cache job", "jobName", job)
	_, err = p.K8sClient.GetJob(ctx, job.Name, job.Namespace)
	if err != nil && k8serrors.IsNotFound(err) {
		klog.Info("create job", "jobName", job.Name)
		_, err = p.K8sClient.CreateJob(ctx, job)
	}
	if err != nil {
		klog.ErrorS(err, "get or create job err", "jobName", job.Name)
		return err
	}
	err = p.waitUtilJobCompleted(ctx, job.Name)
	if err != nil {
		klog.ErrorS(err, "wait for job completed err and fall back to delete job")
		// fall back if err
		if e := p.K8sClient.DeleteJob(ctx, job.Name, job.Namespace); e != nil {
			klog.Error(e, "delete job %s error: %v", "jobName", job.Name)
		}
	}
	return nil
}
