FROM golang:1.10.1 as build
WORKDIR /go/src/docker-sriov-plugin

RUN go get github.com/vishvananda/netlink github.com/codegangsta/cli github.com/docker/go-plugins-helpers/network github.com/docker/libnetwork/options github.com/docker/libnetwork/netlabel github.com/Mellanox/sriovnet github.com/Mellanox/rdmamap

COPY . .
RUN go install -ldflags="-s -w" -v docker-sriov-plugin

FROM debian:stretch-slim
COPY --from=build /go/bin/docker-sriov-plugin /bin/docker-sriov-plugin

CMD ["/bin/docker-sriov-plugin"]
