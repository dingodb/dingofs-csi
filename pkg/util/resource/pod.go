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

package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"

	"github.com/jackblack369/dingofs-csi/pkg/config"
	"github.com/jackblack369/dingofs-csi/pkg/k8sclient"
	k8s "github.com/jackblack369/dingofs-csi/pkg/k8sclient"
	"github.com/jackblack369/dingofs-csi/pkg/util"
)

func IsPodReady(pod *corev1.Pod) bool {
	conditionsTrue := 0
	for _, cond := range pod.Status.Conditions {
		if cond.Status == corev1.ConditionTrue && (cond.Type == corev1.ContainersReady || cond.Type == corev1.PodReady) {
			conditionsTrue++
		}
	}
	return conditionsTrue == 2
}

func containError(statuses []corev1.ContainerStatus) bool {
	for _, status := range statuses {
		if (status.State.Waiting != nil && status.State.Waiting.Reason != "ContainerCreating") ||
			(status.State.Terminated != nil && status.State.Terminated.ExitCode != 0) {
			return true
		}
	}
	return false
}

func IsPodError(pod *corev1.Pod) bool {
	if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodUnknown {
		return true
	}
	return containError(pod.Status.ContainerStatuses)
}

func IsPodResourceError(pod *corev1.Pod) bool {
	if pod.Status.Phase == corev1.PodFailed {
		if strings.Contains(pod.Status.Reason, "OutOf") {
			return true
		}
		if pod.Status.Reason == "UnexpectedAdmissionError" &&
			strings.Contains(pod.Status.Message, "to reclaim resources") {
			return true
		}
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Status == corev1.ConditionFalse && cond.Type == corev1.PodScheduled && cond.Reason == corev1.PodReasonUnschedulable &&
			(strings.Contains(cond.Message, "Insufficient cpu") || strings.Contains(cond.Message, "Insufficient memory")) {
			return true
		}
	}
	return false
}

func DeleteResourceOfPod(pod *corev1.Pod) {
	for i := range pod.Spec.Containers {
		pod.Spec.Containers[i].Resources.Requests = nil
		pod.Spec.Containers[i].Resources.Limits = nil
	}
}

func IsPodHasResource(pod corev1.Pod) bool {
	for _, cn := range pod.Spec.Containers {
		if len(cn.Resources.Requests) != 0 {
			return true
		}
	}
	return false
}

func RemoveFinalizer(ctx context.Context, client *k8sclient.K8sClient, pod *corev1.Pod, finalizer string) error {
	f := pod.GetFinalizers()
	for i := 0; i < len(f); i++ {
		if f[i] == finalizer {
			f = append(f[:i], f[i+1:]...)
			i--
		}
	}
	payload := []k8sclient.PatchListValue{{
		Op:    "replace",
		Path:  "/metadata/finalizers",
		Value: f,
	}}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		klog.ErrorS(err, "Parse json error")
		return err
	}
	if err := client.PatchPod(ctx, pod, payloadBytes, types.JSONPatchType); err != nil {
		klog.ErrorS(err, "Patch pod err")
		return err
	}
	return nil
}

func AddPodLabel(ctx context.Context, client *k8sclient.K8sClient, pod *corev1.Pod, addLabels map[string]string) error {
	payloads := map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": addLabels,
		},
	}

	payloadBytes, err := json.Marshal(payloads)
	if err != nil {
		klog.ErrorS(err, "Parse json error")
		return err
	}
	klog.Infof("add labels in pod[%s], labels:%v", pod.Name, addLabels)
	if err := client.PatchPod(ctx, pod, payloadBytes, types.StrategicMergePatchType); err != nil {
		klog.ErrorS(err, "Patch pod error", "podName", pod.Name)
		return err
	}
	return nil
}

func AddPodAnnotation(ctx context.Context, client *k8sclient.K8sClient, pod *corev1.Pod, addAnnotations map[string]string) error {
	payloads := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": addAnnotations,
		},
	}
	payloadBytes, err := json.Marshal(payloads)
	if err != nil {
		klog.ErrorS(err, "Parse json error")
		return err
	}
	klog.V(1).Info("add annotation in pod", "annotations", addAnnotations, "podName", pod.Name)
	if err := client.PatchPod(ctx, pod, payloadBytes, types.StrategicMergePatchType); err != nil {
		klog.ErrorS(err, "Patch pod error", "podName", pod.Name)
		return err
	}
	return nil
}

