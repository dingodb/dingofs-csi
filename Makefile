CSI_IMAGE_NAME ?= dingofs/dingofscsi
DRIVER_VERSION ?= v1.0.0
LAST_COMMIT ?= $(shell git rev-parse --short HEAD)
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

IMAGE_TAG := $(CSI_IMAGE_NAME):$(DRIVER_VERSION)

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
	go build $(BUILD_FLAG) -ldflags "$(LD_FLAGS)" -o bin/dingofs-csi-driver ./cmd/main.go

docker-build:
	docker build --no-cache --platform linux/amd64 -t $(IMAGE_TAG) .

docker-push:
	docker push $(IMAGE_TAG)

clean:
	rm -rf bin/