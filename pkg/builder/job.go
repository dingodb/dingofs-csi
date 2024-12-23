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

package builder

import (
	"crypto/sha256"
	"fmt"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	"github.com/jackblack369/dingofs-csi/pkg/config"
	"github.com/jackblack369/dingofs-csi/pkg/util"
	"github.com/jackblack369/dingofs-csi/pkg/util/security"
)

const DefaultJobTTLSecond = int32(5)

type JobBuilder struct {
	PodBuilder
}

func NewJobBuilder(setting *config.DfsSetting, capacity int64) *JobBuilder {
	return &JobBuilder{PodBuilder{
		BaseBuilder: BaseBuilder{
			dfsSetting: setting,
			capacity:   capacity,
		}},
	}
}

func (r *JobBuilder) NewJobForCreateVolume() *batchv1.Job {
	jobName := GenJobNameByVolumeId(r.dfsSetting.VolumeId) + "-createvol"
	job := r.newJob(jobName)
	jobCmd := r.getCreateVolumeCmd()
	initCmd := r.genInitFSCommand()
	cmd := strings.Join([]string{initCmd, jobCmd}, "\n")
	job.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", cmd}

	klog.Info("create volume job command:", jobCmd)
	return job
}

func (r *JobBuilder) NewJobForDeleteVolume() *batchv1.Job {
	jobName := GenJobNameByVolumeId(r.dfsSetting.VolumeId) + "-delvol"
	job := r.newJob(jobName)
	jobCmd := r.getDeleteVolumeCmd()
	initCmd := r.genInitFSCommand()
	cmd := strings.Join([]string{initCmd, jobCmd}, "\n")
	job.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", cmd}
	klog.Info("delete volume job command:", jobCmd)
	return job
}

func (r *JobBuilder) NewJobForCleanCache() *batchv1.Job {
	jobName := GenJobNameByVolumeId(r.dfsSetting.VolumeId) + "-cleancache-" + util.RandStringRunes(6)
	job := r.newCleanJob(jobName)
	return job
}

func GenJobNameByVolumeId(volumeId string) string {
	h := sha256.New()
	h.Write([]byte(volumeId))
	return fmt.Sprintf("dingofs-%x", h.Sum(nil))[:16]
}

func (r *JobBuilder) newJob(jobName string) *batchv1.Job {
	secretName := jobName + "-secret"
	r.dfsSetting.SecretName = secretName
	podTemplate := r.genCommonPod(r.genCommonContainer)
	ttlSecond := DefaultJobTTLSecond
	podTemplate.Spec.Containers[0].Lifecycle = &corev1.Lifecycle{
		PreStop: &corev1.LifecycleHandler{
			Exec: &corev1.ExecAction{Command: []string{"sh", "-c", "umount /mnt/dfs -l && rmdir /mnt/dfs"}},
		},
	}
	// set node name to empty to let k8s scheduler to choose a node
	podTemplate.Spec.NodeName = ""
	// set NodeSelector/Affinity/Tolerations follow the csi-node
	podTemplate.Spec.NodeSelector = config.CSIPod.Spec.NodeSelector
	podTemplate.Spec.Affinity = config.CSIPod.Spec.Affinity
	podTemplate.Spec.Tolerations = config.CSIPod.Spec.Tolerations
	// set priority class name to empty to make job use default priority class
	podTemplate.Spec.PriorityClassName = ""
	podTemplate.Spec.RestartPolicy = corev1.RestartPolicyOnFailure
	job := batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: r.dfsSetting.Attr.Namespace,
			Labels: map[string]string{
				config.JobTypeKey: config.JobTypeValue,
			},
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name:      jobName,
					Namespace: r.dfsSetting.Attr.Namespace,
				},
				Spec: podTemplate.Spec,
			},
			TTLSecondsAfterFinished: &ttlSecond,
		},
	}
	return &job
}

func (r *JobBuilder) newCleanJob(jobName string) *batchv1.Job {
	podTemplate := r.genCleanCachePod()
	ttlSecond := DefaultJobTTLSecond
	podTemplate.Spec.RestartPolicy = corev1.RestartPolicyNever
	podTemplate.Spec.NodeName = config.NodeName
	job := batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: r.dfsSetting.Attr.Namespace,
			Labels: map[string]string{
				config.JobTypeKey: config.JobTypeValue,
			},
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name:      jobName,
					Namespace: r.dfsSetting.Attr.Namespace,
				},
				Spec: podTemplate.Spec,
			},
			TTLSecondsAfterFinished: &ttlSecond,
		},
	}
	return &job
}

func (r *JobBuilder) getCreateVolumeCmd() string {
	cmd := r.getJobCommand()
	subpath := security.EscapeBashStr(r.dfsSetting.SubPath)
	return fmt.Sprintf("%s && if [ ! -d /mnt/dfs/%s ]; then mkdir -m 777 /mnt/dfs/%s; fi;", cmd, subpath, subpath)
}

func (r *JobBuilder) getDeleteVolumeCmd() string {
	cmd := r.getJobCommand()
	dfsPath := config.DfsCMDPath
	subpath := security.EscapeBashStr(r.dfsSetting.SubPath)
	// TODO modify the command to delete the volume
	return fmt.Sprintf("%s && if [ -d /mnt/dfs/%s ]; then %s rmr /mnt/dfs/%s; fi;", cmd, subpath, dfsPath, subpath)
}

func NewFuseAbortJob(mountpod *corev1.Pod, devMinor uint32) *batchv1.Job {
	jobName := fmt.Sprintf("%s-abort-fuse", GenJobNameByVolumeId(mountpod.Name))
	ttlSecond := DefaultJobTTLSecond
	privileged := true
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: mountpod.Namespace,
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: &ttlSecond,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "fuse-abort",
							Image:           mountpod.Spec.Containers[0].Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command: []string{
								"sh",
								"-c",
								fmt.Sprintf(
									"if [ $(cat /sys/fs/fuse/connections/%d/waiting) -gt 0 ]; then echo 1 > /sys/fs/fuse/connections/%d/abort; fi;",
									devMinor, devMinor),
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged: &privileged,
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "fuse-connections",
									MountPath: "/sys/fs/fuse/connections",
								},
							},
						},
					},
					NodeName:      mountpod.Spec.NodeName,
					RestartPolicy: corev1.RestartPolicyNever,
					Volumes: []corev1.Volume{
						{
							Name: "fuse-connections",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/sys/fs/fuse/connections",
								},
							},
						},
					},
				},
			},
		},
	}
}