func DelPodAnnotation(ctx context.Context, client *k8sclient.K8sClient, pod *corev1.Pod, delAnnotations []string) error {
	payloads := []k8sclient.PatchDelValue{}
	for _, k := range delAnnotations {
		payloads = append(payloads, k8sclient.PatchDelValue{
			Op:   "remove",
			Path: fmt.Sprintf("/metadata/annotations/%s", k),
		})
	}
	payloadBytes, err := json.Marshal(payloads)
	if err != nil {
		klog.ErrorS(err, "Parse json error")
		return err
	}
	klog.V(1).Info("remove annotations of pod", "annotations", delAnnotations, "podName", pod.Name)
	if err := client.PatchPod(ctx, pod, payloadBytes, types.JSONPatchType); err != nil {
		klog.ErrorS(err, "Patch pod error", "podName", pod.Name)
		return err
	}
	return nil
}

func ReplacePodAnnotation(ctx context.Context, client *k8sclient.K8sClient, pod *corev1.Pod, annotation map[string]string) error {
	payload := []k8sclient.PatchMapValue{{
		Op:    "replace",
		Path:  "/metadata/annotations",
		Value: annotation,
	}}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		klog.ErrorS(err, "Parse json error")
		return err
	}
	klog.V(1).Info("Replace annotations of pod", "annotations", annotation, "podName", pod.Name)
	if err := client.PatchPod(ctx, pod, payloadBytes, types.JSONPatchType); err != nil {
		klog.ErrorS(err, "Patch pod error", "podName", pod.Name)
		return err
	}
	return nil
}

func GetAllRefKeys(pod corev1.Pod) map[string]string {
	annos := make(map[string]string)
	for k, v := range pod.Annotations {
		if k == util.GetReferenceKey(v) {
			annos[k] = v
		}
	}
	return annos
}

func WaitUtilMountReady(ctx context.Context, podName, mntPath string, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// Wait until the mount point is ready
	klog.Infof("schedule check mount pod [%s] status...", podName)
	for {
		var finfo os.FileInfo
		if err := util.DoWithTimeout(waitCtx, timeout, func() (err error) {
			finfo, err = os.Stat(mntPath)
			return err
		}); err != nil {
			if err == context.Canceled || err == context.DeadlineExceeded {
				break
			}
			klog.Infof("Mount path is not ready, wait for it. mountPath:%s, podName: %s, error: %v", mntPath, podName, err)
			// check err is transport endpoint is not connected
			if strings.Contains(err.Error(), "transport endpoint is not connected") {
				// umount target
				target := mntPath
				klog.Info("umount target before mkdir", " target:", target)
				for {
					command := exec.Command("umount", "-l", target)
					out, err := command.CombinedOutput()
					if err == nil {
						continue
					}
					klog.Info(string(out))
					if !strings.Contains(string(out), "not mounted") &&
						!strings.Contains(string(out), "mountpoint not found") &&
						!strings.Contains(string(out), "no mount point specified") {
						klog.ErrorS(err, "Could not lazy unmount", "target:", target, ", out:", string(out))
						return err
					}
					break
				}
			}
			time.Sleep(time.Millisecond * 500)
			continue
		}
		var dev uint64
		if st, ok := finfo.Sys().(*syscall.Stat_t); ok {
			if st.Ino == 1 {
				dev = uint64(st.Dev)
				util.DevMinorTableStore(mntPath, dev)
				klog.Infof("Mount point is ready, podName [%s]", podName)
				return nil
			}
			klog.V(6).Info("Mount point is not ready, wait for it", "podName", podName)
		}
		time.Sleep(time.Millisecond * 500)
	}

	return fmt.Errorf("mount point is not ready eventually, mountpod: %s", podName)
}

func ShouldDelay(ctx context.Context, pod *corev1.Pod, Client *k8s.K8sClient) (shouldDelay bool, err error) {
	delayStr, delayExist := pod.Annotations[config.DeleteDelayTimeKey]
	if !delayExist {
		// not set delete delay
		return false, nil
	}
	delayAtStr, delayAtExist := pod.Annotations[config.DeleteDelayAtKey]
	if !delayAtExist {
		// need to add delayAt annotation
		d, err := util.GetTimeAfterDelay(delayStr)
		if err != nil {
			klog.ErrorS(err, "delayDelete: can't parse delay time", "time", d)
			return false, nil
		}
		addAnnotation := map[string]string{config.DeleteDelayAtKey: d}
		klog.Info("delayDelete: add annotation to pod", "annotations", addAnnotation, "podName", pod.Name)
		if err := AddPodAnnotation(ctx, Client, pod, addAnnotation); err != nil {
			klog.ErrorS(err, "delayDelete: Update pod error", "podName", pod.Name)
			return true, err
		}
		return true, nil
	}
	delayAt, err := util.GetTime(delayAtStr)
	if err != nil {
		klog.ErrorS(err, "delayDelete: can't parse delayAt", "delayAt", delayAtStr)
		return false, nil
	}
	return time.Now().Before(delayAt), nil
}

