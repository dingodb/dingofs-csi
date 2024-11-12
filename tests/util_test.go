package tests

import (
	"fmt"
	"os/exec"
	"testing"
)

const (
	fuseCMD = "/home/dongwei/script/curve-fuse"
)

func TestFuseCMD(t *testing.T) {

	/**
	/curvefs/client/sbin/curve-fuse -o default_permissions -o allow_other
	-o fsname=mydfs-1
	--mdsaddr=172.20.7.232:16700,172.20.7.233:16700,172.20.7.234:16700
	-o conf=/curvefs/conf/client.conf.e146d806-c6e6-4e36-bce9-d58be4f4e8a5
	-o fstype=s3
	/dfs/e146d806-c6e6-4e36-bce9-d58be4f4e8a5/pvc-c1b121a6-a698-4b5e-b847-c4c2ea110dee
	*/

	mdsAddr := "172.20.7.232:16700,172.20.7.233:16700,172.20.7.234:16700"
	createFsArgs := []string{"-o fstype=s3", "-o default_permissions", "-o allow_other", "-o fsname=test-quota",
		"--mdsaddr=" + mdsAddr, "-o conf=/home/dongwei/script/client.conf",
		"/home/dongwei/script/test-mp"}

	createFsCmd := exec.Command(fuseCMD, createFsArgs...)
	_, err := createFsCmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Failed to create filesystem: %v\n", err)
	}
	pid := createFsCmd.Process.Pid
	fmt.Printf("PID: %d\n", pid)

}
