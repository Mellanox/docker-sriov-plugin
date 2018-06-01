ARCH=$(shell uname -m)
GIT_VER=$(shell git rev-list -1 HEAD)
IMAGE=mellanox/sriov-plugin:$(ARCH)
IMAGE_LATEST=mellanox/sriov-plugin
#IMAGE=plugin:$(ARCH)

all:
	echo "Image name is $(IMAGE)"
	docker build . -t $(IMAGE)
ifeq ($(ARCH),x86_64)
	docker build . -t $(IMAGE_LATEST)
endif

push:
	docker push $(IMAGE)
ifeq ($(ARCH),x86_64)
	docker push $(IMAGE_LATEST)
endif

run:
ifeq ($(ARCH),x86_64)
	docker run -v /run/docker/plugins:/run/docker/plugins -v /etc/docker:/etc/docker --net=host --privileged $(IMAGE_LATEST)
else
	docker run -v /run/docker/plugins:/run/docker/plugins -v /etc/docker:/etc/docker --net=host --privileged $(IMAGE)
endif