func GetMountPathOfPod(pod corev1.Pod) (string, string, error) {
	if len(pod.Spec.Containers) == 0 {
		return "", "", fmt.Errorf("pod %v has no container", pod.Name)
	}
	cmd := pod.Spec.Containers[0].Command
	if cmd == nil || len(cmd) < 3 {
		return "", "", fmt.Errorf("get error pod command:%v", cmd)
	}
	sourcePath, volumeId, err := parseMntPath(cmd[2])
	if err != nil {
		return "", "", err
	}
	return sourcePath, volumeId, nil
}

// parseMntPath return mntPath, volumeId (/dfs/volumeId, volumeId err)
func parseMntPath(cmd string) (string, string, error) {
	// klog.Info("parse mnt path cmd:", cmd) // /scripts/mountpoint.sh dw-fs-1 s3 --role=client --args='-f -o default_permissions -o allow_other -o fsname=dw-fs-1 -o fstype=s3 -o user=dingofs -o conf=/dingofs/conf/client.conf /dfs/test-pv-ceph-2-kafthg'
	cmds := strings.Split(cmd, "\n")
	mountCmd := cmds[len(cmds)-1]
	args := strings.Fields(mountCmd)
	if len(args) == 0 {
		return "", "", fmt.Errorf("invalid command: %s", cmd)
	}
	if args[0] == "exec" {
		args = args[1:]
	}
	// klog.Info("args[2]:", args[2])
	// if len(args) < 3 || !strings.HasPrefix(args[2], config.PodMountBase) {
	// 	return "", "", fmt.Errorf("err cmd:%s", cmd)
	// }

	// Take the last argument as the mount path
	mountArg := args[len(args)-1]
	mountArg = strings.Trim(mountArg, "'")
	if !strings.HasPrefix(mountArg, config.PodMountBase) {
		return "", "", fmt.Errorf("invalid mountArg:%s", mountArg)
	}

	parts := strings.Split(mountArg, "/")
	if len(parts) < 3 {
		return "", "", fmt.Errorf("err mntPath:%s", args[2])
	}
	klog.Infof("mntPath:%s, volumeId: %s", mountArg, parts[2])
	return mountArg, parts[2], nil
}

func GetPVWithVolumeHandleOrAppInfo(ctx context.Context, client *k8s.K8sClient, volumeHandle string, volCtx map[string]string) (*corev1.PersistentVolume, *corev1.PersistentVolumeClaim, error) {
	if client == nil {
		return nil, nil, fmt.Errorf("k8s client is nil")
	}
	pv, err := client.GetPersistentVolume(ctx, volumeHandle)
	if k8serrors.IsNotFound(err) {
		// failed to get pv by volumeHandle, try to get pv by appName and appNamespace
		appName, appNamespace := volCtx[config.PodInfoName], volCtx[config.PodInfoNamespace]
		appPod, err := client.GetPod(ctx, appName, appNamespace)
		if err != nil {
			return nil, nil, err
		}
		for _, ref := range appPod.Spec.Volumes {
			if ref.PersistentVolumeClaim != nil {
				pvc, err := client.GetPersistentVolumeClaim(ctx, ref.PersistentVolumeClaim.ClaimName, appNamespace)
				if err != nil {
					return nil, nil, err
				}
				if pvc.Spec.VolumeName == "" {
					continue
				}
				appPV, err := client.GetPersistentVolume(ctx, pvc.Spec.VolumeName)
				if err != nil {
					return nil, nil, err
				}
				if appPV.Spec.CSI != nil && appPV.Spec.CSI.Driver == config.DriverName && appPV.Spec.CSI.VolumeHandle == volumeHandle {
					return appPV, pvc, nil
				}
			}
		}
	} else if err != nil {
		return nil, nil, err
	}

	if pv == nil {
		return nil, nil, fmt.Errorf("pv not found by volumeHandle %s", volumeHandle)
	}

	pvc, err := client.GetPersistentVolumeClaim(ctx, pv.Spec.ClaimRef.Name, pv.Spec.ClaimRef.Namespace)
	if err != nil {
		return nil, nil, err
	}
	return pv, pvc, nil
}

