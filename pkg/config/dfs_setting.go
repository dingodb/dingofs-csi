package config

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackblack369/dingofs-csi/pkg/k8sclient"
	"github.com/jackblack369/dingofs-csi/pkg/util/security"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/klog/v2"
)

type DfsSetting struct {
	HashVal string `json:"-"`

	FSID           string   // fsid of DingoFS
	Name           string   `json:"name"`
	MdsAddr        string   `json:"mdsaddr"`
	Source         string   `json:"source"`
	Storage        string   `json:"storage"`
	FormatOptions  string   `json:"format-options"`
	CacheDirs      []string // hostPath using by mount pod
	ClientConfPath string   `json:"-"`

	CachePVCs          []CachePVC           // PVC using by mount pod
	CacheEmptyDir      *CacheEmptyDir       // EmptyDir using by mount pod
	CacheInlineVolumes []*CacheInlineVolume // InlineVolume using by mount pod

	// mount
	VolumeId   string   // volumeHandle of PV
	UniqueId   string   // mount pod name is generated by uniqueId
	MountPath  string   // mountPath of mount pod
	TargetPath string   // which bind to container path
	Options    []string // mount options
	SubPath    string   // subPath which is to be created or deleted
	SecretName string   // secret with DingoFS volume credentials

	Attr *PodAttr

	// put in secret
	SecretKey     string            `json:"secret-key,omitempty"`
	SecretKey2    string            `json:"secret-key2,omitempty"`
	Token         string            `json:"token,omitempty"`
	Passphrase    string            `json:"passphrase,omitempty"`
	Envs          map[string]string `json:"envs_map,omitempty"`
	EncryptRsaKey string            `json:"encrypt_rsa_key,omitempty"`
	InitConfig    string            `json:"initconfig,omitempty"`
	Configs       map[string]string `json:"configs_map,omitempty"`

	// put in volCtx
	DeletedDelay string   `json:"deleted_delay"`
	CleanCache   bool     `json:"clean_cache"`
	HostPath     []string `json:"host_path"`

	FormatCmd string // format or auth

	PV  *corev1.PersistentVolume      `json:"-"`
	PVC *corev1.PersistentVolumeClaim `json:"-"`
}

type AppInfo struct {
	Name      string
	Namespace string
}

type PodAttr struct {
	Namespace            string
	MountPointPath       string
	DFSConfigPath        string
	DFSMountPriorityName string
	ServiceAccountName   string

	Resources corev1.ResourceRequirements

	Labels                        map[string]string     `json:"labels,omitempty"`
	Annotations                   map[string]string     `json:"annotations,omitempty"`
	LivenessProbe                 *corev1.Probe         `json:"livenessProbe,omitempty"`
	ReadinessProbe                *corev1.Probe         `json:"readinessProbe,omitempty"`
	StartupProbe                  *corev1.Probe         `json:"startupProbe,omitempty"`
	Lifecycle                     *corev1.Lifecycle     `json:"lifecycle,omitempty"`
	TerminationGracePeriodSeconds *int64                `json:"terminationGracePeriodSeconds,omitempty"`
	Volumes                       []corev1.Volume       `json:"volumes,omitempty"`
	VolumeDevices                 []corev1.VolumeDevice `json:"volumeDevices,omitempty"`
	VolumeMounts                  []corev1.VolumeMount  `json:"volumeMounts,omitempty"`
	Env                           []corev1.EnvVar       `json:"env,omitempty"`

	// inherit from csi
	Image            string
	HostNetwork      bool
	HostAliases      []corev1.HostAlias
	HostPID          bool
	HostIPC          bool
	DNSConfig        *corev1.PodDNSConfig
	DNSPolicy        corev1.DNSPolicy
	ImagePullSecrets []corev1.LocalObjectReference
	PreemptionPolicy *corev1.PreemptionPolicy
	Tolerations      []corev1.Toleration
}

