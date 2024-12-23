/*
 Copyright 2021 Juicedata Inc
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

package builder

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	"github.com/jackblack369/dingofs-csi/pkg/config"
	"github.com/jackblack369/dingofs-csi/pkg/k8sclient"
	"github.com/jackblack369/dingofs-csi/pkg/util/script"
)

type PodBuilder struct {
	BaseBuilder
	K8sClient *k8sclient.K8sClient
}

func NewPodBuilder(setting *config.DfsSetting, capacity int64, k8sCli *k8sclient.K8sClient) *PodBuilder {
	return &PodBuilder{
		BaseBuilder: BaseBuilder{
			dfsSetting: setting,
			capacity:   capacity,
		},
		K8sClient: k8sCli,
	}
}

// NewMountPod generates a pod with dingo-fuse bootstrap
func (r *PodBuilder) NewMountPod(podName string) (*corev1.Pod, error) {
	pod := r.genCommonPod(r.genCommonContainer)

	err := r.initFuseConfig(pod)
	if err != nil {
		return nil, err
	}

	pod.Name = podName
	klog.V(6).Infof("mount pod[%s] spec info[%#v]", podName, pod.Spec)
	//mountCmd := r.genMountFSCommand()
	//cmd := mountCmd
	// initCmd := r.genInitFSCommand()
	// if initCmd != "" {
	// 	cmd = strings.Join([]string{initCmd, mountCmd}, "\n")
	// }
	//

	// transfer dingo-fuse args to pod's bootstrap cmd

	// var mountFsArgs []string
	// mountFsArgs = append(mountFsArgs, "-o fstype=s3 -o default_permissions -o allow_other")
	// mountFsArgs = append(mountFsArgs, "-o conf="+r.dfsSetting.ClientConfPath, "-o fsname="+r.dfsSetting.Name, r.dfsSetting.MountPath)
	// mountFsArgsStr := strings.Join(mountFsArgs, " ")
	// mountFsCMD := fmt.Sprintf("%s --role=client --args='%s'", config.DefaultBootstrapPath+"/"+config.DefaultBootstrapShell, mountFsArgsStr) // /scripts/mountpoint.sh --role=client --args='-o default_permissions...'

	format := strings.Join(config.FORMAT_FUSE_ARGS, " ")
	fuseArgs := fmt.Sprintf(format, r.dfsSetting.Name, "s3", r.dfsSetting.ClientConfPath, r.dfsSetting.MountPath)
	mountFsCMD := fmt.Sprintf("%s %s %s --role=client --args='%s'", config.DefaultBootstrapPath+"/"+config.DefaultBootstrapShell, r.dfsSetting.Name, "s3", fuseArgs)

	pod.Spec.Containers[0].Command = []string{"sh", "-c", mountFsCMD}

	pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, corev1.EnvVar{
		Name:  "DFS_FOREGROUND",
		Value: "1",
	})

	// erase: inject fuse fd
	// if podName != "" && util.SupportFusePass(pod.Spec.Containers[0].Image) {
	// 	fdAddress, err := fuse.GlobalFds.GetFdAddress(context.TODO(), r.dfsSetting.HashVal)
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// 	pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, corev1.EnvVar{
	// 		Name:  DfsCommEnv,
	// 		Value: fdAddress,
	// 	})
	// }

	// generate volumes and volumeMounts only used in mount pod
	volumes, volumeMounts := r.genPodVolumes()
	pod.Spec.Volumes = append(pod.Spec.Volumes, volumes...)
	pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, volumeMounts...)

	// add cache-dir hostpath & PVC volume
	cacheVolumes, cacheVolumeMounts := r.genCacheDirVolumes()
	pod.Spec.Volumes = append(pod.Spec.Volumes, cacheVolumes...)
	pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, cacheVolumeMounts...)

	// add mount path host path volume
	mountVolumes, mountVolumeMounts := r.genHostPathVolumes()
	pod.Spec.Volumes = append(pod.Spec.Volumes, mountVolumes...)
	pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, mountVolumeMounts...)

	// add users custom volumes, volumeMounts, volumeDevices
	if r.dfsSetting.Attr.Volumes != nil {
		pod.Spec.Volumes = append(pod.Spec.Volumes, r.dfsSetting.Attr.Volumes...)
	}
	if r.dfsSetting.Attr.VolumeMounts != nil {
		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, r.dfsSetting.Attr.VolumeMounts...)
	}
	if r.dfsSetting.Attr.VolumeDevices != nil {
		pod.Spec.Containers[0].VolumeDevices = append(pod.Spec.Containers[0].VolumeDevices, r.dfsSetting.Attr.VolumeDevices...)
	}

	return pod, nil
}

// initFuseConfig: set outer specify script to pod bootstrap command
func (r *PodBuilder) initFuseConfig(pod *corev1.Pod) error {
	mountScriptContent := script.SHELL_MOUNT_POINT

	// create a ConfigMap with the specified content
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "dingo-fuse-config",
		},
		Data: map[string]string{
			config.DefaultBootstrapShell: mountScriptContent,
		},
	}

	// Create the ConfigMap in the Kubernetes cluster
	_, err := r.K8sClient.CoreV1().ConfigMaps(r.dfsSetting.Attr.Namespace).Create(context.TODO(), configMap, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		klog.ErrorS(err, "Failed to create ConfigMap")
		return err
	}

	// Add ConfigMap volume with defaultMode set to 0755
	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name: "config-volume",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: configMap.Name,
				},
				DefaultMode: func() *int32 {
					mode := int32(0755)
					return &mode
				}(),
			},
		},
	})

	// Mount the volume into the container
	pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
		Name:      "config-volume",
		MountPath: config.DefaultBootstrapPath,
	})
	return nil
}

// genCommonContainer: generate common privileged container
func (r *PodBuilder) genCommonContainer() corev1.Container {
	isPrivileged := true
	rootUser := int64(0)
	return corev1.Container{
		Name:  config.MountContainerName,
		Image: r.BaseBuilder.dfsSetting.Attr.Image,
		SecurityContext: &corev1.SecurityContext{
			Privileged: &isPrivileged,
			RunAsUser:  &rootUser,
		},
		Env: []corev1.EnvVar{
			{
				Name:  config.DfsInsideContainer,
				Value: "1",
			},
		},
	}
}

// genCacheDirVolumes: generate cache-dir hostpath & PVC volume
func (r *PodBuilder) genCacheDirVolumes() ([]corev1.Volume, []corev1.VolumeMount) {
	cacheVolumes := []corev1.Volume{}
	cacheVolumeMounts := []corev1.VolumeMount{}

	hostPathType := corev1.HostPathDirectoryOrCreate

	for idx, cacheDir := range r.dfsSetting.CacheDirs {
		name := fmt.Sprintf("cachedir-%d", idx)

		hostPath := corev1.HostPathVolumeSource{
			Path: cacheDir,
			Type: &hostPathType,
		}
		hostPathVolume := corev1.Volume{
			Name: name,
			VolumeSource: corev1.VolumeSource{
				HostPath: &hostPath,
			},
		}
		cacheVolumes = append(cacheVolumes, hostPathVolume)

		volumeMount := corev1.VolumeMount{
			Name:      name,
			MountPath: cacheDir,
		}
		cacheVolumeMounts = append(cacheVolumeMounts, volumeMount)
	}

	for i, cache := range r.dfsSetting.CachePVCs {
		name := fmt.Sprintf("cachedir-pvc-%d", i)
		pvcVolume := corev1.Volume{
			Name: name,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: cache.PVCName,
					ReadOnly:  false,
				},
			},
		}
		cacheVolumes = append(cacheVolumes, pvcVolume)
		volumeMount := corev1.VolumeMount{
			Name:      name,
			ReadOnly:  false,
			MountPath: cache.Path,
		}
		cacheVolumeMounts = append(cacheVolumeMounts, volumeMount)
	}

	if r.dfsSetting.CacheEmptyDir != nil {
		name := "cachedir-empty-dir"
		emptyVolume := corev1.Volume{
			Name: name,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium:    corev1.StorageMedium(r.dfsSetting.CacheEmptyDir.Medium),
					SizeLimit: &r.dfsSetting.CacheEmptyDir.SizeLimit,
				},
			},
		}
		cacheVolumes = append(cacheVolumes, emptyVolume)
		volumeMount := corev1.VolumeMount{
			Name:      name,
			ReadOnly:  false,
			MountPath: r.dfsSetting.CacheEmptyDir.Path,
		}
		cacheVolumeMounts = append(cacheVolumeMounts, volumeMount)
	}

	if r.dfsSetting.CacheInlineVolumes != nil {
		for i, inlineVolume := range r.dfsSetting.CacheInlineVolumes {
			name := fmt.Sprintf("cachedir-inline-volume-%d", i)
			cacheVolumes = append(cacheVolumes, corev1.Volume{
				Name:         name,
				VolumeSource: corev1.VolumeSource{CSI: inlineVolume.CSI},
			})
			volumeMount := corev1.VolumeMount{
				Name:      name,
				ReadOnly:  false,
				MountPath: inlineVolume.Path,
			}
			cacheVolumeMounts = append(cacheVolumeMounts, volumeMount)
		}
	}

	return cacheVolumes, cacheVolumeMounts
}

// genHostPathVolumes: generate host path volumes
func (r *PodBuilder) genHostPathVolumes() (volumes []corev1.Volume, volumeMounts []corev1.VolumeMount) {
	volumes = []corev1.Volume{}
	volumeMounts = []corev1.VolumeMount{}
	if len(r.dfsSetting.HostPath) == 0 {
		return
	}
	for idx, hostPath := range r.dfsSetting.HostPath {
		name := fmt.Sprintf("hostpath-%d", idx)
		volumes = append(volumes, corev1.Volume{
			Name: name,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: hostPath,
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      name,
			MountPath: hostPath,
		})
	}
	return
}

// genPodVolumes: generate volumes for mount pod
// 1. dfs dir: mount point used to propagate the mount point in the mount container to host
// 2. update db dir: mount updatedb.conf from host to mount pod
// 3. dfs fuse fd path: mount fuse fd pass socket to mount pod
func (r *PodBuilder) genPodVolumes() ([]corev1.Volume, []corev1.VolumeMount) {
	dir := corev1.HostPathDirectoryOrCreate
	file := corev1.HostPathFileOrCreate
	mp := corev1.MountPropagationBidirectional
	volumes := []corev1.Volume{
		{
			Name: DfsDirName,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: config.MountPointPath,
					Type: &dir,
				},
			},
		},
		{
			Name: DfsFuseFdPathName,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: path.Join(DfsFuseFsPathInHost, r.dfsSetting.HashVal),
					Type: &dir,
				},
			},
		},
	}
	volumeMounts := []corev1.VolumeMount{
		{
			Name:             DfsDirName,
			MountPath:        config.PodMountBase,
			MountPropagation: &mp,
		},
		{
			Name:      DfsFuseFdPathName,
			MountPath: DfsFuseFsPathInPod,
		},
	}

	if !config.Immutable {
		volumes = append(volumes, corev1.Volume{
			Name: UpdateDBDirName,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: UpdateDBCfgFile,
					Type: &file,
				},
			}},
		)
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      UpdateDBDirName,
			MountPath: UpdateDBCfgFile,
		})
	}

	return volumes, volumeMounts
}

// genCleanCachePod: generate pod to clean cache in host
func (r *PodBuilder) genCleanCachePod() *corev1.Pod {
	volumeMountPrefix := "/var/dfsCache"
	cacheVolumes := []corev1.Volume{}
	cacheVolumeMounts := []corev1.VolumeMount{}

	hostPathType := corev1.HostPathDirectory

	for idx, cacheDir := range r.dfsSetting.CacheDirs {
		name := fmt.Sprintf("cachedir-%d", idx)

		hostPathVolume := corev1.Volume{
			Name: name,
			VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{
				Path: filepath.Join(cacheDir, r.dfsSetting.FSID, "raw"),
				Type: &hostPathType,
			}},
		}
		cacheVolumes = append(cacheVolumes, hostPathVolume)

		volumeMount := corev1.VolumeMount{
			Name:      name,
			MountPath: filepath.Join(volumeMountPrefix, name),
		}
		cacheVolumeMounts = append(cacheVolumeMounts, volumeMount)
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.dfsSetting.Attr.Namespace,
			Labels: map[string]string{
				config.PodTypeKey: config.PodTypeValue,
			},
			Annotations: make(map[string]string),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:         "dfs-cache-clean",
				Image:        r.dfsSetting.Attr.Image,
				Command:      []string{"sh", "-c", "rm -rf /var/dfsCache/*/chunks"},
				VolumeMounts: cacheVolumeMounts,
			}},
			Volumes: cacheVolumes,
		},
	}
	return pod
}
