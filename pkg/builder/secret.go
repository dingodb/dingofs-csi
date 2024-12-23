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
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jackblack369/dingofs-csi/pkg/config"
)

const (
	checkMountScriptName = "check_mount.sh"
	checkMountScriptPath = "/" + checkMountScriptName
)

var (
	checkMountScriptContent = `ConditionPathIsMountPoint="$1"
count=0
while ! mount | grep $ConditionPathIsMountPoint | grep DingoFS
do
    sleep 3
    count=¬expr $count + 1¬
    if test $count -eq 10
    then
        echo "timed out!"
        exit 1
    fi
done
echo "$(date "+%Y-%m-%d %H:%M:%S")"
echo "succeed in checking mount point $ConditionPathIsMountPoint"
if [ -n "${subpath}" ]; then
	if [ -n "${capacity}" ]; then
		if [ "${community}" == ce ]; then
			echo "set quota in ${subpath}"
			/usr/local/bin/dingofs quota > /dev/null; 
			if [ $? -eq 0 ]; then 
				/usr/local/bin/dingofs quota set ${metaurl} --path ${quotaPath} --capacity ${capacity} &
			fi 
		fi
		if [ "${community}" == ee ]; then
			echo "set quota in ${subpath}"
			/usr/bin/dingofs quota > /dev/null; if [ $? -eq 0 ]; then /usr/bin/dingofs quota set ${name} --path ${quotaPath} --capacity ${capacity}; fi; 
		fi
	fi
fi
`
)

func (r *BaseBuilder) NewSecret() corev1.Secret {
	data := make(map[string]string)
	if r.dfsSetting.MdsAddr != "" {
		data["mdsaddr"] = r.dfsSetting.MdsAddr
	}
	if r.dfsSetting.SecretKey != "" {
		data["secretkey"] = r.dfsSetting.SecretKey
	}
	if r.dfsSetting.SecretKey2 != "" {
		data["secretkey2"] = r.dfsSetting.SecretKey2
	}
	if r.dfsSetting.Token != "" {
		data["token"] = r.dfsSetting.Token
	}
	if r.dfsSetting.Passphrase != "" {
		data["passphrase"] = r.dfsSetting.Passphrase
	}
	if r.dfsSetting.EncryptRsaKey != "" {
		data["encrypt_rsa_key"] = r.dfsSetting.EncryptRsaKey
	}
	if r.dfsSetting.InitConfig != "" {
		data["initconfig"] = r.dfsSetting.InitConfig
	}
	replacer := strings.NewReplacer("¬", "`")
	data[checkMountScriptName] = replacer.Replace(checkMountScriptContent)
	if options, err := r.dfsSetting.ParseFormatOptions(); err == nil {
		for _, pair := range options {
			if pair[0] == "session-token" {
				data["session-token"] = pair[1]
			}
		}
	}
	for k, v := range r.dfsSetting.Envs {
		data[k] = v
	}
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.dfsSetting.Attr.Namespace,
			Name:      r.dfsSetting.SecretName,
			Labels: map[string]string{
				config.DingofsSecretLabelKey: "true",
			},
		},
		StringData: data,
	}
	return secret
}

func SetPodAsOwner(secret *corev1.Secret, owner corev1.Pod) {
	controller := true
	secret.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: "v1",
		Kind:       "Pod",
		Name:       owner.Name,
		UID:        owner.UID,
		Controller: &controller,
	}})
}

func SetPVCAsOwner(secret *corev1.Secret, owner *corev1.PersistentVolumeClaim) {
	controller := true
	secret.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: "v1",
		Kind:       "PersistentVolumeClaim",
		Name:       owner.Name,
		UID:        owner.UID,
		Controller: &controller,
	}})
}

func SetPVAsOwner(secret *corev1.Secret, owner *corev1.PersistentVolume) {
	if owner == nil {
		return
	}
	controller := false
	secret.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: "v1",
		Kind:       "PersistentVolume",
		Name:       owner.Name,
		UID:        owner.UID,
		Controller: &controller,
	}})
}

func SetJobAsOwner(secret *corev1.Secret, owner batchv1.Job) {
	controller := true
	secret.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: "batch/v1",
		Kind:       "Job",
		Name:       owner.Name,
		UID:        owner.UID,
		Controller: &controller,
	}})
}