type PVCSelector struct {
	metav1.LabelSelector
	MatchStorageClassName string `json:"matchStorageClassName,omitempty"`
	MatchName             string `json:"matchName,omitempty"`
}

type MountPodPatch struct {
	// used to specify the selector for the PVC that will be patched
	// omit will patch for all PVC
	PVCSelector *PVCSelector `json:"pvcSelector,omitempty"`

	MountImage string `json:"mountImage,omitempty"`

	Image                         string                       `json:"-"`
	Labels                        map[string]string            `json:"labels,omitempty"`
	Annotations                   map[string]string            `json:"annotations,omitempty"`
	HostNetwork                   *bool                        `json:"hostNetwork,omitempty" `
	HostPID                       *bool                        `json:"hostPID,omitempty" `
	LivenessProbe                 *corev1.Probe                `json:"livenessProbe,omitempty"`
	ReadinessProbe                *corev1.Probe                `json:"readinessProbe,omitempty"`
	StartupProbe                  *corev1.Probe                `json:"startupProbe,omitempty"`
	Lifecycle                     *corev1.Lifecycle            `json:"lifecycle,omitempty"`
	Resources                     *corev1.ResourceRequirements `json:"resources,omitempty"`
	TerminationGracePeriodSeconds *int64                       `json:"terminationGracePeriodSeconds,omitempty"`
	Volumes                       []corev1.Volume              `json:"volumes,omitempty"`
	VolumeDevices                 []corev1.VolumeDevice        `json:"volumeDevices,omitempty"`
	VolumeMounts                  []corev1.VolumeMount         `json:"volumeMounts,omitempty"`
	Env                           []corev1.EnvVar              `json:"env,omitempty"`
	MountOptions                  []string                     `json:"mountOptions,omitempty"`
}

type CachePVC struct {
	PVCName string
	Path    string
}

type CacheInlineVolume struct {
	CSI  *corev1.CSIVolumeSource
	Path string
}

type CacheEmptyDir struct {
	Medium    string
	SizeLimit resource.Quantity
	Path      string
}

func (mpp *MountPodPatch) IsMatch(pvc *corev1.PersistentVolumeClaim) bool {
	if mpp.PVCSelector == nil {
		return true
	}
	if pvc == nil {
		return false
	}
	if mpp.PVCSelector.MatchName != "" && mpp.PVCSelector.MatchName != pvc.Name {
		return false
	}
	if mpp.PVCSelector.MatchStorageClassName != "" && pvc.Spec.StorageClassName != nil && mpp.PVCSelector.MatchStorageClassName != *pvc.Spec.StorageClassName {
		return false
	}
	selector, err := metav1.LabelSelectorAsSelector(&mpp.PVCSelector.LabelSelector)
	if err != nil {
		return false
	}
	return selector.Matches(labels.Set(pvc.Labels))
}

func (mpp *MountPodPatch) DeepCopy() MountPodPatch {
	var copy MountPodPatch
	data, _ := json.Marshal(mpp)
	_ = json.Unmarshal(data, &copy)
	return copy
}