func GetErrContainerLog(ctx context.Context, client *k8s.K8sClient, podName string) (log string, err error) {
	pod, err := client.GetPod(ctx, podName, config.Namespace)
	if err != nil {
		return
	}
	for _, cn := range pod.Status.InitContainerStatuses {
		if !cn.Ready {
			log, err = client.GetPodLog(ctx, pod.Name, pod.Namespace, cn.Name)
			return
		}
	}
	for _, cn := range pod.Status.ContainerStatuses {
		if !cn.Ready {
			log, err = client.GetPodLog(ctx, pod.Name, pod.Namespace, cn.Name)
			return
		}
	}
	return
}

func GenMountPodName(ctx context.Context, client *k8sclient.K8sClient, dfsSetting *config.DfsSetting) (string, error) {
	labelSelector := &metav1.LabelSelector{MatchLabels: map[string]string{
		config.PodTypeKey:          config.PodTypeValue,
		config.PodVolumeIdLabelKey: dfsSetting.UniqueId,
		config.PodHashLabelKey:     dfsSetting.HashVal,
	}}
	selectorJSON, err := json.MarshalIndent(labelSelector, "", "  ")
	if err != nil {
		klog.ErrorS(err, "Failed to marshal selector to JSON")
	} else {
		klog.Infof("pod selector label: %s", string(selectorJSON))
	}
	pods, err := client.ListPod(ctx, config.Namespace, labelSelector, nil)
	if err != nil {
		klog.ErrorS(err, "List pods of uniqueId:%s, hashVal:%s", dfsSetting.UniqueId, dfsSetting.HashVal)
		return "", err
	}
	for _, pod := range pods {
		if pod.DeletionTimestamp != nil {
			continue
		}
		klog.Infof("pod spec info: %v", pod.Spec)
		if pod.Spec.NodeName == config.NodeName || pod.Spec.NodeSelector["kubernetes.io/hostname"] == config.NodeName {
			return pod.Name, nil
		}
	}
	return GenPodNameByUniqueId(dfsSetting.UniqueId, true), nil
}

func GenPodNameByUniqueId(uniqueId string, withRandom bool) string {
	if !withRandom {
		return fmt.Sprintf("dingofs-%s-%s", config.NodeName, uniqueId)
	}
	podName := fmt.Sprintf("dingofs-%s-%s-%s", config.NodeName, uniqueId, util.RandStringRunes(6)) // e.g.dingofs-node1-pvc-839e19a3-de96-4e2b-91f7-5bf5d55d0fcb-ktxexr
	fmt.Printf("mount pod name:%s", podName)
	return podName
}

func SetMountLabel(ctx context.Context, client *k8sclient.K8sClient, uniqueId, mountPodName string, podName, podNamespace string) (err error) {
	var pod *corev1.Pod
	pod, err = client.GetPod(context.Background(), podName, podNamespace)
	if err != nil {
		return err
	}
	klog.Info("set mount pod name:", podName)
	if err := AddPodLabel(ctx, client, pod, map[string]string{config.UniqueId: ""}); err != nil {
		return err
	}

	return nil
}

func SetFSIDAnnotation(ctx context.Context, client *k8sclient.K8sClient, podName string, fsid string) (err error) {
	var pod *corev1.Pod
	pod, err = client.GetPod(context.Background(), podName, config.Namespace)
	if err != nil {
		return err
	}
	klog.Infof("set pod annotation podName:[%s] , dingofs-fsid:[%s]", podName, fsid)
	return AddPodAnnotation(ctx, client, pod, map[string]string{config.DingoFSID: fsid})
}

func CreateOrUpdateSecret(ctx context.Context, client *k8sclient.K8sClient, secret *corev1.Secret) error {
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		oldSecret, err := client.GetSecret(ctx, secret.Name, config.Namespace)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				// secret not exist, create
				_, err := client.CreateSecret(ctx, secret)
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
		klog.Info("secret info:", oldSecret)
		return client.UpdateSecret(ctx, oldSecret)
	})
	if err != nil {
		klog.ErrorS(err, "create or update secret error", "secretName", secret.Name)
		return err
	}
	return nil
}
