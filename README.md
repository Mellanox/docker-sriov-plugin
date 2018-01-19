docker-passthrough-plugin
=========================

### Overview

This network plugin allows to have direct/passthrough access to the native Ethernet networking device to the Docker container(s).
It provides two modes of operations.

(1) sriov

    In this mode given netdev interface is used as PCIe physical function to define a network.
    All container instances will get one PCIe VF based network device when they are started.
    This mode uses PCIe SRIOV capability of the network devices.

    sriov mode provides native access to the actual PCIe based networking device without any overheads of virtual devices.
    With this mode, every container can get dedicated NIC Tx and Rx queues to send receive application data without any contention
    to other containers.

    In sriov mode, plugin driver takes care to enable/disable sriov, assigning VF based network device to container during
    starting a container. This will reduce administrative overheads in dealing with sriov enablement.

(2) passthrough
    
    In this mode given netdev interface is mapped to a container.
    Which means that there is one network device per network, and therefore every container gets one network.

    In some cases there would be need to map bonded device directly without additional layer and without consuming any
    extra mac address. In such cases this passthrough plugin driver will be equally useful.

In some sense both modes are similar to passthrough mode of KVM or similar virtualization technology.

With this plugin based interfaces, there is no limitation of IP address subnet for netdevice of container and netdevice of host.
Any container can have any ip address, same or different subnet as that of host or other containers.

In future more settings for each such netdevice and network will be added.

### Usecases

(a) In certain use cases where high performance networking application running as container can benefit from such native devices.

(b) It is probably good fit for NFV applications which can benefit of hardware based isolation, NIC adapter based switching, granular control of the device, possibly at lower cpu utilization.

(c) nested virtualization - where macvlan or ipvlan based nested containers on top of VF based network interface

(d) When using InfiniBand (not RoCE), IPoIB netdevices can be directly mapped as passthrough devices without creating additional virtual devices.

### QuickStart Instructions

The quickstart instructions describe how to start the plugin and make use of it.

**1.** Make sure you are using Docker 1.9 or later

**2.** Get the new plugin

```
$ docker pull mellanox/passthrough-plugin
```

**3.** Run the plugin now
```
$ docker run -v /run/docker/plugins:/run/docker/plugins -v /etc/docker:/etc/docker --net=host --privileged mellanox/passthrough-plugin
```
This will start the container and emits console logs of the plugin where its started.
The powerful aspect of this is, it doesn't require user/administrator to restart the docker engine.

This persists the network configuration in /etc/docker/mellanox directory.

**4.** Test it out - SRIOV mode

**4.1** Now you are ready to create a new network

Below ens2f0 is PF based netdevice.
Mode is set to sriov, so plugin driver will automatically assign right VF netdevice
to container when a container is started.
Subnet of the netdevice of container and host can be different.

```
$ docker network create -d passthrough --subnet=194.168.1.0/24 -o netdevice=ens2f0 -o mode=sriov mynet
```

**4.2** Now you are ready run container to make use of passthrough-sriov network and its interface
```
$ docker run --net=mynet -itd --name=web nginx

```

**4.3** Multi tenant support in SRIOV mode

There might be a need to isolate containers to groups/tenants.
This plugin allows to create vlan based layer 2 networks.
Every new container started in a particular network is isolated from other network.
Below two commands creates two networks.
Each network is identified with vlan. vlan usage is completely transparent to running container applications.
customer1 has vlan 100.
customer2 has vlan 200.
All containers created in customer1 belong to vlan 100 and cannot talk to containers running in customer2 network which is in vlan 200.

```
$ docker network create -d passthrough --subnet=194.168.1.0/24 -o netdevice=ens2f0 -o mode=sriov -o vlan=100 customer1
```

```
$ docker network create -d passthrough --subnet=194.168.1.0/24 -o netdevice=ens2f0 -o mode=sriov -o vlan=200 customer2
```

```
$ docker run --net=customer1 -itd --name=web nginx
```

```
$ docker run --net=customer2 -itd --name=web nginx
```


**4.4** Secure networks

By default this plugin creates unprivileged networks. This means all containers in such network will not be able to perform spoof attack or sniff packets of other containers.
This plugin uses such hardware offload features for spoofing attacks.

However certain trusted network services running using DPDK or other frameworks would need to modify L2 addresses.
To enable access for trusted applications network should be created using 'privileged' option.
Below command created privileged network. All containers running in this network will have access to modify L2 addresses and also sniff traffic.

```
$ docker network create -d passthrough --subnet=194.168.1.0/24 -o netdevice=ens2f0 -o mode=sriov -o vlan=100 -o privileged=1 customer1
```

**5.** Test it out Passthrough mode

**5.1** Now you are ready to create a new network

```
$ docker network create -d passthrough --subnet=194.168.1.0/24 -o netdevice=ens2f0 -o mode=passthrough mynet
```

**5.2** Now you are ready run container to make use of passthrough network and its interface
```
$ docker run --net=mynet -itd --name=web nginx

```


**6.** Network Creation options list

1. netdevice - parent network device to use for creating netdevice interfaces
2. mode - passthrough/sriov
3. vlan - vlan offload to use for child netdevices
4. privileged - indicating privileged network that can sniff packets, and modify L2 addresses

### Limitations

It supported on Linux environment.