func (mpp *MountPodPatch) Merge(mp MountPodPatch) {
	mpp.MountImage = mp.MountImage
	if mp.HostNetwork != nil {
		mpp.HostNetwork = mp.HostNetwork
	}
	if mp.HostPID != nil {
		mpp.HostPID = mp.HostPID
	}
	if mp.LivenessProbe != nil {
		mpp.LivenessProbe = mp.LivenessProbe
	}
	if mp.ReadinessProbe != nil {
		mpp.ReadinessProbe = mp.ReadinessProbe
	}
	if mp.ReadinessProbe != nil {
		mpp.ReadinessProbe = mp.ReadinessProbe
	}
	if mp.Lifecycle != nil {
		mpp.Lifecycle = mp.Lifecycle
	}
	if mp.Labels != nil {
		mpp.Labels = mp.Labels
	}
	if mp.Annotations != nil {
		mpp.Annotations = mp.Annotations
	}
	if mp.Resources != nil {
		mpp.Resources = mp.Resources
	}
	if mp.TerminationGracePeriodSeconds != nil {
		mpp.TerminationGracePeriodSeconds = mp.TerminationGracePeriodSeconds
	}
	vok := make(map[string]bool)
	if mp.Volumes != nil {
		if mpp.Volumes == nil {
			mpp.Volumes = []corev1.Volume{}
		}
		for _, v := range mp.Volumes {
			if IsInterVolume(v.Name) {
				klog.Info("applyConfig: volume uses an internal volume name, ignore", "volume", v.Name)
				continue
			}
			found := false
			for _, vv := range mpp.Volumes {
				if vv.Name == v.Name {
					found = true
					break
				}
			}
			if found {
				klog.Info("applyConfig: volume already exists, ignore", "volume", v.Name)
				continue
			}
			vok[v.Name] = true
			mpp.Volumes = append(mpp.Volumes, v)
		}
	}
	if mp.VolumeMounts != nil {
		if mpp.VolumeMounts == nil {
			mpp.VolumeMounts = []corev1.VolumeMount{}
		}
		for _, vm := range mp.VolumeMounts {
			if !vok[vm.Name] {
				klog.Info("applyConfig: volumeMount not exists in volumes, ignore", "volume", vm.Name)
				continue
			}
			mpp.VolumeMounts = append(mpp.VolumeMounts, vm)
		}
	}
	if mp.VolumeDevices != nil {
		if mpp.VolumeDevices == nil {
			mpp.VolumeDevices = []corev1.VolumeDevice{}
		}
		for _, vm := range mp.VolumeDevices {
			if !vok[vm.Name] {
				klog.Info("applyConfig: volumeDevices not exists in volumes, ignore", "volume", vm.Name)
				continue
			}
			mpp.VolumeDevices = append(mpp.VolumeDevices, vm)
		}
	}
	if mp.Env != nil {
		mpp.Env = mp.Env
	}
	if mp.MountOptions != nil {
		mpp.MountOptions = mp.MountOptions
	}
}

var interVolumesPrefix = []string{
	"rsa-key",
	"init-config",
	"config-",
	"dfs-dir",
	"update-db",
	"cachedir-",
}

