package dingofs

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	config "github.com/jackblack369/dingofs-csi/pkg/config"
	"github.com/jackblack369/dingofs-csi/pkg/k8sclient"
	podmount "github.com/jackblack369/dingofs-csi/pkg/mount"
	"github.com/jackblack369/dingofs-csi/pkg/util"

	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	k8sexec "k8s.io/utils/exec"
	"k8s.io/utils/mount"
)

// ProviderInterface of dingofs provider
type ProviderInterface interface {
	mount.Interface
	DfsMount(ctx context.Context, volumeID string, target string, secrets, volCtx map[string]string, options []string) (DfsInterface, error)
	// DfsCreateVol(ctx context.Context, volumeID string, subPath string, secrets, volCtx map[string]string) error
	// DfsDeleteVol(ctx context.Context, volumeID string, target string, secrets, volCtx map[string]string, options []string) error
	// DfsUnmount(ctx context.Context, volumeID, mountPath string) error
	// DfsCleanupMountPoint(ctx context.Context, mountPath string) error
	GetDfsVolUUID(ctx context.Context, dfsSetting *config.DfsSetting) (string, error)
	// SetQuota(ctx context.Context, secrets map[string]string, dfsSetting *config.DfsSetting, quotaPath string, capacity int64) error
	// Settings(ctx context.Context, volumeID string, secrets, volCtx map[string]string, options []string) (*config.DfsSetting, error)
	// GetSubPath(ctx context.Context, volumeID string) (string, error)
	// CreateTarget(ctx context.Context, target string) error
	// AuthFs(ctx context.Context, secrets map[string]string, dfsSetting *config.DfsSetting, force bool) (string, error)
	// Status(ctx context.Context, metaUrl string) error
}

type dingofs struct {
	sync.Mutex
	mount.SafeFormatAndMount
	*k8sclient.K8sClient

	podMount     podmount.MntInterface
	UUIDMaps     map[string]string
	CacheDirMaps map[string][]string
}

// NewDfsProvider creates a provider for DingoFS file system
func NewDfsProvider(mounter *mount.SafeFormatAndMount, k8sClient *k8sclient.K8sClient) ProviderInterface {
	if mounter == nil {
		mounter = &mount.SafeFormatAndMount{
			Interface: mount.New(""),
			Exec:      k8sexec.New(),
		}
	}
	podMnt := podmount.NewPodMount(k8sClient, *mounter)

	uuidMaps := make(map[string]string)
	cacheDirMaps := make(map[string][]string)
	return &dingofs{
		Mutex:              sync.Mutex{},
		SafeFormatAndMount: *mounter,
		K8sClient:          k8sClient,
		podMount:           podMnt,
		UUIDMaps:           uuidMaps,
		CacheDirMaps:       cacheDirMaps,
	}
}

func (d *dingofs) DfsMount(ctx context.Context, volumeID string, target string, secrets, volCtx map[string]string, options []string) (DfsInterface, error) {
	if err := d.validTarget(target); err != nil {
		return nil, err
	}
	// genJfsSettings get jfs settings and unique id, which will init dingofs fs by ceFormat
	dfsSetting, err := d.genDfsSettings(ctx, volumeID, target, secrets, volCtx, options)
	if err != nil {
		return nil, err
	}
	appInfo, err := config.ParseAppInfo(volCtx)
	if err != nil {
		return nil, err
	}
	mountPath, err := d.MountFs(ctx, appInfo, dfsSetting)
	if err != nil {
		return nil, err
	}

	return &dfs{
		Provider:  d,
		Name:      secrets["name"],
		MountPath: mountPath,
		Options:   options,
		Setting:   dfsSetting,
	}, nil
}

// MountFs mounts DingoFS with idempotency
func (d *dingofs) MountFs(ctx context.Context, appInfo *config.AppInfo, dfsSetting *config.DfsSetting) (string, error) {
	var mnt podmount.MntInterface

	dfsSetting.MountPath = filepath.Join(config.PodMountBase, dfsSetting.UniqueId) // e.g. /jfs/pvc-7175fc74-d52d-46bc-94b3-ad9296b726cd-alypal
	mnt = d.podMount

	err := mnt.DMount(ctx, appInfo, dfsSetting)
	if err != nil {
		return "", err
	}
	klog.Info("mounting with options, source:[%s], mountPath:[%s], options:[%s]", util.StripPasswd(dfsSetting.Source), dfsSetting.MountPath, dfsSetting.Options)
	return dfsSetting.MountPath, nil
}

