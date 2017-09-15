package driver

import (
	"fmt"
	"strconv"

	"github.com/docker/go-plugins-helpers/network"

	log "github.com/Sirupsen/logrus"
)

const (
	SRIOV_ENABLED    = "enabled"
	SRIOV_DISABLED   = "disabled"
	sriovUnsupported = "unsupported"
)

type sriovDevice struct {
	pciVfDevList  []string
	maxVFCount    int
	state         string
	nwUseRefCount int
}

type sriovNetwork struct {
	genNw      *genericNetwork
	vlan       int
	privileged int
}

// nid to network map
// key = nid
// value = sriovNetwork
var networks map[string]*sriovNetwork

// netdevice to sriovstate map
// key = phy netdevice
// value = its sriov state/information
var sriovDevices map[string]*sriovDevice

func checkVlanNwExist(ndevName string, vlan int) bool {
	if vlan == 0 {
		return false
	}

	for _, nw := range networks {
		if nw.vlan == vlan && nw.genNw.ndevName == ndevName {
			return true
		}
	}
	return false
}

func (nw *sriovNetwork) CreateNetwork(d *driver, genNw *genericNetwork,
	nid string, options map[string]string,
	ipv4Data *network.IPAMData) error {
	var err error
	var vlan int
	var privileged int

	ndevName := options[networkDevice]
	err = d.getNetworkByGateway(ipv4Data.Gateway)
	if err != nil {
		return err
	}

	if options[sriovVlan] != "" {
		vlan, _ = strconv.Atoi(options[sriovVlan])
		if vlan > 4095 {
			return fmt.Errorf("vlan id out of range")
		}
		if checkVlanNwExist(ndevName, vlan) {
			return fmt.Errorf("vlan already exist")
		}
	}
	if options[networkPrivileged] != "" {
		privileged, _ = strconv.Atoi(options[networkPrivileged])
	}
	nw.privileged = privileged

	nw.genNw = genNw

	err = SetPFLinkUp(ndevName)
	if err != nil {
		return err
	}

	err = nw.DiscoverVFs(ndevName)
	if err != nil {
		return err
	}
	// store vlan so that when VFs are attached to container, vlan will be set at that time
	nw.vlan = vlan
	if len(networks) == 0 {
		networks = make(map[string]*sriovNetwork)
	}
	networks[nid] = nw

	dev := sriovDevices[ndevName]
	dev.nwUseRefCount++
	log.Debugf("SRIOV CreateNetwork : [%s] IPv4Data : [ %+v ]\n", nw.genNw.id, nw.genNw.IPv4Data)
	return nil
}

func disableSRIOV(ndevName string) {

	netdevDisableSRIOV(ndevName)
	dev := sriovDevices[ndevName]
	dev.state = SRIOV_DISABLED
	dev.pciVfDevList = nil
}

func initSriovState(ndevName string, dev *sriovDevice) error {
	var err error
	var curVFs int

	dev.maxVFCount, err = netdevGetMaxVFCount(ndevName)
	if err != nil {
		return err
	}
	curVFs, err = netdevGetEnabledVFCount(ndevName)
	if err != nil {
		return err
	}
	if curVFs != 0 {
		dev.state = SRIOV_ENABLED
	} else {
		dev.state = SRIOV_DISABLED
	}

	if dev.state == SRIOV_DISABLED {
		err = netdevEnableSRIOV(ndevName)
		if err != nil {
			return err
		}
		dev.state = SRIOV_ENABLED
	}

	// if we haven't discovered VFs yet, try to discover
	if len(dev.pciVfDevList) == 0 {
		dev.pciVfDevList, err = GetVfPciDevList(ndevName)
		if err != nil {
			disableSRIOV(ndevName)
			return err
		}
	}
	return nil
}

func (nw *sriovNetwork) DiscoverVFs(ndevName string) error {
	var err error

	if len(sriovDevices) == 0 {
		sriovDevices = make(map[string]*sriovDevice)
	}

	dev := sriovDevices[ndevName]
	if dev == nil {
		newDev := sriovDevice{}
		err = initSriovState(ndevName, &newDev)
		if err != nil {
			return err
		}
		sriovDevices[ndevName] = &newDev
		dev = &newDev
	}
	log.Debugf("DiscoverVF vfDev list length : [%d]",
		len(dev.pciVfDevList))
	return nil
}