func IsInterVolume(name string) bool {
	for _, prefix := range interVolumesPrefix {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func ParseAppInfo(volCtx map[string]string) (*AppInfo, error) {
	// check kubelet access. If not, should turn `podInfoOnMount` on in csiDriver, and fallback to apiServer
	if KubeletPort != "" && HostIp != "" {
		port, err := strconv.Atoi(KubeletPort)
		if err != nil {
			return nil, err
		}
		kc, err := k8sclient.NewKubeletClient(HostIp, port)
		if err != nil {
			return nil, err
		}
		if _, err := kc.GetNodeRunningPods(); err != nil {
			if volCtx == nil || volCtx[PodInfoName] == "" {
				return nil, fmt.Errorf("can not connect to kubelet, please turn `podInfoOnMount` on in csiDriver, and fallback to apiServer")
			}
		}
	}
	if volCtx != nil {
		return &AppInfo{
			Name:      volCtx[PodInfoName],
			Namespace: volCtx[PodInfoNamespace],
		}, nil
	}
	return nil, nil
}

func GenPodAttrWithCfg(setting *DfsSetting, volCtx map[string]string) error {
	var err error
	var attr *PodAttr
	if setting.Attr != nil {
		attr = setting.Attr
	} else {
		attr = &PodAttr{
			Namespace:          Namespace,
			MountPointPath:     MountPointPath,
			HostNetwork:        CSIPod.Spec.HostNetwork,
			HostAliases:        CSIPod.Spec.HostAliases,
			HostPID:            CSIPod.Spec.HostPID,
			HostIPC:            CSIPod.Spec.HostIPC,
			DNSConfig:          CSIPod.Spec.DNSConfig,
			DNSPolicy:          CSIPod.Spec.DNSPolicy,
			ImagePullSecrets:   CSIPod.Spec.ImagePullSecrets,
			Tolerations:        CSIPod.Spec.Tolerations,
			PreemptionPolicy:   CSIPod.Spec.PreemptionPolicy,
			ServiceAccountName: CSIPod.Spec.ServiceAccountName,
			Resources:          getDefaultResource(),
			Labels:             make(map[string]string),
			Annotations:        make(map[string]string),
		}
		attr.Image = DefaultMountImage
		setting.Attr = attr
	}

	if volCtx != nil {
		if v, ok := volCtx[MountPodImageKey]; ok && v != "" {
			attr.Image = v
		}
		if v, ok := volCtx[MountPodServiceAccount]; ok && v != "" {
			attr.ServiceAccountName = v
		}
		cpuLimit := volCtx[MountPodCpuLimitKey]
		memoryLimit := volCtx[MountPodMemLimitKey]
		cpuRequest := volCtx[MountPodCpuRequestKey]
		memoryRequest := volCtx[MountPodMemRequestKey]
		attr.Resources, err = ParsePodResources(cpuLimit, memoryLimit, cpuRequest, memoryRequest)
		if err != nil {
			klog.Error("Parse resource error: %v", err)
			return err
		}
		if v, ok := volCtx[MountPodLabelKey]; ok && v != "" {
			ctxLabel := make(map[string]string)
			if err := ParseYamlOrJson(v, &ctxLabel); err != nil {
				return err
			}
			for k, v := range ctxLabel {
				attr.Labels[k] = v
			}
		}
		if v, ok := volCtx[MountPodAnnotationKey]; ok && v != "" {
			ctxAnno := make(map[string]string)
			if err := ParseYamlOrJson(v, &ctxAnno); err != nil {
				return err
			}
			for k, v := range ctxAnno {
				attr.Annotations[k] = v
			}
		}
	}
	setting.Attr = attr
	// apply config patch
	ApplyConfigPatch(setting)

	return nil
}

func getDefaultResource() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(DefaultMountPodCpuLimit),
			corev1.ResourceMemory: resource.MustParse(DefaultMountPodMemLimit),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(DefaultMountPodCpuRequest),
			corev1.ResourceMemory: resource.MustParse(DefaultMountPodMemRequest),
		},
	}
}

func ParsePodResources(cpuLimit, memoryLimit, cpuRequest, memoryRequest string) (corev1.ResourceRequirements, error) {
	podLimit := map[corev1.ResourceName]resource.Quantity{}
	podRequest := map[corev1.ResourceName]resource.Quantity{}
	// set default value
	podLimit[corev1.ResourceCPU] = resource.MustParse(DefaultMountPodCpuLimit)
	podLimit[corev1.ResourceMemory] = resource.MustParse(DefaultMountPodMemLimit)
	podRequest[corev1.ResourceCPU] = resource.MustParse(DefaultMountPodCpuRequest)
	podRequest[corev1.ResourceMemory] = resource.MustParse(DefaultMountPodMemRequest)
	var err error
	if cpuLimit != "" {
		if podLimit[corev1.ResourceCPU], err = resource.ParseQuantity(cpuLimit); err != nil {
			return corev1.ResourceRequirements{}, err
		}
		q := podLimit[corev1.ResourceCPU]
		if res := q.Cmp(*resource.NewQuantity(0, resource.DecimalSI)); res <= 0 {
			delete(podLimit, corev1.ResourceCPU)
		}
	}
	if memoryLimit != "" {
		if podLimit[corev1.ResourceMemory], err = resource.ParseQuantity(memoryLimit); err != nil {
			return corev1.ResourceRequirements{}, err
		}
		q := podLimit[corev1.ResourceMemory]
		if res := q.Cmp(*resource.NewQuantity(0, resource.DecimalSI)); res <= 0 {
			delete(podLimit, corev1.ResourceMemory)
		}
	}
	if cpuRequest != "" {
		if podRequest[corev1.ResourceCPU], err = resource.ParseQuantity(cpuRequest); err != nil {
			return corev1.ResourceRequirements{}, err
		}
		q := podRequest[corev1.ResourceCPU]
		if res := q.Cmp(*resource.NewQuantity(0, resource.DecimalSI)); res <= 0 {
			delete(podRequest, corev1.ResourceCPU)
		}
	}
	if memoryRequest != "" {
		if podRequest[corev1.ResourceMemory], err = resource.ParseQuantity(memoryRequest); err != nil {
			return corev1.ResourceRequirements{}, err
		}
		q := podRequest[corev1.ResourceMemory]
		if res := q.Cmp(*resource.NewQuantity(0, resource.DecimalSI)); res <= 0 {
			delete(podRequest, corev1.ResourceMemory)
		}
	}
	return corev1.ResourceRequirements{
		Limits:   podLimit,
		Requests: podRequest,
	}, nil
}