func (d *dingofs) validTarget(target string) error {
	var msg string
	if strings.Contains(target, "../") || strings.Contains(target, "/..") || strings.Contains(target, "..") {
		msg = msg + fmt.Sprintf("Path %s has illegal access.", target)
		return errors.New(msg)
	}
	if strings.Contains(target, "./") || strings.Contains(target, "/.") {
		msg = msg + fmt.Sprintf("Path %s has illegal access.", target)
		return errors.New(msg)
	}

	kubeletDir := "/var/lib/kubelet"
	for _, v := range config.CSIPod.Spec.Volumes {
		if v.Name == "kubelet-dir" {
			kubeletDir = v.HostPath.Path
			break
		}
	}
	dirs := strings.Split(target, "/pods/")
	if len(dirs) == 0 {
		return fmt.Errorf("can't parse kubelet rootdir from target %s", target)
	}
	if kubeletDir != dirs[0] {
		return fmt.Errorf("target kubelet rootdir %s is not equal csi mounted kubelet root-dir %s", dirs[0], kubeletDir)
	}
	return nil
}

// genJfsSettings get jfs settings and unique id
func (d *dingofs) genDfsSettings(ctx context.Context, volumeID string, target string, secrets, volCtx map[string]string, options []string) (*config.DfsSetting, error) {
	// get settings
	dfsSetting, err := d.Settings(ctx, volumeID, secrets, volCtx, options)
	if err != nil {
		return nil, err
	}
	dfsSetting.TargetPath = target
	// get unique id, uniqueId is not uuid
	uniqueId, err := d.getUniqueId(ctx, volumeID) // e.g. pvc-7175fc74-d52d-46bc-94b3-ad9296b726cd
	if err != nil {
		klog.Error(err, "Get volume name by volume id error", "volumeID", volumeID)
		return nil, err
	}
	klog.V(1).Info("Get uniqueId of volume", "volumeId", volumeID, "uniqueId", uniqueId)
	dfsSetting.UniqueId = uniqueId
	dfsSetting.SecretName = fmt.Sprintf("dingofs-%s-secret", dfsSetting.UniqueId)
	if dfsSetting.CleanCache {
		uuid := dfsSetting.Name
		if uuid, err = d.GetDfsVolUUID(ctx, dfsSetting); err != nil {
			return nil, err
		}
		dfsSetting.FSID = uuid

		klog.V(1).Info("Get uuid of volume", "volumeId", volumeID, "uuid", uuid)
	}
	return dfsSetting, nil
}

// Settings get all dfs settings and generate format/auth command
func (d *dingofs) Settings(ctx context.Context, volumeID string, secrets, volCtx map[string]string, options []string) (*config.DfsSetting, error) {
	pv, pvc, err := util.GetPVWithVolumeHandleOrAppInfo(ctx, d.K8sClient, volumeID, volCtx)
	if err != nil {
		klog.Error(err, "Get PV with volumeID error", "volumeId", volumeID)
	}
	// overwrite volCtx with pvc annotations
	if pvc != nil {
		if volCtx == nil {
			volCtx = make(map[string]string)
		}
		for k, v := range pvc.Annotations {
			if !strings.HasPrefix(k, "dingofs") {
				continue
			}
			volCtx[k] = v
		}
	}

	dfsSetting, err := ParseSetting(secrets, volCtx, options, pv, pvc)
	if err != nil {
		klog.Error(err, "Parse config error", "secret", secrets["name"])
		return nil, err
	}
	dfsSetting.VolumeId = volumeID

	return dfsSetting, nil
}

// getUniqueId: get UniqueId from volumeId (volumeHandle of PV)
// When STORAGE_CLASS_SHARE_MOUNT env is set:
//
//	in dynamic provision, UniqueId set as SC name
//	in static provision, UniqueId set as volumeId
//
// When STORAGE_CLASS_SHARE_MOUNT env not set:
//
//	UniqueId set as volumeId
func (d *dingofs) getUniqueId(ctx context.Context, volumeId string) (string, error) {
	// TODO share mount pod
	//if config.StorageClassShareMount && !config.ByProcess {
	//	pv, err := d.K8sClient.GetPersistentVolume(ctx, volumeId)
	//	// In static provision, volumeId may not be PV name, it is expected that PV cannot be found by volumeId
	//	if err != nil && !k8serrors.IsNotFound(err) {
	//		return "", err
	//	}
	//	// In dynamic provision, PV.spec.StorageClassName is which SC(StorageClass) it belongs to.
	//	if err == nil && pv.Spec.StorageClassName != "" {
	//		return pv.Spec.StorageClassName, nil
	//	}
	//}
	return volumeId, nil
}

