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
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"

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
