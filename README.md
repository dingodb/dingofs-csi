# DingoFS CSI Driver

## Introduction

The DingoFS CSI Driver implements the CSI specification for container orchestrators to manager DingoFS File Systems.

## Prerequisites

- Kubernetes 1.18+

## Build
```shell
make csi
CSI_IMAGE_NAME=<IMAGE_NAME> DRIVER_VERSION=<IMAGE_TAG> make docker-build
CSI_IMAGE_NAME=<IMAGE_NAME> DRIVER_VERSION=<IMAGE_TAG> make docker-push
```

## CSI Interface Implemented

- ControllerServer: CreateVolume, DeleteVolume, ValidateVolumeCapabilities
- NodeServer: NodePublishVolume, NodeUnpublishVolume, NodeGetInfo, NodeGetCapabilities
- IdentityServer: GetPluginInfo, Probe, GetPluginCapabilities
- Provisioner: Responsible for dynamically provisioning and deprovisioning storage resources. 

## How to use

### via kubectl

1. add label to node
    ```bash
    kubectl label node <nodename> dingofs-csi-controller=enabled
    # Add the following label for all node that your pod run on
    kubectl label node <nodename> dingofs-csi-node=enabled
    ```
2. deploy csi driver
    ```bash
    kubectl apply -f deploy/csi-driver.yaml
    kubectl apply -f deploy/csi-rbac.yaml
    kubectl apply -f deploy/csi-controller-deployment.yaml
    kubectl apply -f deploy/csi-node-daemonset.yaml
    ```
   attention: if you want to enable DiskCache, read the related section below
3. create storage class and pvc
   ```bash
   # copy and fill in the blanks in storageclass-default.yaml
   kubectl apply -f storageclass.yaml
   # copy and modify the pvc-default.yaml
   kubectl apply -f pvc.yaml
   ```
4. now you can bind this pvc to a pod

#### DiskCache related

what is DiskCache? A disk based cache used by client to increase the io performance
of client.

If you want to enable it:
1. check out content in csi-node-daemonset-enable-cache.yaml to bind the cache dir on curvefs-csi-node to pod's /curvefs/client/data/cache
2. add "diskCache.diskCacheType=2" or "diskCache.diskCacheType=1" to your mountOptions section of storageclass.yaml, 2 for read and write, 1 for read

Know Issue:

With discache enabled (type=2, write), metadata in metadatasever will be newer than data in s3 storage,
if the csi node pod crash but write cache is not fully uploaded to s3 storage,
you may lose this part of data. Remount will crash, because you only have meta but without data (haven't been flushed to s3).


## Build Status

| DingoFS CSI Driver Version | DingoFS Version | DingoFS CSI Driver Image                          |
|----------------------------|-----------------|---------------------------------------------------|
| v1.0.0                     | v2.3.0-rc0      | dingodatabase/dingofs-csi:v1.0.0 |
| v1.1 | v2.4.0-beta2 | dingodatabase/dingofs-csi:v1.1|
| v2.1 | v2.5.0-beta | dingodatabase/dingofs-csi:v2.1|
