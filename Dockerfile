FROM golang:1.10.1 as build
WORKDIR /go/src/docker-sriov-plugin

RUN go get github.com/docker/docker/client
RUN go get github.com/docker/docker/api/types

RUN go get github.com/golang/dep/cmd/dep
COPY Gopkg.toml Gopkg.lock ./
RUN dep ensure -v -vendor-only

COPY . .
RUN export CGO_LDFLAGS_ALLOW='-Wl,--unresolved-symbols=ignore-in-object-files' && \
    go install -ldflags="-s -w" -v docker-sriov-plugin

FROM debian:stretch-slim
COPY --from=build /go/bin/docker-sriov-plugin /bin/docker-sriov-plugin

CMD ["/bin/docker-sriov-plugin"]
