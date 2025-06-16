/*
 * Copyright (c) 2025 dingodb.com, Inc. All Rights Reserved
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

package util

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"k8s.io/klog/v2"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultToolExampleConfPath   = "/dingofs/conf/tools.conf"
	defaultClientExampleConfPath = "/dingofs/conf/client.conf"
	toolPath                     = "/dingofs/tools-v2/sbin/dingo"
	clientPath                   = "/dingofs/client/sbin/dingo-fuse"
	cacheDirPrefix               = "/dingofs/client/data/cache/"
	PodMountBase                 = "/dfs"
	MountBase                    = "/var/lib/dfs"
)

type dingofsTool struct {
	ToolParams  map[string]string
	QuotaParams map[string]string
}

type FsInfo struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func NewDingofsTool() *dingofsTool {
	return &dingofsTool{ToolParams: map[string]string{}, QuotaParams: map[string]string{}}
}

func (ct *dingofsTool) CreateFsV1(
	params map[string]string,
	secrets map[string]string,
) error {
	fsName := secrets["name"]
	//if _, ok := fss[fsName]; ok {
	//	klog.Infof("file system: %s has beed already created", fsName)
	//	return nil
	//}

	err := ct.ValidateCommonParams(secrets)
	if err != nil {
		return err
	}

	// check fs exist or not
	fsExisted, err := ct.CheckFsExisted(fsName, ct.ToolParams["mdsaddr"])
	if err != nil {
		return err
	}

	if fsExisted {
		klog.Infof("file system: %s has beed already created", fsName)
		return nil
	}

	klog.Infof("check fs [%s] not existed, begin create fs [%s]", fsName, fsName)
	err = ct.ValidateCreateFsParams(secrets)
	if err != nil {
		return err
	}
	ct.ToolParams["fsname"] = fsName
	// call dingofs create fs to create a fs
	createFsArgs := []string{"create", "fs"}
	for k, v := range ct.ToolParams {
		arg := fmt.Sprintf("--%s=%s", k, v)
		createFsArgs = append(createFsArgs, arg)
	}

	createFsCmd := exec.Command(toolPath, createFsArgs...) //
	output, err := createFsCmd.CombinedOutput()
	klog.Infof("'create fs' command use createFsArgs: %v, output: %s", createFsArgs, output)
	if err != nil {
		return status.Errorf(
			codes.Internal,
			"dingo create fs failed. cmd: %s %v, output: %s, err: %v",
			toolPath,
			createFsArgs,
			output,
			err,
		)
	}

	// verify fs created success or not by "query fs in dingofs by fsname or fsid"
	queryFsArgs := []string{"query", "fs", "--fsname=" + fsName, "--mdsaddr=" + ct.ToolParams["mdsaddr"]}
	queryFsCmd := exec.Command(toolPath, queryFsArgs...)
	output, err = queryFsCmd.CombinedOutput()
	klog.Infof("'query fs' command use queryFsArgs: %v, output: %s", queryFsArgs, output)
	// check output have 'Error' or not
	if err != nil || strings.Contains(string(output), "Error") {
		return status.Errorf(
			codes.Internal,
			"dingo query fs failed. cmd: %s %v, output: %s, err: %v",
			toolPath,
			queryFsArgs,
			output,
			err,
		)
	}

	configQuotaArgs := []string{"config", "fs", "--fsname=" + fsName, "--mdsaddr=" + ct.ToolParams["mdsaddr"]}
	if len(ct.QuotaParams) != 0 {
		for k, v := range ct.QuotaParams {
			arg := fmt.Sprintf("--%s=%s", k, v)
			configQuotaArgs = append(configQuotaArgs, arg)
		}
		klog.Infof("config fs, configQuotaArgs: %v", configQuotaArgs)
		configQuotaCmd := exec.Command(toolPath, configQuotaArgs...)
		outputQuota, errQuota := configQuotaCmd.CombinedOutput()
		if errQuota != nil {
			return status.Errorf(
				codes.Internal,
				"dingo config fs quota failed. cmd: %s %v, output: %s, err: %v",
				toolPath,
				configQuotaArgs,
				outputQuota,
				errQuota,
			)
		}
	}

	//fss[fsName] = fsName
	klog.Infof("create fs success, fsName: %s, quota: %v", fsName, ct.QuotaParams)

	return nil
}

func (ct *dingofsTool) DeleteFs(volumeID string, params map[string]string) error {
	err := ct.ValidateCommonParams(params)
	if err != nil {
		return err
	}
	ct.ToolParams["fsname"] = volumeID // todo change to fsName
	ct.ToolParams["noconfirm"] = "1"
	// call dingo tool delete-fs to create a fs
	deleteFsArgs := []string{"delete-fs"}
	for k, v := range ct.ToolParams {
		arg := fmt.Sprintf("-%s=%s", k, v)
		deleteFsArgs = append(deleteFsArgs, arg)
	}
	deleteFsCmd := exec.Command(toolPath, deleteFsArgs...)
	output, err := deleteFsCmd.CombinedOutput()
	if err != nil {
		return status.Errorf(
			codes.Internal,
			"dingo tool delete-fs failed. cmd:%s %v, output: %s, err: %v",
			toolPath,
			deleteFsArgs,
			output,
			err,
		)
	}
	return nil
}

func (ct *dingofsTool) CheckFsExisted(fsName string, mdsAddr string) (bool, error) {
	listFsArgs := []string{"list", "fs", "--mdsaddr=" + mdsAddr}
	listFsCmd := exec.Command(toolPath, listFsArgs...)
	fsInfos, err := listFsCmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Failed to list filesystems: %v\n", err)
		return false, err
	}

	// Parse the command output
	lines := strings.Split(string(fsInfos), "\n")
	// print lines for debug
	if len(lines) < 3 {
		return false, nil
	}
	var fsInfoList []FsInfo
	fmt.Println("check current fs used by 'dingo list fs' command")
	for _, line := range lines {
		fmt.Println(line)
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "| ID") || line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		id := ParseInt(fields[1])
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

func (ct *dingofsTool) SetVolumeQuota(mdsaddr string, path string, fsname string, capacity string, inodes string) error {
	klog.Infof("set volume quota, mdsaddr: %s, path: %s, fsname: %s, capacity: %s, inodes: %s", mdsaddr, path, fsname, capacity, inodes)
	// call dingofs set quota to set volume quota
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

func (ct *dingofsTool) ValidateCommonParams(params map[string]string) error {
	if mdsAddr, ok := params["mdsAddr"]; ok {
		ct.ToolParams["mdsaddr"] = mdsAddr
	} else {
		return status.Error(codes.InvalidArgument, "mdsAddr is missing")
	}
	return nil
}

func (ct *dingofsTool) ValidateCreateFsParams(params map[string]string) error {
	var fsType, storagetype string
	if value, ok := params["fsType"]; ok {
		fsType = value
		ct.ToolParams["fstype"] = fsType
	}
	if value, ok := params["storagetype"]; ok {
		storagetype = value
		ct.ToolParams["storagetype"] = storagetype
	}
	klog.Infof("validate create fs type, fsType: [%s], storagetype: [%s]", fsType, storagetype)

	if fsType == "s3" || storagetype == "s3" {
		s3Endpoint, ok1 := params["s3Endpoint"]
		s3AccessKey, ok2 := params["s3AccessKey"]
		s3SecretKey, ok3 := params["s3SecretKey"]
		s3Bucket, ok4 := params["s3Bucket"]
		if ok1 && ok2 && ok3 && ok4 {
			ct.ToolParams["s3.endpoint"] = s3Endpoint
			ct.ToolParams["s3.ak"] = s3AccessKey
			ct.ToolParams["s3.sk"] = s3SecretKey
			ct.ToolParams["s3.bucketname"] = s3Bucket
		} else {
			return status.Error(codes.InvalidArgument, "s3Info is incomplete")
		}
	} else if fsType == "rados" || storagetype == "rados" {
		if radosClusterName, ok1 := params["radosClustername"]; ok1 {
			ct.ToolParams["rados.clustername"] = radosClusterName
		}
		radosPoolName, ok2 := params["radosPoolname"]
		radosUserName, ok3 := params["radosUsername"]
		radosUserKey, ok4 := params["radosKey"]
		radosMon, ok5 := params["radosMon"]
		if ok2 && ok3 && ok4 && ok5 {
			ct.ToolParams["rados.poolname"] = radosPoolName
			ct.ToolParams["rados.username"] = radosUserName
			ct.ToolParams["rados.key"] = radosUserKey
			ct.ToolParams["rados.mon"] = radosMon
		} else {
			return status.Error(codes.InvalidArgument, "radosInfo is incomplete")
		}
	} else if fsType == "volume" {
		if backendVolName, ok := params["backendVolName"]; ok {
			ct.ToolParams["volumeName"] = backendVolName
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
			ct.ToolParams["volumeSize"] = backendVolSizeGB
		} else {
			return status.Error(codes.InvalidArgument, "backendVolSize is missing")
		}
	} else {
		return status.Errorf(codes.InvalidArgument, "unsupported fsType %s", fsType)
	}

	if quotaCapacity, ok := params["quotaCapacity"]; ok {
		ct.QuotaParams["capacity"] = quotaCapacity
	}

	if quotaInodes, ok := params["quotaInodes"]; ok {
		ct.QuotaParams["inodes"] = quotaInodes
	}

	return nil
}

type dingofsMounter struct {
	mounterParams map[string]string
}

func (cm *dingofsMounter) GetCacheDir() string {
	return cm.mounterParams["cache_dir"]
}

func NewDingofsMounter() *dingofsMounter {
	return &dingofsMounter{mounterParams: map[string]string{}}
}

func (cm *dingofsMounter) MountFs(
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
	// dingo-fuse -o default_permissions -o allow_other \
	//  -o conf=/etc/dingofs/client.conf -o fsname=testfs \
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

	err = CreatePath(mountPath)
	if err != nil {
		return 0, status.Errorf(
			codes.Internal,
			"Failed to create mount point path %s, err: %v",
			mountPath,
			err,
		)
	}

	klog.V(3).Infof("dingo-fuse mountFsArgs: %s", mountFsArgs)
	mountFsCmd := exec.Command(clientPath, mountFsArgs...)
	output, err := mountFsCmd.CombinedOutput()
	if err != nil {
		return 0, status.Errorf(
			codes.Internal,
			"dingo-fuse mount failed. cmd: %s %v, output: %s, err: %v",
			clientPath,
			mountFsArgs,
			output,
			err,
		)
	}
	// get command process id
	pid := mountFsCmd.Process.Pid
	klog.V(1).Infof("dingo-fuse mount success, pid: %d", pid+2)
	return pid + 2, nil
}

func (cm *dingofsMounter) UmountFs(targetPath string, mountUUID string, cacheDirs string) error {
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
func (cm *dingofsMounter) applyMountFlags(
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

func (cm *dingofsMounter) validateMountFsParams(params map[string]string) error {
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

// GetDfsID get fsId from result of `dingo query fs --fsname test1`
func (ct *dingofsTool) GetDfsID(name string) (string, error) {
	// todo get UUID from dingofs
	return "", nil
}
