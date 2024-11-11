package pkg_dingofs

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

const (
	toolPath = "/home/dongwei/script/curve"
)

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

	// Marshal fsInfo to JSON
	fsInfoJson, errFormat := json.MarshalIndent(fsInfos, "", "  ")
	if errFormat != nil {
		t.Fatalf("Failed to marshal fsInfo to JSON: %v", errFormat)
	}
	fmt.Println(string(fsInfoJson))

}
