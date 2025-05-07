/*
 *
 * Copyright 2022 The Curve Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * 	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 * /
 */

package util

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackblack369/dingofs-csi/pkg/config"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
)

func ValidateCharacter(inputs []string) bool {
	for _, input := range inputs {
		if matched, err := regexp.MatchString("^[A-Za-z0-9=._@:~/-]*$", input); err != nil ||
			!matched {
			return false
		}
	}
	return true
}

func CreatePath(path string) error {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(path, 0777); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	if fi != nil && !fi.IsDir() {
		return fmt.Errorf("Path %s already exists but not dir", path)
	}
	return nil
}

func GetCurrentFuncName() string {
	pc, _, _, _ := runtime.Caller(1)
	return fmt.Sprintf("%s", runtime.FuncForPC(pc).Name())
}

// ByteToGB converts bytes to gigabytes
func ByteToGB(bytes int64) int64 {
	const bytesPerGB = 1024 * 1024 * 1024
	return bytes / bytesPerGB
}

func ParseInt(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}

func ParseBool(s string) bool {
	return s == "true"
}

func KillProcess(pid int) error {
	// Find the process by PID
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process: %v", err)
	}

	// Kill the process
	err = process.Kill()
	if err != nil {
		return fmt.Errorf("failed to kill process: %v", err)
	}
	klog.Infof("killed process [%d] success !", pid)

	return nil
}

func GenHashOfSetting(setting config.DfsSetting) string {
	setting.TargetPath = ""
	setting.VolumeId = ""
	setting.SubPath = ""
	settingStr, _ := json.Marshal(setting)
	h := sha256.New()
	h.Write(settingStr)
	val := hex.EncodeToString(h.Sum(nil))[:63]
	klog.Infof("get dfsSetting hash, hashVal:%s", val)
	return val
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz")

func RandStringRunes(n int) string {
	b := make([]rune, n)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := range b {
		b[i] = letterRunes[r.Intn(len(letterRunes))]
	}
	return string(b)
}

func GetReferenceKey(target string) string {
	h := sha256.New()
	h.Write([]byte(target))
	return fmt.Sprintf("dingofs-%x", h.Sum(nil))[:63]
}

func DoWithTimeout(parent context.Context, timeout time.Duration, f func() error) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	doneCh := make(chan error)
	go func() {
		doneCh <- f()
	}()

	select {
	case <-parent.Done():
		return parent.Err()
	case <-timer.C:
		return errors.New("function timeout")
	case err := <-doneCh:
		return err
	}
}

// GetTimeAfterDelay get time which after delay
func GetTimeAfterDelay(delayStr string) (string, error) {
	delay, err := time.ParseDuration(delayStr)
	if err != nil {
		return "", err
	}
	delayAt := time.Now().Add(delay)
	return delayAt.Format("2006-01-02 15:04:05"), nil
}

func GetTime(str string) (time.Time, error) {
	return time.Parse("2006-01-02 15:04:05", str)
}

func StripPasswd(uri string) string {
	p := strings.Index(uri, "@")
	if p < 0 {
		return uri
	}
	sp := strings.Index(uri, "://")
	cp := strings.Index(uri[sp+3:], ":")
	if cp < 0 || sp+3+cp > p {
		return uri
	}
	return uri[:sp+3+cp] + ":****" + uri[p:]
}

// Each sync.Mutex in the array can be used to lock and unlock a specific resource,
// ensuring that only one goroutine can access the resource at a time.
var PodLocks [1024]sync.Mutex

func GetPodLock(podHashVal string) *sync.Mutex {
	h := fnv.New32a()
	h.Write([]byte(podHashVal))
	// This ensures that the same podHashVal will always map to the same sync.Mutex in the PodLocks array.
	index := h.Sum32() % 1024
	return &PodLocks[index]
}

func QuoteForShell(cmd string) string {
	if strings.Contains(cmd, "(") {
		cmd = strings.ReplaceAll(cmd, "(", "\\(")
	}
	if strings.Contains(cmd, ")") {
		cmd = strings.ReplaceAll(cmd, ")", "\\)")
	}
	return cmd
}

func UmountPath(ctx context.Context, sourcePath string) {
	out, err := exec.CommandContext(ctx, "umount", "-l", sourcePath).CombinedOutput()
	if err != nil &&
		!strings.Contains(string(out), "not mounted") &&
		!strings.Contains(string(out), "mountpoint not found") &&
		!strings.Contains(string(out), "no mount point specified") {
		klog.ErrorS(err, "Could not lazy unmount", "path", sourcePath, "out", string(out))
	}
}