func ApplyConfigPatch(setting *DfsSetting) {
	attr := setting.Attr
	// overwrite by mountpod patch
	patch := GenMountPodPatch(setting)
	if patch.Image != "" {
		attr.Image = patch.Image
	}
	if patch.HostNetwork != nil {
		attr.HostNetwork = *patch.HostNetwork
	}
	if patch.HostPID != nil {
		attr.HostPID = *patch.HostPID
	}
	for k, v := range patch.Labels {
		attr.Labels[k] = v
	}
	for k, v := range patch.Annotations {
		attr.Annotations[k] = v
	}
	if patch.Resources != nil {
		attr.Resources = *patch.Resources
	}
	attr.Lifecycle = patch.Lifecycle
	attr.LivenessProbe = patch.LivenessProbe
	attr.ReadinessProbe = patch.ReadinessProbe
	attr.StartupProbe = patch.StartupProbe
	attr.TerminationGracePeriodSeconds = patch.TerminationGracePeriodSeconds
	attr.VolumeDevices = patch.VolumeDevices
	attr.VolumeMounts = patch.VolumeMounts
	attr.Volumes = patch.Volumes
	attr.Env = patch.Env

	// merge or overwrite setting options
	if setting.Options == nil {
		setting.Options = make([]string, 0)
	}
	for _, option := range patch.MountOptions {
		for i, o := range setting.Options {
			if strings.Split(o, "=")[0] == option {
				setting.Options = append(setting.Options[:i], setting.Options[i+1:]...)
			}
		}
		setting.Options = append(setting.Options, option)
	}
}

// GenMountPodPatch generate mount pod patch from dfsSettting
// 1. match pv selector
// 2. parse template value
// 3. return the merged mount pod patch
func GenMountPodPatch(setting *DfsSetting) MountPodPatch {
	patch := &MountPodPatch{
		Labels:      map[string]string{},
		Annotations: map[string]string{},
	}

	// merge each patch
	// for _, mp := range mountPodPatch {
	// 	if mp.IsMatch(setting.PVC) {
	// 		patch.Merge(mp.DeepCopy())
	// 	}
	// }

	patch.Image = patch.MountImage

	data, _ := json.Marshal(patch)
	strData := string(data)
	strData = strings.ReplaceAll(strData, "${MOUNT_POINT}", setting.MountPath)
	strData = strings.ReplaceAll(strData, "${VOLUME_ID}", setting.VolumeId)
	strData = strings.ReplaceAll(strData, "${VOLUME_NAME}", setting.Name)
	strData = strings.ReplaceAll(strData, "${SUB_PATH}", setting.SubPath)
	_ = json.Unmarshal([]byte(strData), patch)
	klog.V(1).Info("volume using patch", "volumeId", setting.VolumeId, "patch", patch)
	return *patch
}