func ParseSetting(secrets, volCtx map[string]string, options []string, pv *corev1.PersistentVolume, pvc *corev1.PersistentVolumeClaim) (*config.DfsSetting, error) {
	dfsSetting := config.DfsSetting{
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
	if err := util.ParseYamlOrJson(string(secretStr), &dfsSetting); err != nil {
		return nil, err
	}

	if secrets["name"] == "" {
		return nil, status.Errorf(codes.InvalidArgument, "Empty name")
	}
	dfsSetting.Name = secrets["name"]
	dfsSetting.Storage = secrets["storage"]
	dfsSetting.Envs = make(map[string]string)
	dfsSetting.Configs = make(map[string]string)
	dfsSetting.ClientConfPath = config.DefaultClientConfPath
	dfsSetting.CacheDirs = []string{}
	dfsSetting.CachePVCs = []config.CachePVC{}
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
		if err := util.ParseYamlOrJson(configStr, &configs); err != nil {
			return nil, err
		}
		dfsSetting.Configs = configs
	}

	if secrets["envs"] != "" {
		envStr := secrets["envs"]
		env := make(map[string]string)
		klog.V(1).Info("Get envs in secret", "env", envStr)
		if err := util.ParseYamlOrJson(envStr, &env); err != nil {
			return nil, err
		}
		dfsSetting.Envs = env
	}

	if volCtx != nil {
		// subPath
		if volCtx["subPath"] != "" {
			dfsSetting.SubPath = volCtx["subPath"]
		}

		if volCtx[config.CleanCacheKey] == "true" {
			dfsSetting.CleanCache = true
		}
		delay := volCtx[config.DeleteDelay]
		if delay != "" {
			if _, err := time.ParseDuration(delay); err != nil {
				return nil, fmt.Errorf("can't parse delay time %s", delay)
			}
			dfsSetting.DeletedDelay = delay
		}

		var hostPaths []string
		if volCtx[config.MountPodHostPath] != "" {
			for _, v := range strings.Split(volCtx[config.MountPodHostPath], ",") {
				p := strings.TrimSpace(v)
				if p != "" {
					hostPaths = append(hostPaths, strings.TrimSpace(v))
				}
			}
			dfsSetting.HostPath = hostPaths
		}
	}

	if err := podmount.GenPodAttrWithCfg(&dfsSetting, volCtx); err != nil {
		return nil, fmt.Errorf("GenPodAttrWithCfg error: %v", err)
	}
	if err := podmount.GenAndValidOptions(&dfsSetting, options); err != nil {
		return nil, fmt.Errorf("genAndValidOptions error: %v", err)
	}
	// TODO generate cache dirs
	//if err := genCacheDirs(&dfsSetting, volCtx); err != nil {
	//	return nil, fmt.Errorf("genCacheDirs error: %v", err)
	//}
	return &dfsSetting, nil
}

// GetDfsVolUUID get UUID from result of `dingofs status <volumeName>`
func (d *dingofs) GetDfsVolUUID(ctx context.Context, dfsSetting *config.DfsSetting) (string, error) {
	cmdCtx, cmdCancel := context.WithTimeout(ctx, 8*config.DefaultCheckTimeout)
	defer cmdCancel()
	statusCmd := d.Exec.CommandContext(cmdCtx, config.CliPath, "status", dfsSetting.Source)
	envs := syscall.Environ()
	for key, val := range dfsSetting.Envs {
		envs = append(envs, fmt.Sprintf("%s=%s", util.EscapeBashStr(key), util.EscapeBashStr(val)))
	}
	statusCmd.SetEnv(envs)
	stdout, err := statusCmd.CombinedOutput()
	if err != nil {
		re := string(stdout)
		if strings.Contains(re, "database is not formatted") {
			klog.V(1).Info("dingofs not formatted.", "name", dfsSetting.Source)
			return "", nil
		}
		klog.Error(err, "dingofs status error", "output", re)
		if cmdCtx.Err() == context.DeadlineExceeded {
			re = fmt.Sprintf("dingofs status %s timed out", 8*config.DefaultCheckTimeout)
			return "", errors.New(re)
		}
		return "", errors.Wrap(err, re)
	}

	matchExp := regexp.MustCompile(`"UUID": "(.*)"`)
	idStr := matchExp.FindString(string(stdout))
	idStrs := strings.Split(idStr, "\"")
	if len(idStrs) < 4 {
		return "", fmt.Errorf("get uuid of %s error", dfsSetting.Source)
	}

	return idStrs[3], nil
}