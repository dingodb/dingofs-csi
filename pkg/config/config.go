package config

import (
	"time"

	corev1 "k8s.io/api/core/v1"
)

var (
	DriverName               = "csi.dingofs.com"
	NodeName                 = ""
	Namespace                = ""
	PodName                  = ""
	HostIp                   = ""
	KubeletPort              = ""
	ReconcileTimeout         = 5 * time.Minute
	ReconcilerInterval       = 5
	SecretReconcilerInterval = 1 * time.Hour

	CSIPod = corev1.Pod{}

	MountManager    = false // manage mount pod in controller (only in k8s)
	Immutable       = false // csi driver is running in an immutable environment
	Provisioner     = false // provisioner in controller
	CacheClientConf = false // cache client config files and use directly in mount containers

	DFSConfigPath            = "/var/lib/dingofs/config"
	DFSMountPriorityName     = "system-node-critical"
	DFSMountPreemptionPolicy = ""

	DefaultMountImage = "dingodatabase/dingofs-csi:latest" // mount pod image, override by ENV
	// DefaultMountImage = "harbor.zetyun.cn/dingofs/dingofs-csi:latest" // TODO upgrade image version
	MountPointPath = "/var/lib/dingofs/volume"

	FORMAT_FUSE_ARGS = []string{
		"-f",
		"-o default_permissions",
		"-o allow_other",
		"-o fsname=%s", // fsname
		"-o fstype=%s", // fstype, `s3` or `volume`
		"-o user=curvefs",
		"-o conf=%s", // config path
		"%s",         // mount path
	}
)

const (
	CSINodeLabelKey     = "app"
	CSINodeLabelValue   = "dingofs-csi-node"
	PodTypeKey          = "app.kubernetes.io/name"
	PodTypeValue        = "dingofs-mount"
	JobTypeKey          = "batch.kubernetes.io/name"
	JobTypeValue        = "dingofs-job"
	PodUniqueIdLabelKey = "volume-id"
	PodHashLabelKey     = "dingofs-hash"

	DingoFSID = "dingfs-fsid"
	UniqueId  = "dingofs-uniqueid"

	DeleteDelayTimeKey = "dingofs-delete-delay"
	DeleteDelayAtKey   = "dingofs-delete-at"

	PodMountBase = "/dfs"

	PodInfoName         = "csi.storage.k8s.io/pod.name"
	PodInfoNamespace    = "csi.storage.k8s.io/pod.namespace"
	DefaultCheckTimeout = 2 * time.Second

	MountContainerName = "dfs-mount"

	// default dingo-fuse config key (set env with underscore instead dot in bash)
	MdsAddrKey = "mdsOpt_rpcRetryOpt_addrs"

	// default value
	DefaultMountPodCpuLimit   = "2000m"
	DefaultMountPodMemLimit   = "5Gi"
	DefaultMountPodCpuRequest = "1000m"
	DefaultMountPodMemRequest = "1Gi"

	// config in pv
	MountPodCpuLimitKey    = "dingofs/mount-cpu-limit"
	MountPodMemLimitKey    = "dingofs/mount-memory-limit"
	MountPodCpuRequestKey  = "dingofs/mount-cpu-request"
	MountPodMemRequestKey  = "dingofs/mount-memory-request"
	MountPodLabelKey       = "dingofs/mount-labels"
	MountPodAnnotationKey  = "dingofs/mount-annotations"
	MountPodServiceAccount = "dingofs/mount-service-account"
	MountPodImageKey       = "dingofs/mount-image"
	CleanCacheKey          = "dingofs/clean-cache"
	DeleteDelay            = "dingofs/mount-delete-delay"
	MountPodHostPath       = "dingofs/host-path"

	DfsInsideContainer = "DFS_INSIDE_CONTAINER"
	Finalizer          = "dingofs.com/finalizer"

	CleanCache = "dingofs-clean-cache"
	ROConfPath = "/etc/dingofs"

	DfsDirName          = "dfs-dir"
	UpdateDBDirName     = "updatedb"
	UpdateDBCfgFile     = "/etc/updatedb.conf"
	DfsFuseFdPathName   = "dfs-fuse-fd"
	DfsFuseFsPathInPod  = "/tmp"
	DfsFuseFsPathInHost = "/var/run/dingofs-csi"
	DfsCommEnv          = "DFS_SUPER_COMM"

	DefaultBootstrapPath  = "/scripts"
	DefaultBootstrapShell = "mountpoint.sh"
	DefaultClientConfPath = "/dingofs/conf/client.conf"
	DfsCMDPath            = "/usr/bin/dingo"
	DfsFuseCMDPath        = "/dingofs/client/sbin/dingo-fuse"

	TmpPodMountBase = "/tmp"

	// secret labels
	DingofsSecretLabelKey = "dingofs/secret"

	// webhook
	WebhookName          = "dingofs-admission-webhook"
	True                 = "true"
	False                = "false"
	inject               = ".dingofs.com/inject"
	injectSidecar        = ".sidecar" + inject
	InjectSidecarDone    = "done" + injectSidecar
	InjectSidecarDisable = "disable" + injectSidecar

	// CSI Secret
	ProvisionerSecretName           = "csi.storage.k8s.io/provisioner-secret-name"
	ProvisionerSecretNamespace      = "csi.storage.k8s.io/provisioner-secret-namespace"
	PublishSecretName               = "csi.storage.k8s.io/node-publish-secret-name"
	PublishSecretNamespace          = "csi.storage.k8s.io/node-publish-secret-namespace"
	ControllerExpandSecretName      = "csi.storage.k8s.io/controller-expand-secret-name"
	ControllerExpandSecretNamespace = "csi.storage.k8s.io/controller-expand-secret-namespace"

	ResyncPeriod = 1000 * time.Millisecond
)