func ParseYamlOrJson(source string, dst interface{}) error {
	if err := yaml.Unmarshal([]byte(source), &dst); err != nil {
		if err := json.Unmarshal([]byte(source), &dst); err != nil {
			return status.Errorf(codes.InvalidArgument,
				"Parse yaml or json error: %v", err)
		}
	}
	return nil
}

func (s *DfsSetting) ParseFormatOptions() ([][]string, error) {
	options := strings.Split(s.FormatOptions, ",")
	parsedFormatOptions := make([][]string, 0, len(options))
	for _, option := range options {
		pair := strings.SplitN(strings.TrimSpace(option), "=", 2)
		if len(pair) == 2 && pair[1] == "" {
			return nil, fmt.Errorf("invalid format options: %s", s.FormatOptions)
		}
		key := strings.TrimSpace(pair[0])
		if key == "" {
			// ignore empty key
			continue
		}
		var value string
		if len(pair) == 1 {
			// single key
			value = ""
		} else {
			value = strings.TrimSpace(pair[1])
		}
		parsedFormatOptions = append(parsedFormatOptions, []string{key, value})
	}
	return parsedFormatOptions, nil
}

func ParseSetting(secrets, volCtx map[string]string, options []string, pv *corev1.PersistentVolume, pvc *corev1.PersistentVolumeClaim) (*DfsSetting, error) {
	dfsSetting := DfsSetting{
		Options: []string{},
	}
	if options != nil {
		dfsSetting.Options = options
	}
	if secrets == nil {
		return &dfsSetting, nil
	}

	secretStr, err := json.Marshal(secrets)
	if err != nil {
		return nil, err
	}
	if err := ParseYamlOrJson(string(secretStr), &dfsSetting); err != nil {
		return nil, err
	}

	if secrets["name"] == "" {
		return nil, status.Errorf(codes.InvalidArgument, "Empty name")
	}
	dfsSetting.Name = secrets["name"]
	dfsSetting.Storage = secrets["storage"]
	dfsSetting.Envs = make(map[string]string)
	dfsSetting.Configs = make(map[string]string)
	dfsSetting.ClientConfPath = DefaultClientConfPath
	dfsSetting.CacheDirs = []string{}
	dfsSetting.CachePVCs = []CachePVC{}
	dfsSetting.PV = pv
	dfsSetting.PVC = pvc

	if secrets["secretkey"] != "" {
		dfsSetting.SecretKey = secrets["secretkey"]
	}
	if secrets["secretkey2"] != "" {
		dfsSetting.SecretKey2 = secrets["secretkey2"]
	}

	if secrets["configs"] != "" {
		configStr := secrets["configs"]
		configs := make(map[string]string)
		klog.V(1).Info("Get configs in secret", "config", configStr)
		if err := ParseYamlOrJson(configStr, &configs); err != nil {
			return nil, err
		}
		dfsSetting.Configs = configs
	}

	if secrets["envs"] != "" {
		envStr := secrets["envs"]
		env := make(map[string]string)
		klog.V(1).Info("Get envs in secret", "env", envStr)
		if err := ParseYamlOrJson(envStr, &env); err != nil {
			return nil, err
		}
		dfsSetting.Envs = env
	}

	if volCtx != nil {
		// subPath
		if volCtx["subPath"] != "" {
			dfsSetting.SubPath = volCtx["subPath"]
		}

		if volCtx[CleanCacheKey] == "true" {
			dfsSetting.CleanCache = true
		}
		delay := volCtx[DeleteDelay]
		if delay != "" {
			if _, err := time.ParseDuration(delay); err != nil {
				return nil, fmt.Errorf("can't parse delay time %s", delay)
			}
			dfsSetting.DeletedDelay = delay
		}

		var hostPaths []string
		if volCtx[MountPodHostPath] != "" {
			for _, v := range strings.Split(volCtx[MountPodHostPath], ",") {
				p := strings.TrimSpace(v)
				if p != "" {
					hostPaths = append(hostPaths, strings.TrimSpace(v))
				}
			}
			dfsSetting.HostPath = hostPaths
		}
	}

	if err := GenPodAttrWithCfg(&dfsSetting, volCtx); err != nil {
		return nil, fmt.Errorf("GenPodAttrWithCfg error: %v", err)
	}
	if err := GenAndValidOptions(&dfsSetting, options); err != nil {
		return nil, fmt.Errorf("genAndValidOptions error: %v", err)
	}
	// TODO generate cache dirs
	//if err := genCacheDirs(&dfsSetting, volCtx); err != nil {
	//	return nil, fmt.Errorf("genCacheDirs error: %v", err)
	//}
	return &dfsSetting, nil
}

