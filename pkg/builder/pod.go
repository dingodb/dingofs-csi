/*
Copyright 2021 Juicedata Inc

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
	"fmt"
	"path"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jackblack369/dingofs-csi/pkg/config"
)

type PodBuilder struct {
	BaseBuilder
}

func NewPodBuilder(setting *config.DfsSetting, capacity int64) *PodBuilder {
	return &PodBuilder{
		BaseBuilder: BaseBuilder{
			dfsSetting: setting,
			capacity:   capacity,
		},
	}
}

// NewMountPod generates a pod with dingofs client
func (r *PodBuilder) NewMountPod(podName string) (*corev1.Pod, error) {
	pod := r.genCommonJuicePod(r.genCommonContainer)

	pod.Name = podName
	mountCmd := r.genMountCommand()
	cmd := mountCmd
	initCmd := r.genInitCommand()
	if initCmd != "" {
		cmd = strings.Join([]string{initCmd, mountCmd}, "\n")
	}
	pod.Spec.Containers[0].Command = []string{"sh", "-c", cmd}
	pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, corev1.EnvVar{
		Name:  "JFS_FOREGROUND",
		Value: "1",
	})

	// inject fuse fd
	// if podName != "" && util.SupportFusePass(pod.Spec.Containers[0].Image) {
	// 	fdAddress, err := fuse.GlobalFds.GetFdAddress(context.TODO(), r.jfsSetting.HashVal)
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// 	pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, corev1.EnvVar{
	// 		Name:  JfsCommEnv,
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
			Name: JfsDirName,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: config.MountPointPath,
					Type: &dir,
				},
			},
		},
		{
			Name: JfsFuseFdPathName,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: path.Join(JfsFuseFsPathInHost, r.dfsSetting.HashVal),
					Type: &dir,
				},
			},
		},
	}
	volumeMounts := []corev1.VolumeMount{
		{
			Name:             JfsDirName,
			MountPath:        config.PodMountBase,
			MountPropagation: &mp,
		},
		{
			Name:      JfsFuseFdPathName,
			MountPath: JfsFuseFsPathInPod,
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
				Command:      []string{"sh", "-c", "rm -rf /var/jfsCache/*/chunks"},
				VolumeMounts: cacheVolumeMounts,
			}},
			Volumes: cacheVolumes,
		},
	}
	return pod
}