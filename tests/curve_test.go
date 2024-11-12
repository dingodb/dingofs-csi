package tests

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

const (
	toolPath = "/home/dongwei/script/curve"
)

type FsInfo struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func TestGetClusterFsInfo(t *testing.T) {
	mdsAddr := "172.20.7.232:16700,172.20.7.233:16700,172.20.7.234:16700"
	listFsArgs := []string{"list", "fs", "--mdsaddr=" + mdsAddr}
	// Check if the tool exists
	if _, err := os.Stat(toolPath); os.IsNotExist(err) {
		fmt.Printf("The tool does not exist at path: %s\n", toolPath)
		return
	}

	listFsCmd := exec.Command(toolPath, listFsArgs...)
	fsInfos, err := listFsCmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Failed to list filesystems: %v\n", err)
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
		id := parseInt(fields[1])
		fsInfo := FsInfo{
			ID:     id,
			Name:   fields[3],
			Status: fields[5],
		}
		fsInfoList = append(fsInfoList, fsInfo)
	}

	for _, fsInfo := range fsInfoList {
		fmt.Printf("ID: %d, Name: %s, Status: %s\n", fsInfo.ID, fsInfo.Name, fsInfo.Status)
	}

}

func parseInt(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}
