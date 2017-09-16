package driver

import (
	"fmt"
	"strconv"

	"github.com/docker/go-plugins-helpers/network"

	log "github.com/Sirupsen/logrus"
)

type dpSriovNetwork struct {
	genNw      *genericNetwork
	vlan       int
	privileged int
}

type dpPfDevice struct {
	childNetdevLlist []string
	maxChildDev      int
	state            string
	nwUseRefCount    int
}

// nid to network map
// key = nid
// value = dpSriovNetwork
var dpNetworks map[string]*dpSriovNetwork

// netdevice to sriovstate map
// key = phy netdevice
// value = its sriov state/information
var dpPfDevices map[string]*dpPfDevice

func (nw *dpSriovNetwork) checkVlanNwExist(pfNetdevName string, vlan int) bool {
	if vlan == 0 {
		return false
	}

	for _, cur_nw := range dpNetworks {
		if cur_nw.vlan == vlan && cur_nw.genNw.ndevName == pfNetdevName {
			return true
		}
	}
	return false
}

func (nw *dpSriovNetwork) getGenNw() *genericNetwork {
	return nw.genNw
}

func (nw *dpSriovNetwork) CreateNetwork(d *driver, genNw *genericNetwork,
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
		if nw.checkVlanNwExist(ndevName, vlan) {
			return fmt.Errorf("vlan already exist")
		}
		return fmt.Errorf("vlan not yet supported on dual port devices.")
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
	if len(dpNetworks) == 0 {
		dpNetworks = make(map[string]*dpSriovNetwork)
	}
	dpNetworks[nid] = nw

	dev := dpPfDevices[ndevName]
	dev.nwUseRefCount++
	log.Debugf("SRIOV CreateNetwork : [%s] IPv4Data : [ %+v ]\n", nw.genNw.id, nw.genNw.IPv4Data)
	return nil
}

func (nw *dpSriovNetwork) initSriovState(pfNetdevName string, dev *dpPfDevice) error {

	var err error

	dev.childNetdevLlist, err = GetChildNetdevListByPort(pfNetdevName)
	if err != nil {
		return err
	}

	dev.maxChildDev = len(dev.childNetdevLlist)
	if dev.maxChildDev != 0 {
		dev.state = SRIOV_ENABLED
	} else {
		dev.state = SRIOV_DISABLED
		return fmt.Errorf("SRIOV is disabled [ %s ].", pfNetdevName)
	}
	return nil
}

func (nw *dpSriovNetwork) DiscoverVFs(ndevName string) error {
	var err error

	if len(dpPfDevices) == 0 {
		dpPfDevices = make(map[string]*dpPfDevice)
	}

	dev := dpPfDevices[ndevName]
	if dev == nil {
		newDev := dpPfDevice{}
		err = nw.initSriovState(ndevName, &newDev)
		if err != nil {
			return err
		}
		dpPfDevices[ndevName] = &newDev
		dev = &newDev
	}
	log.Debugf("DiscoverVF vfDev list length : [%d]",
		dev.maxChildDev)
	return nil
}

func (nw *dpSriovNetwork) AllocVF(parentNetdev string) (string) {
	var allocatedDev string
	var privileged bool

	if nw.privileged > 0 {
		privileged = true
	} else {
		privileged = false
	}

	dev := dpPfDevices[parentNetdev]
	if len(dev.childNetdevLlist) == 0 {
		return ""
	}

	// fetch the last element
	allocatedDev = dev.childNetdevLlist[len(dev.childNetdevLlist)-1]
	if allocatedDev == "" {
		return ""
	}

	err := SetVFPrivileged(parentNetdev, allocatedDev, privileged)
	if err != nil {
		return ""
	}

	dev.childNetdevLlist = dev.childNetdevLlist[:len(dev.childNetdevLlist)-1]

	log.Debugf("AllocVF parent [ %+v ] vf:%v vfdev: %v, len %v",
		parentNetdev, allocatedDev, len(dev.childNetdevLlist))
	return allocatedDev
}

func (nw *dpSriovNetwork) FreeVF(pf *dpPfDevice, vfName string) {
	log.Debugf("FreeVF %v", vfName)
	pf.childNetdevLlist = append(pf.childNetdevLlist, vfName)
}

func (nw *dpSriovNetwork) CreateEndpoint(r *network.CreateEndpointRequest) (*network.CreateEndpointResponse, error) {
	var netdevName string

	netdevName = nw.AllocVF(nw.genNw.ndevName)
	if netdevName == "" {
		return nil, fmt.Errorf("All devices in use [ %s ].", r.NetworkID)
	}
	ndev := &ptEndpoint{
		devName: netdevName,
		vfName:  netdevName,
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

func (nw *dpSriovNetwork) DeleteEndpoint(endpoint *ptEndpoint) {

	dev := dpPfDevices[nw.genNw.ndevName]

	nw.FreeVF(dev, endpoint.vfName)
	log.Debugf("DeleteEndpoint vfDev list length ----------: [ %+d ]", len(dev.childNetdevLlist))
}

func (nw *dpSriovNetwork) DeleteNetwork(d *driver, req *network.DeleteNetworkRequest) {
	delete(dpNetworks, nw.genNw.id)

	dev := dpPfDevices[nw.genNw.ndevName]
	dev.nwUseRefCount--

	// multiple vlan based network will share enabled VFs.
	// So first created network enables SRIOV and
	// Last network that gets deleted, disables SRIOV.
	if dev.nwUseRefCount == 0 {
		delete(dpPfDevices, nw.genNw.ndevName)
	}
	log.Debugf("DeleteNetwork: total dpNetworks = %d", len(dpNetworks))
}
