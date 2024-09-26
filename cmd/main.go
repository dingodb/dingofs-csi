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

package main

import (
	"flag"
	"fmt"
	curvefsdriver "github.com/jackblack369/dingofs-csi/pkg/curvefs-driver"
	"github.com/jackblack369/dingofs-csi/pkg/util"
	"k8s.io/klog/v2"
	"os"
)

var (
	endpoint = flag.String("endpoint", "unix://tmp/csi.sock", "CSI Endpoint")
	version  = flag.Bool("version", false, "Print the version info")
	nodeID   = flag.String("nodeid", "", "NodeID")
)

func init() {
	klog.InitFlags(nil)
	flag.Parse()
}

func main() {
	if *version {
		info, err := util.GetVersionJSON()
		if err != nil {
			klog.Fatalln(err)
		}
		fmt.Println(info)
		os.Exit(0)
	}

	if *nodeID == "" {
		klog.Fatalln("nodeID must be provided")
	}

	driver, err := curvefsdriver.NewDriver(*endpoint, *nodeID)
	if err != nil {
		klog.Fatalln(err)
	}
	driver.Run()
}
