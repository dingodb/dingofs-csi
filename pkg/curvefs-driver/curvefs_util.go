/*
Copyright 2022 The Curve Authors

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

package curvefsdriver

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"k8s.io/klog/v2"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/jackblack369/dingofs-csi/pkg/util"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultToolExampleConfPath   = "/curvefs/conf/tools.conf"
	defaultClientExampleConfPath = "/curvefs/conf/client.conf"
	// toolPath                     = "/curvefs/tools/sbin/curvefs_tool"
	toolPath       = "/curvefs/tools-v2/sbin/curve"
	clientPath     = "/curvefs/client/sbin/curve-fuse"
	cacheDirPrefix = "/curvefs/client/data/cache/"
	PodMountBase   = "/dfs"
	MountBase      = "/var/lib/dfs"
)

type curvefsTool struct {
	toolParams  map[string]string
	quotaParams map[string]string
}

type FsInfo struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

var fss = map[string]string{}

func NewCurvefsTool() *curvefsTool {
	return &curvefsTool{toolParams: map[string]string{}, quotaParams: map[string]string{}}
}

func (ct *curvefsTool) CreateFs(
	params map[string]string,
	secrets map[string]string,
) error {
	fsName := secrets["name"]
	//if _, ok := fss[fsName]; ok {
	//	klog.Infof("file system: %s has beed already created", fsName)
	//	return nil
	//}

	err := ct.validateCommonParamsV2(secrets)
	if err != nil {
		return err
	}

	// check fs exist or not
	fsExisted, err := ct.CheckFsExisted(fsName, ct.toolParams["mdsaddr"])
	if err != nil {
		return err
	}
	if fsExisted {
		klog.Infof("file system: %s has beed already created", fsName)
		return nil
	}

	err = ct.validateCreateFsParamsV2(secrets)
	if err != nil {
		return err
	}
	ct.toolParams["fsname"] = fsName
	// call curvefs create fs to create a fs
	createFsArgs := []string{"create", "fs"}
	for k, v := range ct.toolParams {
		arg := fmt.Sprintf("--%s=%s", k, v)
		createFsArgs = append(createFsArgs, arg)
	}

	klog.V(1).Infof("create fs, createFsArgs: %v", createFsArgs)
	createFsCmd := exec.Command(toolPath, createFsArgs...) //
	output, err := createFsCmd.CombinedOutput()
	if err != nil {
		return status.Errorf(
			codes.Internal,
			"curve create fs failed. cmd: %s %v, output: %s, err: %v",
			toolPath,
			createFsArgs,
			output,
			err,
		)
	}

	configQuotaArgs := []string{"config", "fs", "--fsname=" + fsName, "--mdsaddr=" + ct.toolParams["mdsaddr"]}
	if len(ct.quotaParams) != 0 {
		for k, v := range ct.quotaParams {
			arg := fmt.Sprintf("--%s=%s", k, v)
			configQuotaArgs = append(configQuotaArgs, arg)
		}
		klog.V(1).Infof("config fs, configQuotaArgs: %v", configQuotaArgs)
		configQuotaCmd := exec.Command(toolPath, configQuotaArgs...)
		outputQuota, errQuota := configQuotaCmd.CombinedOutput()
		if errQuota != nil {
			return status.Errorf(
				codes.Internal,
				"curve config fs quota failed. cmd: %s %v, output: %s, err: %v",
				toolPath,
				configQuotaArgs,
				outputQuota,
				errQuota,
			)
		}
	}

	//fss[fsName] = fsName
	klog.Infof("create fs success, fsName: %s, quota: %v", fsName, ct.quotaParams)

	return nil
}

func (ct *curvefsTool) DeleteFs(volumeID string, params map[string]string) error {
	err := ct.validateCommonParams(params)
	if err != nil {
		return err
	}
	ct.toolParams["fsname"] = volumeID // todo change to fsName
	ct.toolParams["noconfirm"] = "1"
	// call curvefs_tool delete-fs to create a fs
	deleteFsArgs := []string{"delete-fs"}
	for k, v := range ct.toolParams {
		arg := fmt.Sprintf("-%s=%s", k, v)
		deleteFsArgs = append(deleteFsArgs, arg)
	}
	deleteFsCmd := exec.Command(toolPath, deleteFsArgs...)
	output, err := deleteFsCmd.CombinedOutput()
	if err != nil {
		return status.Errorf(
			codes.Internal,
			"curvefs_tool delete-fs failed. cmd:%s %v, output: %s, err: %v",
			toolPath,
			deleteFsArgs,
			output,
			err,
		)
	}
	return nil
}

func (ct *curvefsTool) CheckFsExisted(fsName string, mdsAddr string) (bool, error) {
	listFsArgs := []string{"list", "fs", "--mdsaddr=" + mdsAddr}
	listFsCmd := exec.Command(toolPath, listFsArgs...)
	fsInfos, err := listFsCmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Failed to list filesystems: %v\n", err)
		return false, err
	}

	// Parse the command output
	lines := strings.Split(string(fsInfos), "\n")
	var fsInfoList []FsInfo
	for _, line := range lines {
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "| ID") || line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		id := util.ParseInt(fields[1])
		fsInfo := FsInfo{
			ID:     id,
			Name:   fields[3],
			Status: fields[5],
		}
		fsInfoList = append(fsInfoList, fsInfo)
	}

	for _, fsInfo := range fsInfoList {
		if fsInfo.Name == fsName {
			fmt.Printf("find ID: %d, Name: %s, Status: %s\n", fsInfo.ID, fsInfo.Name, fsInfo.Status)
			return true, nil
		}
	}
	return false, nil
}

func (ct *curvefsTool) SetVolumeQuota(mdsaddr string, path string, fsname string, capacity string, inodes string) error {
	klog.Infof("set volume quota, mdsaddr: %s, path: %s, fsname: %s, capacity: %s, inodes: %s", mdsaddr, path, fsname, capacity, inodes)
	// call curvefs set quota to set volume quota
	setQuotaArgs := []string{"quota", "set", "--mdsaddr=" + mdsaddr, "--path=" + path, "--fsname=" + fsname, "--capacity=" + capacity}
	if strings.TrimSpace(inodes) != "" {
		setQuotaArgs = append(setQuotaArgs, "--inodes="+inodes)
	}
	setQuotaCmd := exec.Command(toolPath, setQuotaArgs...)
	output, err := setQuotaCmd.CombinedOutput()
	if err != nil {
		return status.Errorf(
			codes.Internal,
			"dingofs config volume quota failed. cmd:%s %v, output: %s, err: %v",
			toolPath,
			setQuotaArgs,
			output,
			err,
		)
	}
	return nil
}

func (ct *curvefsTool) validateCommonParams(params map[string]string) error {
	if mdsAddr, ok := params["mdsAddr"]; ok {
		ct.toolParams["mdsAddr"] = mdsAddr
	} else {
		return status.Error(codes.InvalidArgument, "mdsAddr is missing")
	}
	if confPath, ok := params["toolConfPath"]; ok {
		ct.toolParams["confPath"] = confPath
	} else {
		ct.toolParams["confPath"] = defaultToolExampleConfPath
	}
	return nil
}

func (ct *curvefsTool) validateCommonParamsV2(params map[string]string) error {
	if mdsAddr, ok := params["mdsAddr"]; ok {
		ct.toolParams["mdsaddr"] = mdsAddr
	} else {
		return status.Error(codes.InvalidArgument, "mdsAddr is missing")
	}
	return nil
}

func (ct *curvefsTool) validateCreateFsParams(params map[string]string) error {
	if fsType, ok := params["fsType"]; ok {
		ct.toolParams["fsType"] = fsType
		enableSumInDir, ok := params["enableSumInDir"]
		if ok {
			ct.toolParams["enableSumInDir"] = enableSumInDir
		} else {
			ct.toolParams["enableSumInDir"] = "0"
		}
		if fsType == "s3" {
			s3Endpoint, ok1 := params["s3Endpoint"]
			s3AccessKey, ok2 := params["s3AccessKey"]
			s3SecretKey, ok3 := params["s3SecretKey"]
			s3Bucket, ok4 := params["s3Bucket"]
			if ok1 && ok2 && ok3 && ok4 {
				ct.toolParams["s3_endpoint"] = s3Endpoint
				ct.toolParams["s3_ak"] = s3AccessKey
				ct.toolParams["s3_sk"] = s3SecretKey
				ct.toolParams["s3_bucket_name"] = s3Bucket
			} else {
				return status.Error(codes.InvalidArgument, "s3Info is incomplete")
			}
		} else if fsType == "volume" {
			if backendVolName, ok := params["backendVolName"]; ok {
				ct.toolParams["volumeName"] = backendVolName
			} else {
				return status.Error(codes.InvalidArgument, "backendVolName is missing")
			}
			if backendVolSizeGB, ok := params["backendVolSizeGB"]; ok {
				backendVolSizeGBInt, err := strconv.ParseInt(backendVolSizeGB, 0, 64)
				if err != nil {
					return status.Error(codes.InvalidArgument, "backendVolSize is not integer")
				}
				if backendVolSizeGBInt < 10 {
					return status.Error(codes.InvalidArgument, "backendVolSize must larger than 10GB")
				}
				ct.toolParams["volumeSize"] = backendVolSizeGB
			} else {
				return status.Error(codes.InvalidArgument, "backendVolSize is missing")
			}
		} else {
			return status.Errorf(codes.InvalidArgument, "unsupported fsType %s", fsType)
		}
	} else {
		return status.Error(codes.InvalidArgument, "fsType is missing")
	}
	return nil
}

func (ct *curvefsTool) validateCreateFsParamsV2(params map[string]string) error {
	if fsType, ok := params["fsType"]; ok {
		ct.toolParams["fstype"] = fsType

		if fsType == "s3" {
			s3Endpoint, ok1 := params["s3Endpoint"]
			s3AccessKey, ok2 := params["s3AccessKey"]
			s3SecretKey, ok3 := params["s3SecretKey"]
			s3Bucket, ok4 := params["s3Bucket"]
			if ok1 && ok2 && ok3 && ok4 {
				ct.toolParams["s3.endpoint"] = s3Endpoint
				ct.toolParams["s3.ak"] = s3AccessKey
				ct.toolParams["s3.sk"] = s3SecretKey
				ct.toolParams["s3.bucketname"] = s3Bucket
			} else {
				return status.Error(codes.InvalidArgument, "s3Info is incomplete")
			}
		} else if fsType == "volume" {
			if backendVolName, ok := params["backendVolName"]; ok {
				ct.toolParams["volumeName"] = backendVolName
			} else {
				return status.Error(codes.InvalidArgument, "backendVolName is missing")
			}
			if backendVolSizeGB, ok := params["backendVolSizeGB"]; ok {
				backendVolSizeGBInt, err := strconv.ParseInt(backendVolSizeGB, 0, 64)
				if err != nil {
					return status.Error(codes.InvalidArgument, "backendVolSize is not integer")
				}
				if backendVolSizeGBInt < 10 {
					return status.Error(codes.InvalidArgument, "backendVolSize must larger than 10GB")
				}
				ct.toolParams["volumeSize"] = backendVolSizeGB
			} else {
				return status.Error(codes.InvalidArgument, "backendVolSize is missing")
			}
		} else {
			return status.Errorf(codes.InvalidArgument, "unsupported fsType %s", fsType)
		}
	} else {
		return status.Error(codes.InvalidArgument, "fsType is missing")
	}

	if quotaCapacity, ok := params["quotaCapacity"]; ok {
		ct.quotaParams["capacity"] = quotaCapacity
	}

	if quotaInodes, ok := params["quotaInodes"]; ok {
		ct.quotaParams["inodes"] = quotaInodes
	}

	return nil
}

type curvefsMounter struct {
	mounterParams map[string]string
}

func NewCurvefsMounter() *curvefsMounter {
	return &curvefsMounter{mounterParams: map[string]string{}}
}

func (cm *curvefsMounter) MountFs(
	mountPath string,
	params map[string]string,
	mountOption *csi.VolumeCapability_MountVolume,
	mountUUID string,
	secrets map[string]string,
) (int, error) {
	fsname := secrets["name"]
	klog.V(1).Infof("mount fs, fsname: %s, \n mountPath: %s, \n params: %v, \n mountOption: %v, \n mountUUID: %s", fsname, mountPath, params, mountOption, mountUUID)
	err := cm.validateMountFsParams(secrets)
	if err != nil {
		return 0, err
	}
	// mount options from storage class
	// copy and create new conf file with mount options override
	if mountOption != nil {
		confPath, err := cm.applyMountFlags(
			cm.mounterParams["conf"],
			mountOption.MountFlags,
			mountUUID,
		)
		if err != nil {
			return 0, err
		}
		cm.mounterParams["conf"] = confPath
	}

	cm.mounterParams["fsname"] = fsname
	// curve-fuse -o default_permissions -o allow_other \
	//  -o conf=/etc/curvefs/client.conf -o fsname=testfs \
	//  -o fstype=s3  --mdsAddr=1.1.1.1 <mountpoint>
	var mountFsArgs []string
	doubleDashArgs := map[string]string{"mdsaddr": ""}
	extraPara := []string{"default_permissions", "allow_other"}
	for _, para := range extraPara {
		mountFsArgs = append(mountFsArgs, "-o")
		mountFsArgs = append(mountFsArgs, para)
	}

	for k, v := range cm.mounterParams {
		// exclude cache_dir from mount options
		if k == "cache_dir" {
			continue
		}
		if _, ok := doubleDashArgs[k]; ok {
			arg := fmt.Sprintf("--%s=%s", k, v)
			mountFsArgs = append(mountFsArgs, arg)
		} else {
			mountFsArgs = append(mountFsArgs, "-o")
			arg := fmt.Sprintf("%s=%s", k, v)
			mountFsArgs = append(mountFsArgs, arg)
		}
	}

	mountFsArgs = append(mountFsArgs, mountPath)

	err = util.CreatePath(mountPath)
	if err != nil {
		return 0, status.Errorf(
			codes.Internal,
			"Failed to create mount point path %s, err: %v",
			mountPath,
			err,
		)
	}

	klog.V(3).Infof("curve-fuse mountFsArgs: %s", mountFsArgs)
	mountFsCmd := exec.Command(clientPath, mountFsArgs...)
	output, err := mountFsCmd.CombinedOutput()
	if err != nil {
		return 0, status.Errorf(
			codes.Internal,
			"curve-fuse mount failed. cmd: %s %v, output: %s, err: %v",
			clientPath,
			mountFsArgs,
			output,
			err,
		)
	}
	// get command process id
	pid := mountFsCmd.Process.Pid
	klog.V(1).Infof("curve-fuse mount success, pid: %d", pid+2)
	return pid + 2, nil
}

func (cm *curvefsMounter) UmountFs(targetPath string, mountUUID string, cacheDirs string) error {
	// umount TargetCmd volume /var/lib/kubelet/pods/15c066c3-3399-42c6-be63-74c95aa97eba/volumes/kubernetes.io~csi/pvc-c1b121a6-a698-4b5e-b847-c4c2ea110dee/mount
	umountTargetCmd := exec.Command("umount", targetPath)
	output, err := umountTargetCmd.CombinedOutput()
	if err != nil {
		return status.Errorf(
			codes.Internal,
			"umount %s failed. output: %s, err: %v",
			targetPath,
			output,
			err,
		)
	}

	// umount sourcePath /dfs/fe5d0340-b5fa-491b-b826-1129c12de962
	mountDir := PodMountBase + "/" + mountUUID
	umountFsCmd := exec.Command("umount", mountDir)
	output, err = umountFsCmd.CombinedOutput()
	if err != nil {
		return status.Errorf(
			codes.Internal,
			"umount %s failed. output: %s, err: %v",
			mountDir,
			output,
			err,
		)
	}
	// do cleanup, config file and cache dir
	if mountUUID != "" {
		confPath := defaultClientExampleConfPath + "." + mountUUID
		go os.Remove(confPath)

		// cacheDir := cacheDirPrefix + mountUUID // TODO remove all cache dir by specify mountUUID
		// go os.RemoveAll(cacheDir)
		for _, path := range strings.Split(cacheDirs, ";") {
			klog.Infof("remove cache dir: %s", path)
			go os.RemoveAll(path)
		}

		go os.RemoveAll(mountDir)
	}
	return nil
}

// update the configuration file with the mount flags
func (cm *curvefsMounter) applyMountFlags(
	origConfPath string,
	mountFlags []string,
	mountUUID string,
) (string, error) {
	confPath := defaultClientExampleConfPath + "." + mountUUID

	// Step 1: Copy the original configuration file to a new file
	data, err := os.ReadFile(origConfPath)
	if err != nil {
		return "", status.Errorf(
			codes.Internal,
			"applyMountFlag: failed to read conf %s, %v",
			origConfPath,
			err,
		)
	}
	err = os.WriteFile(confPath, data, 0644)
	if err != nil {
		return "", status.Errorf(
			codes.Internal,
			"applyMountFlag: failed to write new conf %s, %v",
			confPath,
			err,
		)
	}

	// Step 2: Read the new configuration file
	data, err = os.ReadFile(confPath)
	if err != nil {
		return "", status.Errorf(
			codes.Internal,
			"applyMountFlag: failed to read new conf %s, %v",
			confPath,
			err,
		)
	}

	// Step 3: Iterate over the mountFlags items
	lines := strings.Split(string(data), "\n")
	configMap := make(map[string]string)
	for _, line := range lines {
		if strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		configMap[parts[0]] = parts[1]
	}

	cacheEnabled := false
	for _, flag := range mountFlags {
		parts := strings.SplitN(flag, "=", 2)
		if len(parts) == 2 {
			configMap[parts[0]] = parts[1]
			if parts[0] == "diskCache.diskCacheType" && (parts[1] == "2" || parts[1] == "1") {
				cacheEnabled = true
			}
		}
	}

	// Step 4: Write the updated configuration back to the new file
	var newData strings.Builder
	for _, line := range lines {
		if strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			newData.WriteString(line + "\n")
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if newValue, exists := configMap[parts[0]]; exists {
			if parts[0] == "disk_cache.cache_dir" {
				cacheDirs := strings.Split(newValue, ";")
				cacheDirsWithCapacity := make([]string, len(cacheDirs))
				cacheDirsPaths := make([]string, len(cacheDirs))
				for i, cacheDir := range cacheDirs {
					cacheDirParts := strings.SplitN(cacheDir, ":", 2)
					if len(cacheDirParts) == 2 {
						cacheDirsWithCapacity[i] = fmt.Sprintf("%s/%s:%s", cacheDirParts[0], mountUUID, cacheDirParts[1])
					} else {
						cacheDirsWithCapacity[i] = fmt.Sprintf("%s/%s", cacheDirParts[0], mountUUID)
					}
					cacheDirsPaths[i] = fmt.Sprintf("%s/%s", cacheDirParts[0], mountUUID)
				}
				newValue = strings.Join(cacheDirsWithCapacity, ";")
				// buffer the cache dir for later use
				cm.mounterParams["cache_dir"] = strings.Join(cacheDirsPaths, ";")

			}
			newData.WriteString(fmt.Sprintf("%s=%s\n", parts[0], newValue))
			delete(configMap, parts[0])
		} else {
			newData.WriteString(line + "\n")
		}
	}

	// Write the remaining new configuration items
	for key, value := range configMap {
		newData.WriteString(fmt.Sprintf("%s=%s\n", key, value))
	}

	err = os.WriteFile(confPath, []byte(newData.String()), 0644)
	if err != nil {
		return "", status.Errorf(
			codes.Internal,
			"applyMountFlag: failed to write updated conf %s, %v",
			confPath,
			err,
		)
	}

	if cacheEnabled {
		for _, cacheDir := range strings.Split(cm.mounterParams["cache_dir"], ";") {
			if err := os.MkdirAll(cacheDir, 0777); err != nil {
				return "", err
			}
		}
		//cacheDir := cacheDirPrefix + mountUUID
		//if err := os.MkdirAll(cacheDir, 0777); err != nil {
		//	return "", err
		//}
	}

	return confPath, nil
}

func (cm *curvefsMounter) validateMountFsParams(params map[string]string) error {
	if mdsAddr, ok := params["mdsAddr"]; ok {
		cm.mounterParams["mdsaddr"] = mdsAddr
	} else {
		return status.Error(codes.InvalidArgument, "mdsAddr is missing")
	}
	if confPath, ok := params["clientConfPath"]; ok {
		cm.mounterParams["conf"] = confPath
	} else {
		cm.mounterParams["conf"] = defaultClientExampleConfPath
	}
	if fsType, ok := params["fsType"]; ok {
		cm.mounterParams["fstype"] = fsType
	} else {
		return status.Error(codes.InvalidArgument, "fsType is missing")
	}
	return nil
}
