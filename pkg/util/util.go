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
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackblack369/dingofs-csi/pkg/config"

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
	klog.Infof("get jfsSetting hash, hashVal:%s", val)
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

func EscapeBashStr(s string) string {
	if !containsOne(s, []rune{'$', '`', '&', ';', '>', '|', '(', ')'}) {
		return s
	}
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return fmt.Sprintf(`$'%s'`, s)
}

func containsOne(target string, chars []rune) bool {
	charMap := make(map[rune]bool, len(chars))
	for _, c := range chars {
		charMap[c] = true
	}
	for _, s := range target {
		if charMap[s] {
			return true
		}
	}
	return false
}

func UmountPath(ctx context.Context, sourcePath string) {
	out, err := exec.CommandContext(ctx, "umount", "-l", sourcePath).CombinedOutput()
	if err != nil &&
		!strings.Contains(string(out), "not mounted") &&
		!strings.Contains(string(out), "mountpoint not found") &&
		!strings.Contains(string(out), "no mount point specified") {
		klog.Error(err, "Could not lazy unmount", "path", sourcePath, "out", string(out))
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
		klog.Error(err, "GetDiskUsage: syscall.Statfs failed")
		return 1, 1, 1, 1
	}
}