func GenAndValidOptions(dfsSetting *DfsSetting, options []string) error {
	mountOptions := []string{}
	for _, option := range options {
		mountOption := strings.TrimSpace(option)
		ops := strings.Split(mountOption, "=")
		if len(ops) > 2 {
			return fmt.Errorf("invalid mount option: %s", mountOption)
		}
		if len(ops) == 2 {
			mountOption = fmt.Sprintf("%s=%s", strings.TrimSpace(ops[0]), strings.TrimSpace(ops[1]))
		}
		if mountOption == "writeback" {
			klog.Info("writeback is not suitable in CSI, please do not use it.", "volumeId", dfsSetting.VolumeId)
		}
		if len(ops) == 2 && ops[0] == "buffer-size" {
			memLimit := dfsSetting.Attr.Resources.Limits[corev1.ResourceMemory]
			memLimitByte := memLimit.Value()

			// buffer-size is in MiB, turn to byte
			bufferSize, err := ParseToBytes(ops[1])
			if err != nil {
				return fmt.Errorf("invalid mount option: %s", mountOption)
			}
			if bufferSize > uint64(memLimitByte) {
				return fmt.Errorf("buffer-size %s MiB is greater than pod memory limit %s", ops[1], memLimit.String())
			}
		}
		mountOptions = append(mountOptions, mountOption)
	}
	dfsSetting.Options = mountOptions
	return nil
}

// ParseToBytes parses a string with a unit suffix (e.g. "1M", "2G") to bytes.
// default unit is M
func ParseToBytes(value string) (uint64, error) {
	if len(value) == 0 {
		return 0, nil
	}
	s := value
	unit := byte('M')
	if c := s[len(s)-1]; c < '0' || c > '9' {
		unit = c
		s = s[:len(s)-1]
	}
	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %s to bytes", value)
	}
	var shift int
	switch unit {
	case 'k', 'K':
		shift = 10
	case 'm', 'M':
		shift = 20
	case 'g', 'G':
		shift = 30
	case 't', 'T':
		shift = 40
	case 'p', 'P':
		shift = 50
	case 'e', 'E':
		shift = 60
	default:
		return 0, fmt.Errorf("cannot parse %s to bytes, invalid unit", value)
	}
	val *= float64(uint64(1) << shift)

	return uint64(val), nil
}

