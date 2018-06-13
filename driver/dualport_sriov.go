package driver

import (
	"fmt"
	"github.com/docker/go-plugins-helpers/network"
	"log"
	"strconv"
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

	if IsSRIOVSupported(ndevName) == false {
		return fmt.Errorf("SRIOV is unsuppported on %s", ndevName)
	}

	if options[sriovVlan] != "" {
		vlan, _ = strconv.Atoi(options[sriovVlan])
		if vlan < 0 || vlan > 4095 {
			return fmt.Errorf("vlan id out of range")
		}
		if nw.checkVlanNwExist(ndevName, vlan) {
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
	if len(dpNetworks) == 0 {
		dpNetworks = make(map[string]*dpSriovNetwork)
	}
	dpNetworks[nid] = nw

	dev := dpPfDevices[ndevName]
	dev.nwUseRefCount++
	log.Printf("SRIOV CreateNetwork : [%s] IPv4Data : [ %+v ]\n", nw.genNw.id, nw.genNw.IPv4Data)
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
	log.Printf("DiscoverVF vfDev list length : [%d]\n", dev.maxChildDev)
	return nil
}

func (nw *dpSriovNetwork) AllocVF(parentNetdev string) string {
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

	vfDir, err := FindVFDirForNetdev(nw.genNw.ndevName, allocatedDev)
	if err != nil {
		return ""
	}

	SetVFDefaultMacAddress(nw.genNw.ndevName, vfDir, allocatedDev)
	if nw.vlan > 0 {
		SetVFVlan(nw.genNw.ndevName, vfDir, nw.vlan)
	}

	err = SetVFPrivileged(parentNetdev, vfDir, privileged)
	if err != nil {
		return ""
	}

	dev.childNetdevLlist = dev.childNetdevLlist[:len(dev.childNetdevLlist)-1]

	log.Printf("AllocVF parent [ %+v ] vf:%v vfdev: %v\n",
		parentNetdev, allocatedDev, len(dev.childNetdevLlist))
	return allocatedDev
}

func (nw *dpSriovNetwork) FreeVF(pf *dpPfDevice, vfName string) {
	log.Printf("FreeVF %v\n", vfName)
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

	log.Printf("SRIOV CreateEndpoint resp interface: [ %+v ] \n", resp.Interface)
	return resp, nil
}

func (nw *dpSriovNetwork) DeleteEndpoint(endpoint *ptEndpoint) {

	dev := dpPfDevices[nw.genNw.ndevName]

	nw.FreeVF(dev, endpoint.vfName)
	log.Printf("DeleteEndpoint vfDev list length ----------: [ %+d ]\n", len(dev.childNetdevLlist))
}

func (nw *dpSriovNetwork) DeleteNetwork(d *driver, req *network.DeleteNetworkRequest) {

	dev := dpPfDevices[nw.genNw.ndevName]
	dev.nwUseRefCount--

	// multiple vlan based network will share enabled VFs.
	// So first created network enables SRIOV and
	// Last network that gets deleted, disables SRIOV.
	if dev.nwUseRefCount == 0 {
		delete(dpPfDevices, nw.genNw.ndevName)
	}
	delete(dpNetworks, nw.genNw.id)
	log.Printf("DeleteNetwork: total dpNetworks = %d\n", len(dpNetworks))
}
