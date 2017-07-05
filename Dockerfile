FROM golang
RUN apt-get update && apt-get -y install dbus
RUN go get github.com/vishvananda/netlink github.com/Sirupsen/logrus github.com/codegangsta/cli github.com/docker/go-plugins-helpers/network github.com/docker/libnetwork/options github.com/docker/libnetwork/netlabel

COPY . /go/src/github.com/gopher-net/docker-passthrough-plugin
WORKDIR /go/src/github.com/gopher-net/docker-passthrough-plugin
RUN go install -v
ENTRYPOINT ["docker-passthrough-plugin"]
