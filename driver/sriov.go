package driver

import (
	"fmt"
	"strconv"
	"container/list"

	"github.com/docker/go-plugins-helpers/network"

	log "github.com/Sirupsen/logrus"
)

const (
	SRIOV_ENABLED    = "enabled"
	SRIOV_DISABLED   = "disabled"
	sriovUnsupported = "unsupported"
)

type pfDevice struct {
	pciVfDevList  *list.List
	
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
var pfDevices map[string]*pfDevice

func checkVlanNwExist(pfNetdevName string, vlan int) bool {
	if vlan == 0 {
		return false
	}

	for _, nw := range networks {
		if nw.vlan == vlan && nw.genNw.ndevName == pfNetdevName {
			return true
		}
	}
	return false
}

func (nw *sriovNetwork) getGenNw() *genericNetwork {
	return nw.genNw
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
		if vlan < 0 || vlan > 4095 {
			return fmt.Errorf("Invalid vlan id given")
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

	dev := pfDevices[ndevName]
	dev.nwUseRefCount++
	log.Debugf("SRIOV CreateNetwork : [%s] IPv4Data : [ %+v ]\n", nw.genNw.id, nw.genNw.IPv4Data)
	return nil
}

func disableSRIOV(pfNetdevName string) {

	netdevDisableSRIOV(pfNetdevName)
	dev := pfDevices[pfNetdevName]
	dev.state = SRIOV_DISABLED
	dev.pciVfDevList = nil
}

func initSriovState(pfNetdevName string, dev *pfDevice) error {
	var vfNetdevName string
	var vf *list.Element
	var curVFs int
	var err error

	dev.maxVFCount, err = netdevGetMaxVFCount(pfNetdevName)
	if err != nil {
		return err
	}
	curVFs, err = netdevGetEnabledVFCount(pfNetdevName)
	if err != nil {
		return err
	}
	if curVFs != 0 {
		dev.state = SRIOV_ENABLED
	} else {
		dev.state = SRIOV_DISABLED
	}

	if dev.state == SRIOV_DISABLED {
		err = netdevEnableSRIOV(pfNetdevName)
		if err != nil {
			return err
		}
		dev.state = SRIOV_ENABLED
	}

	// if we haven't discovered VFs yet, try to discover
	if dev.pciVfDevList == nil {
		vfList, err2 := GetVfPciDevList(pfNetdevName)
		if err2 != nil {
			disableSRIOV(pfNetdevName)
			return err2
		}
		dev.pciVfDevList = list.New()
		for _, vf := range vfList {
			 dev.pciVfDevList.PushBack(vf)
		}
	}

	for vf = dev.pciVfDevList.Front(); vf != nil; vf = vf.Next() {
		vfNetdevName = vfNetdevNameFromParent(pfNetdevName, vf.Value.(string))
	
		SetVFDefaultMacAddress(pfNetdevName, vf.Value.(string), vfNetdevName)
	
		pciDevName := vfPCIDevNameFromVfDir(pfNetdevName, vf.Value.(string))
		unbindVF(pfNetdevName, pciDevName)
		bindVF(pfNetdevName, pciDevName)
	}

	return nil
}

func (nw *sriovNetwork) DiscoverVFs(pfNetdevName string) error {
	var err error

	if len(pfDevices) == 0 {
		pfDevices = make(map[string]*pfDevice)
	}

	dev := pfDevices[pfNetdevName]
	if dev == nil {
		newDev := pfDevice{}
		err = initSriovState(pfNetdevName, &newDev)
		if err != nil {
			return err
		}
		pfDevices[pfNetdevName] = &newDev
		dev = &newDev
	}
	log.Debugf("DiscoverVF vfDev list length : [%d]",
		dev.pciVfDevList.Len())
	return nil
}

func (nw *sriovNetwork) AllocVF(pfNetdev string) (string, string) {
	var allocatedDev string
	var vfNetdevName string
	var privileged bool

	if nw.privileged > 0 {
		privileged = true
	} else {
		privileged = false
	}

	dev := pfDevices[pfNetdev]
 
	if dev.pciVfDevList == nil {
		return "", ""
	}

	// pick first element
	e := dev.pciVfDevList.Front()
	if e == nil {
		return "", ""
	}
	allocatedDev = e.Value.(string)

	vfNetdevName = vfNetdevNameFromParent(pfNetdev, allocatedDev)
	if vfNetdevName == "" {
		return "", ""
	}

	pciDevName := vfPCIDevNameFromVfDir(pfNetdev, allocatedDev)
	if pciDevName != "" {
		SetVFDefaultMacAddress(pfNetdev, allocatedDev, vfNetdevName)
		if nw.vlan > 0 {
			SetVFVlan(pfNetdev, allocatedDev, nw.vlan)
		}

		err := SetVFPrivileged(pfNetdev, allocatedDev, privileged)
		if err != nil {
			return "", ""
		}
	}

	dev.pciVfDevList.Remove(e)

	log.Debugf("AllocVF PF [ %+v ] vf:%v vfdev: %v, len %v",
		pfNetdev, allocatedDev, vfNetdevName,
		dev.pciVfDevList.Len())
	return allocatedDev, vfNetdevName
}

func (nw *sriovNetwork) AllocVFByMacAddr(pfNetdev string, vfMacAddress string) (string, string) {
	var allocatedDev string
	var vfNetdevName string
	var privileged bool
	var vf *list.Element

	if nw.privileged > 0 {
		privileged = true
	} else {
		privileged = false
	}

	dev := pfDevices[pfNetdev]
	if dev.pciVfDevList == nil {
		return "", ""
	}

	for vf = dev.pciVfDevList.Front(); vf != nil; vf = vf.Next() {
		vfNetdevName = vfNetdevNameFromParent(pfNetdev, vf.Value.(string))

		macAddr, _ := GetVFDefaultMacAddr(vfNetdevName)
		if vfMacAddress == macAddr {
			allocatedDev = vf.Value.(string)
			break
		}
	}
	if allocatedDev == "" {
		log.Debugf("AllocVFByMacAddr PF Device for MAC %v not found len %v",
			pfNetdev, vfMacAddress, dev.pciVfDevList.Len())
		return "", ""
	}

	pciDevName := vfPCIDevNameFromVfDir(pfNetdev, allocatedDev)
	if pciDevName != "" {
		SetVFDefaultMacAddress(pfNetdev, allocatedDev, vfNetdevName)
		if nw.vlan > 0 {
			SetVFVlan(pfNetdev, allocatedDev, nw.vlan)
		}

		err := SetVFPrivileged(pfNetdev, allocatedDev, privileged)
		if err != nil {
			return "", ""
		}
	}

	/* get the new name, as this name can change after unbind-bind sequence */
	vfNetdevName = vfNetdevNameFromParent(pfNetdev, allocatedDev)
	if vfNetdevName == "" {
		return "", ""
	}

	dev.pciVfDevList.Remove(vf)

	log.Debugf("AllocVF PF [ %+v ] vf:%v vfdev: %v, len %v",
		pfNetdev, allocatedDev, vfNetdevName, dev.pciVfDevList.Len())
	return allocatedDev, vfNetdevName
}

func FreeVF(dev *pfDevice, vfName string) {
	log.Debugf("FreeVF %v", vfName)
	dev.pciVfDevList.PushBack(vfName)
}

func (nw *sriovNetwork) CreateEndpoint(r *network.CreateEndpointRequest) (*network.CreateEndpointResponse, error) {
	var netdevName string
	var vfName string

	if r.Interface.MacAddress != "" {
		vfName, netdevName = nw.AllocVFByMacAddr(nw.genNw.ndevName, r.Interface.MacAddress)
	} else {
		vfName, netdevName = nw.AllocVF(nw.genNw.ndevName)
	}

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

	dev := pfDevices[nw.genNw.ndevName]

	FreeVF(dev, endpoint.vfName)
	log.Debugf("DeleteEndpoint vfDev list length ----------: [ %+d ]",
		dev.pciVfDevList.Len())
}

func (nw *sriovNetwork) DeleteNetwork(d *driver, req *network.DeleteNetworkRequest) {

	dev := pfDevices[nw.genNw.ndevName]
	dev.nwUseRefCount--

	// multiple vlan based network will share enabled VFs.
	// So first created network enables SRIOV and
	// Last network that gets deleted, disables SRIOV.
	if dev.nwUseRefCount == 0 {
		disableSRIOV(nw.genNw.ndevName)
		delete(pfDevices, nw.genNw.ndevName)
	}
	delete(networks, nw.genNw.id)
	log.Debugf("DeleteNetwork: total networks = %d", len(networks))
}