func CheckDynamicPV(name string) (bool, error) {
	return regexp.Match("pvc-\\w{8}(-\\w{4}){3}-\\w{12}", []byte(name))
}

// ContainsPrefix String checks if a string slice contains a string with a given prefix
func ContainsPrefix(slice []string, s string) bool {
	for _, item := range slice {
		if strings.HasPrefix(item, s) {
			return true
		}
	}
	return false
}

func StripReadonlyOption(options []string) []string {
	news := make([]string, 0)
	for _, option := range options {
		if option != "ro" && option != "read-only" {
			news = append(news, option)
		}
	}
	return news
}

func GetDiskUsage(path string) (uint64, uint64, uint64, uint64) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err == nil {
		// in bytes
		blockSize := uint64(stat.Bsize)
		totalSize := blockSize * stat.Blocks
		freeSize := blockSize * stat.Bfree
		totalFiles := stat.Files
		freeFiles := stat.Ffree
		return totalSize, freeSize, totalFiles, freeFiles
	} else {
		klog.ErrorS(err, "GetDiskUsage: syscall.Statfs failed")
		return 1, 1, 1, 1
	}
}

func ParseEndpoint(endpoint string) (string, string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", "", fmt.Errorf("could not parse endpoint: %v", err)
	}

	addr := path.Join(u.Host, filepath.FromSlash(u.Path))

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "tcp":
	case "unix":
		addr = path.Join("/", addr)
		if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
			return "", "", fmt.Errorf("could not remove unix domain socket %q: %v", addr, err)
		}
	default:
		return "", "", fmt.Errorf("unsupported protocol: %s", scheme)
	}

	return scheme, addr, nil
}

// ContainsString checks if a string is in a string slice.
func ContainsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// CheckExpectValue Check if the key has the expected value
func CheckExpectValue(m map[string]string, key string, targetValue string) bool {
	if len(m) == 0 {
		return false
	}
	if v, ok := m[key]; ok {
		return v == targetValue
	}
	return false
}

func GetDfsInfo(vID string) (config.DfsVol, error) {
	splitInfos := strings.Split(vID, ";")
	var dfsVol config.DfsVol

	if len(splitInfos) == 2 {
		/* fsname=<filesystem_name>;path=<dfs_mount_path> */
		fsNameInfo := splitInfos[0]
		mountPathInfo := splitInfos[1]
		fsNameSplit := strings.Split(fsNameInfo, "=")
		mountPathSplit := strings.Split(mountPathInfo, "=")
		if len(fsNameSplit) < 2 || len(mountPathSplit) < 2 {
			return config.DfsVol{}, status.Error(codes.Internal, fmt.Sprintf("Invalid Volume info : [%v]", vID))
		}
		dfsVol.FsName = fsNameSplit[1]
		dfsVol.MountPath = mountPathSplit[1]
		return dfsVol, nil
	}

	return config.DfsVol{}, status.Error(codes.Internal, fmt.Sprintf("Invalid Volume info : [%v]", vID))
}

// CheckDfsType checks if a given path is of type dingofs and
// returns nil if it is a dingofs type, otherwise returns corresponding error.
func CheckDfsType(ctx context.Context, path string) error {
	dfsPaths := getDfsPaths()
	isDfsPath := false
	for _, dfsPath := range dfsPaths {
		if strings.HasPrefix(path, dfsPath) {
			isDfsPath = true
			break
		}
	}

	if !isDfsPath {
		return fmt.Errorf("checkDfsType: the path [%s] is not a valid dingofs path ", path)
	}

	return nil
}

func getDfsPaths() []string {
	var dingofsPaths []string
	gpfsPathCmd := `cat /proc/mounts | grep "dingofs"`
	cmd := exec.Command("bash", "-c", gpfsPathCmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		klog.Errorf("Error in executing command: [%s]", err)
	} else {
		outputPaths := string(output)
		strOutput := strings.Split(outputPaths, "\n")
		for _, out := range strOutput {
			finalOutput := strings.Split(out, " ")
			if len(finalOutput) == config.MountPathLength {
				if finalOutput[1] != "" && finalOutput[2] == "fuse.dingofs" {
					dingofsPaths = append(dingofsPaths, finalOutput[1])
				}
			}
		}
	}
	// print the dingofs paths
	klog.Infof("dingofs mount paths: %v", dingofsPaths)
	return dingofsPaths
}