// GenPodAttrWithMountPod generate pod attr with mount pod
// Return the latest pod attributes following the priorities below:
//
// 1. original mount pod
// 2. pvc annotations
// 3. global config
func GenPodAttrWithMountPod(ctx context.Context, client *k8sclient.K8sClient, mountPod *corev1.Pod) (*PodAttr, error) {
	attr := &PodAttr{
		Namespace:            mountPod.Namespace,
		MountPointPath:       MountPointPath,
		DFSConfigPath:        DFSConfigPath,
		DFSMountPriorityName: DFSMountPriorityName,
		HostNetwork:          mountPod.Spec.HostNetwork,
		HostAliases:          mountPod.Spec.HostAliases,
		HostPID:              mountPod.Spec.HostPID,
		HostIPC:              mountPod.Spec.HostIPC,
		DNSConfig:            mountPod.Spec.DNSConfig,
		DNSPolicy:            mountPod.Spec.DNSPolicy,
		ImagePullSecrets:     mountPod.Spec.ImagePullSecrets,
		Tolerations:          mountPod.Spec.Tolerations,
		PreemptionPolicy:     mountPod.Spec.PreemptionPolicy,
		ServiceAccountName:   mountPod.Spec.ServiceAccountName,
		Labels:               make(map[string]string),
		Annotations:          make(map[string]string),
	}
	if mountPod.Spec.Containers != nil && len(mountPod.Spec.Containers) > 0 {
		attr.Image = mountPod.Spec.Containers[0].Image
		attr.Resources = mountPod.Spec.Containers[0].Resources
	}
	for k, v := range mountPod.Labels {
		attr.Labels[k] = v
	}
	for k, v := range mountPod.Annotations {
		attr.Annotations[k] = v
	}
	pvName := mountPod.Annotations[UniqueId]
	pv, err := client.GetPersistentVolume(ctx, pvName)
	if err != nil {
		klog.Error(err, "Get pv error", "pv", pvName)
		return nil, err
	}
	pvc, err := client.GetPersistentVolumeClaim(ctx, pv.Spec.ClaimRef.Name, pv.Spec.ClaimRef.Namespace)
	if err != nil {
		klog.Error(err, "Get pvc error", "namespace", pv.Spec.ClaimRef.Namespace, "name", pv.Spec.ClaimRef.Name)
		return nil, err
	}
	cpuLimit := pvc.Annotations[MountPodCpuLimitKey]
	memoryLimit := pvc.Annotations[MountPodMemLimitKey]
	cpuRequest := pvc.Annotations[MountPodCpuRequestKey]
	memoryRequest := pvc.Annotations[MountPodMemRequestKey]
	resources, err := ParsePodResources(cpuLimit, memoryLimit, cpuRequest, memoryRequest)
	if err != nil {
		return nil, fmt.Errorf("parse pvc resources error: %v", err)
	}
	attr.Resources = resources
	setting := &DfsSetting{
		PV:        pv,
		PVC:       pvc,
		Name:      mountPod.Annotations[DingoFSID],
		VolumeId:  mountPod.Annotations[UniqueId],
		MountPath: filepath.Join(PodMountBase, pvName) + mountPod.Name[len(mountPod.Name)-7:],
		Options:   pv.Spec.MountOptions,
	}
	if v, ok := pv.Spec.CSI.VolumeAttributes["subPath"]; ok && v != "" {
		setting.SubPath = v
	}
	setting.Attr = attr
	// apply config patch
	ApplyConfigPatch(setting)
	return attr, nil
}

func (s *DfsSetting) RepresentFormatOptions(parsedOptions [][]string) []string {
	options := make([]string, 0)
	for _, pair := range parsedOptions {
		option := security.EscapeBashStr(pair[0])
		if pair[1] != "" {
			option = fmt.Sprintf("%s=%s", option, security.EscapeBashStr(pair[1]))
		}
		options = append(options, "--"+option)
	}
	return options
}

func (s *DfsSetting) StripFormatOptions(parsedOptions [][]string, strippedKeys []string) []string {
	options := make([]string, 0)
	strippedMap := make(map[string]bool)
	for _, key := range strippedKeys {
		strippedMap[key] = true
	}

	for _, pair := range parsedOptions {
		option := security.EscapeBashStr(pair[0])
		if pair[1] != "" {
			if strippedMap[pair[0]] {
				option = fmt.Sprintf("%s=${%s}", option, pair[0])
			} else {
				option = fmt.Sprintf("%s=%s", option, security.EscapeBashStr(pair[1]))
			}
		}
		options = append(options, "--"+option)
	}
	return options
}
