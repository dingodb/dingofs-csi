package dingofsdriver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackblack369/dingofs-csi/pkg/config"
	"github.com/jackblack369/dingofs-csi/pkg/util"
	"k8s.io/klog/v2"
	"k8s.io/mount-utils"
)

const (
	fsTypeNone        = "none"
	procMountInfoPath = "/proc/self/mountinfo"
)

// Dfs is the interface of a mounted file system
type DfsInterface interface {
	GetBasePath() string
	GetSetting() *config.DfsSetting
	CreateVol(ctx context.Context, volumeID, subPath string) (string, error)
	BindTarget(ctx context.Context, bindSource, target string) error
}

type dfs struct {
	Provider     *dingofs
	Name         string
	MountPath    string
	MountOptions []string
	Setting      *config.DfsSetting
}

var _ DfsInterface = &dfs{}

func (fs *dfs) GetBasePath() string {
	return fs.MountPath
}

// CreateVol creates the directory needed
func (fs *dfs) CreateVol(ctx context.Context, volumeID, subPath string) (string, error) {
	volPath := filepath.Join(fs.MountPath, subPath)
	klog.Info("checking volPath exists", "volPath", volPath, "fs", fs)
	var exists bool
	if err := util.DoWithTimeout(ctx, config.DefaultCheckTimeout, func() (err error) {
		exists, err = mount.PathExists(volPath)
		return
	}); err != nil {
		return "", fmt.Errorf("could not check volume path %q exists: %v", volPath, err)
	}
	if !exists {
		klog.Info("volume not existed")
		if err := util.DoWithTimeout(ctx, config.DefaultCheckTimeout, func() (err error) {
			return os.MkdirAll(volPath, os.FileMode(0777))
		}); err != nil {
			return "", fmt.Errorf("could not make directory for meta %q: %v", volPath, err)
		}
		var fi os.FileInfo
		if err := util.DoWithTimeout(ctx, config.DefaultCheckTimeout, func() (err error) {
			fi, err = os.Stat(volPath)
			return err
		}); err != nil {
			return "", fmt.Errorf("could not stat directory %s: %q", volPath, err)
		} else if fi.Mode().Perm() != 0777 { // The perm of `volPath` may not be 0777 when the umask applied
			if err := util.DoWithTimeout(ctx, config.DefaultCheckTimeout, func() (err error) {
				return os.Chmod(volPath, os.FileMode(0777))
			}); err != nil {
				return "", fmt.Errorf("could not chmod directory %s: %q", volPath, err)
			}
		}
	}

	return volPath, nil
}

func (fs *dfs) BindTarget(ctx context.Context, bindSource, target string) error {
	mountInfos, err := mount.ParseMountInfo(procMountInfoPath)
	if err != nil {
		return err
	}
	var mountMinor, targetMinor *int
	for _, mi := range mountInfos {
		if mi.MountPoint == fs.MountPath {
			minor := mi.Minor
			mountMinor = &minor
		}
		if mi.MountPoint == target {
			targetMinor = &mi.Minor
		}
	}
	if mountMinor == nil {
		return fmt.Errorf("BindTarget: mountPath %s not mounted", fs.MountPath)
	}
	if targetMinor != nil {
		if *targetMinor == *mountMinor {
			// target already binded mountpath
			klog.V(1).Info("target already bind mounted.", "target", target, "mountPath", fs.MountPath)
			return nil
		}
		// target is bind by other path, umount it
		klog.Info("target bind mount to other path, umount it", "target", target)
		util.UmountPath(ctx, target)
	}
	// bind target to mountpath
	klog.Infof("binding source[%s] at target[%s]", bindSource, target)
	if err := fs.Provider.Mount(bindSource, target, fsTypeNone, []string{"bind"}); err != nil {
		os.Remove(target)
		return err
	}
	return nil
}

func (fs *dfs) GetSetting() *config.DfsSetting {
	return fs.Setting
}
