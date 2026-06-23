# CSI_IMAGE_NAME ?= dingodatabase/dingofs-csi
CSI_IMAGE_NAME ?= harbor.zetyun.cn/dingofs/dingofs-csi
DRIVER_VERSION ?= v5.1-b1ec0f8
LAST_COMMIT ?= $(shell git rev-parse --short HEAD)
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

IMAGE_TAG := $(CSI_IMAGE_NAME):$(LAST_COMMIT)-$(DRIVER_VERSION)

GO_PROJECT := github.com/jackblack369/dingofs-csi

LD_FLAGS ?=
LD_FLAGS += -extldflags '-static'
LD_FLAGS += -X $(GO_PROJECT)/pkg/util.driverVersion=$(DRIVER_VERSION)
LD_FLAGS += -X $(GO_PROJECT)/pkg/util.gitCommit=$(LAST_COMMIT)
LD_FLAGS += -X $(GO_PROJECT)/pkg/util.buildDate=$(BUILD_DATE)

BUILD_FLAG ?= -mod vendor
BUILD_FLAG += -a

.PHONY: csi docker-build docker-push clean
csi: 
	go mod vendor
	CGO_ENABLED=1 CC=musl-gcc go build $(BUILD_FLAG) -ldflags "$(LD_FLAGS)" -o bin/dingofs-csi-driver ./cmd

docker-build:
	docker build --no-cache --platform linux/amd64 -t $(IMAGE_TAG) .

docker-build-offline:
	docker build --no-cache --platform linux/amd64 -t $(IMAGE_TAG) -f Dockerfile.offline .

docker-push:
	docker push $(IMAGE_TAG)

clean:
	rm -rf bin/