func (nw *sriovNetwork) AllocVF(parentNetdev string) (string, string) {
	var allocatedDev string
	var vfNetdevName string
	var privileged bool

	if nw.privileged > 0 {
		privileged = true
	} else {
		privileged = false
	}

	dev := sriovDevices[parentNetdev]
	if len(dev.pciVfDevList) == 0 {
		return "", ""
	}

	// fetch the last element
	allocatedDev = dev.pciVfDevList[len(dev.pciVfDevList)-1]

	vfNetdevName = vfNetdevNameFromParent(parentNetdev, allocatedDev)
	if vfNetdevName == "" {
		return "", ""
	}

	pciDevName := vfPCIDevNameFromVfDir(parentNetdev, allocatedDev)
	if pciDevName != "" {
		SetVFDefaultMacAddress(parentNetdev, allocatedDev, vfNetdevName)
		if nw.vlan > 0 {
			SetVFVlan(parentNetdev, allocatedDev, nw.vlan)
		}

		err := SetVFPrivileged(parentNetdev, allocatedDev, privileged)
		if err != nil {
			return "", ""
		}
		unbindVF(parentNetdev, pciDevName)
		bindVF(parentNetdev, pciDevName)
	}

	/* get the new name, as this name can change after unbind-bind sequence */
	vfNetdevName = vfNetdevNameFromParent(parentNetdev, allocatedDev)
	if vfNetdevName == "" {
		return "", ""
	}

	dev.pciVfDevList = dev.pciVfDevList[:len(dev.pciVfDevList)-1]

	log.Debugf("AllocVF parent [ %+v ] vf:%v vfdev: %v, len %v",
		parentNetdev, allocatedDev, vfNetdevName, len(dev.pciVfDevList))
	return allocatedDev, vfNetdevName
}

func FreeVF(dev *sriovDevice, vfName string) {
	log.Debugf("FreeVF %v", vfName)
	dev.pciVfDevList = append(dev.pciVfDevList, vfName)
}

func (nw *sriovNetwork) CreateEndpoint(r *network.CreateEndpointRequest) (*network.CreateEndpointResponse, error) {
	var netdevName string
	var vfName string

	vfName, netdevName = nw.AllocVF(nw.genNw.ndevName)
	if netdevName == "" {
		return nil, fmt.Errorf("All devices in use [ %s ].", r.NetworkID)
	}
	ndev := &ptEndpoint{
		devName: netdevName,
		vfName:  vfName,
		Address: r.Interface.Address,
	}
	nw.genNw.ndevEndpoints[r.EndpointID] = ndev

	endpointInterface := &network.EndpointInterface{}
	if r.Interface.Address == "" {
		endpointInterface.Address = ndev.Address
	}
	if r.Interface.MacAddress == "" {
		//endpointInterface.MacAddress = ndev.HardwareAddr
	}
	resp := &network.CreateEndpointResponse{Interface: endpointInterface}

	log.Debugf("SRIOV CreateEndpoint resp interface: [ %+v ] ", resp.Interface)
	return resp, nil
}

func (nw *sriovNetwork) DeleteEndpoint(endpoint *ptEndpoint) {

	dev := sriovDevices[nw.genNw.ndevName]

	FreeVF(dev, endpoint.vfName)
	log.Debugf("DeleteEndpoint vfDev list length ----------: [ %+d ]", len(dev.pciVfDevList))
}

func (nw *sriovNetwork) DeleteNetwork(d *driver, req *network.DeleteNetworkRequest) {
	delete(networks, nw.genNw.id)

	dev := sriovDevices[nw.genNw.ndevName]
	dev.nwUseRefCount--

	// multiple vlan based network will share enabled VFs.
	// So first created network enables SRIOV and
	// Last network that gets deleted, disables SRIOV.
	if dev.nwUseRefCount == 0 {
		disableSRIOV(nw.genNw.ndevName)
		delete(sriovDevices, nw.genNw.ndevName)
	}
	log.Debugf("DeleteNetwork: total networks = %d", len(networks))
}
