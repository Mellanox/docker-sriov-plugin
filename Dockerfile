FROM golang
RUN apt-get update && apt-get -y install dbus
RUN go get github.com/vishvananda/netlink github.com/Sirupsen/logrus github.com/codegangsta/cli github.com/docker/go-plugins-helpers/network github.com/docker/libnetwork/options github.com/docker/libnetwork/netlabel github.com/Mellanox/sriovnet

#COPY tools/ibdev2netdev /usr/bin/ibdev2netdev
RUN git clone https://github.com/Mellanox/container_scripts.git /tmp/tools

#COPY tmp/tools/ibdev2netdev /usr/bin/ibdev2netdev
COPY . /go/src/github.com/gopher-net/docker-sriov-plugin
WORKDIR /go/src/github.com/gopher-net/docker-sriov-plugin
RUN go install -v
ENTRYPOINT ["docker-sriov-plugin"]
