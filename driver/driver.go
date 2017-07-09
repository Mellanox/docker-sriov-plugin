package driver

import (
	"fmt"
	"net"
	"sync"
	"reflect"

	"github.com/docker/go-plugins-helpers/network"
	"github.com/docker/libnetwork/netlabel"
	"github.com/docker/libnetwork/options"

	log "github.com/Sirupsen/logrus"
)

const (
	containerVethPrefix	= "eth"
	parentOpt		= "netdevice"   // netdevice interface -o netdevice

	networkMode		= "mode"
	networkModePT		= "passthrough"
	networkModeSRIOV	= "sriov"
)

type ptEndpoint struct {
	/* key */
	id		string

	/* value */
	HardwareAddr	string
	devName		string
	mtu		int
	Address		string
	sandboxKey	string
	vfName		string
}

const (
	SRIOV_ENABLED	= "enabled"
	SRIOV_DISABLED	= "disabled"
	sriovUnsupported = "unsupported"
)

type genericNetwork struct {
	id		string
	lock		sync.Mutex
	IPv4Data	*network.IPAMData
	ndevEndpoints	map[string]*ptEndpoint
	driver		*driver // The network's driver
	mode		string	// SRIOV or Passthough

	ndevName	string
}

type ptNetwork struct {
	genNw		*genericNetwork
}

type sriovNetwork struct {
	genNw			*genericNetwork
	vfDevList		[]string
	discoveredVFCount	int
	maxVFCount		int
	state			string
}

type NwIface interface {
	CreateNetwork(d *driver, genNw *genericNetwork,
				   nid string, ndevName string,
				   networkMode string,
				   ipv4Data *network.IPAMData) error
	CreateEndpoint(r *network.CreateEndpointRequest) (*network.CreateEndpointResponse, error)
	DeleteEndpoint(endpoint *ptEndpoint)
	getGenNw() *genericNetwork
}

type driver struct {
	// below map maps a network id to NwInterface object
	networks	map[string]NwIface
	sync.Mutex
}

func createGenNw(nid string, ndevName string,
		 networkMode string, ipv4Data *network.IPAMData) *genericNetwork {

	genNw := genericNetwork { }
	ndevs := map[string]*ptEndpoint{}
	genNw.id = nid
	genNw.mode = networkMode
	genNw.IPv4Data = ipv4Data
	genNw.ndevEndpoints = ndevs
	genNw.ndevName = ndevName
	return &genNw
}

func (d *driver) GetCapabilities() (*network.CapabilitiesResponse, error) {
	return &network.CapabilitiesResponse{Scope: network.LocalScope}, nil
}

// parseNetworkGenericOptions parses generic driver docker network options
func parseNetworkGenericOptions(data interface{}) (string, string, error) {
	var err	error
	var name, mode string

	name = ""
	mode = ""

	switch opt := data.(type) {
	case map[string]interface {}:
		for key, value := range opt {
			switch key {
			case parentOpt:
				name = fmt.Sprintf("%s", value)
			case networkMode:
				mode = fmt.Sprintf("%s", value)
			}
		}
		log.Debugf("parseNetworkGenericOptions netdev: [%s] mode: [%s]", name, mode)
	default:
		log.Debugf("unrecognized network config format: %v\n", reflect.TypeOf(opt))
	}

	if mode == "" {
		// default to passthrough
		mode = networkModePT
	} else {
		if (mode != networkModePT && mode != networkModeSRIOV) {
			return "", "", fmt.Errorf("valid modes are: passthrough and sriov")
		}
	}
	if name == "" {
		if mode == networkModeSRIOV {
			return "", "", fmt.Errorf("sriov mode requires netdevice")
		} else {
			return "", "", fmt.Errorf("passthrough mode requires netdevice")
		}
	}
	return name, mode, err
}

func parseNetworkOptions(id string, option options.Generic) (string, string, error) {
	var err    error
	var name, mode   string

	// parse generic labels first
	genData, ok := option[netlabel.GenericData]
	if ok && genData != nil {
		name, mode, err = parseNetworkGenericOptions(genData);
		if err != nil {
			return "", "", err
		}
	}
	return name, mode, nil
}

func (d *driver) CreateNetwork(req *network.CreateNetworkRequest) error {
	var err error

	log.Debugf("CreateNetwork Called: [ %+v ]\n", req)
	log.Debugf("CreateNetwork IPv4Data len : [ %v ]\n", len(req.IPv4Data))

	d.Lock()
	defer d.Unlock()

	if req.IPv4Data == nil || len(req.IPv4Data) == 0 {
		return fmt.Errorf("Network gateway config miss.")
	}

	name, mode, ret := parseNetworkOptions(req.NetworkID, req.Options)
	if ret != nil {
		log.Debugf("CreateNetwork network options parse error")
		return ret
	}
	if name == "" {
		log.Debugf("CreateNetwork network options invalid/null net device name")
		return fmt.Errorf("CreateNetwork network options invalid/null net device name")
	}
	if mode == "" {
		mode = "passthrough"
	}

	ipv4Data := req.IPv4Data[0]
	

	genNw := createGenNw(req.NetworkID, name, mode, ipv4Data)

	if mode == "passthrough" {
		nw := ptNetwork { }
		err = nw.CreateNetwork(d, genNw, req.NetworkID, name, mode, ipv4Data)
		if err != nil {
			return err
		}
		d.networks[req.NetworkID] = &nw
	} else {
		nw := sriovNetwork { }
		err = nw.CreateNetwork(d, genNw, req.NetworkID, name, mode, ipv4Data)
		if err != nil {
			return err
		}
		d.networks[req.NetworkID] = &nw
	}
	return nil
}

func (d *driver) AllocateNetwork(r *network.AllocateNetworkRequest) (*network.AllocateNetworkResponse, error) {
	log.Debugf("AllocateNetwork Called: [ %+v ]", r)
	return nil, nil
}

func (d *driver) DeleteNetwork(req *network.DeleteNetworkRequest) error {

	log.Debugf("DeleteNetwork Called: [ %+v ]", req)

	d.Lock()
	defer d.Unlock()

	delete(d.networks, req.NetworkID)
	return nil
}

func (d *driver) FreeNetwork(r *network.FreeNetworkRequest) error {
	log.Debugf("FreeNetwork Called: [ %+v ]", r)
	return nil
}

func StartDriver() (*driver, error) {

	// allocate an empty map of network objects that can
	// be later on referred by using id passed in CreateNetwork, DeleteNetwork
	// etc operations.

	//dnetworks := make(map[string]interface{})
	dnetworks := make(map[string]NwIface)

	driver := &driver {
		networks: dnetworks,
	}
	return driver, nil
}

func (d *driver) CreateEndpoint(r *network.CreateEndpointRequest) (*network.CreateEndpointResponse, error) {
	d.Lock()
	defer d.Unlock()

	log.Debugf("CreateEndpoint Called: [ %+v ]", r)
	log.Debugf("r.Interface: [ %+v ]", r.Interface)

	nw := d.networks[r.NetworkID]
	if nw == nil {
		return nil, fmt.Errorf("Plugin can not find network [ %s ].", r.NetworkID)
	}

	return nw.CreateEndpoint(r)
}

func getEndpoint(genNw *genericNetwork, endpointID string) *ptEndpoint {
	return genNw.ndevEndpoints[endpointID]
}

func (nw *ptNetwork) getGenNw() *genericNetwork {
	return nw.genNw
}

func (nw *sriovNetwork) getGenNw() *genericNetwork {
	return nw.genNw
}

func (d *driver) getGenNwFromNetworkID(networkID string) *genericNetwork {
	nw := d.networks[networkID]
	if nw == nil {
		return nil
	}
	return nw.getGenNw()
}

func (d *driver) EndpointInfo(r *network.InfoRequest) (*network.InfoResponse, error) {
	log.Debugf("EndpointInfo Called: [ %+v ]", r)
	d.Lock()
	defer d.Unlock()

	genNw := d.getGenNwFromNetworkID(r.NetworkID)
	if genNw == nil {
		return nil, fmt.Errorf("Can not find network [ %s ].", r.NetworkID)
	}

	endpoint := getEndpoint(genNw, r.EndpointID)
	if endpoint == nil {
		return nil, fmt.Errorf("Cannot find endpoint by id: %s", r.EndpointID)
	}

	value := make(map[string]string)
	value["id"] = endpoint.id
	value["srcName"] = endpoint.devName
	resp := &network.InfoResponse{
		Value: value,
	}
	log.Debugf("EndpointInfo resp.Value : [ %+v ]", resp.Value)
	return resp, nil
}

func (d *driver) Join(r *network.JoinRequest) (*network.JoinResponse, error) {
	log.Debugf("Join Called: [ %+v ]", r)

	d.Lock()
	defer d.Unlock()

	genNw := d.getGenNwFromNetworkID(r.NetworkID)
	if genNw == nil {
		return nil, fmt.Errorf("Can not find network [ %s ].", r.NetworkID)
	}

	endpoint := getEndpoint(genNw, r.EndpointID)
	if endpoint == nil {
		return nil, fmt.Errorf("Cannot find endpoint by id: %s", r.EndpointID)
	}

	if endpoint.sandboxKey != "" {
		return nil, fmt.Errorf("Endpoint [%s] has bean bind to sandbox [%s]", r.EndpointID, endpoint.sandboxKey)
	}
	gw, _, err := net.ParseCIDR(genNw.IPv4Data.Gateway)
	if err != nil {
		return nil, fmt.Errorf("Parse gateway [%s] error: %s", genNw.IPv4Data.Gateway, err.Error())
	}
	endpoint.sandboxKey = r.SandboxKey
	resp := network.JoinResponse{
		InterfaceName:         network.InterfaceName {
						SrcName: endpoint.devName,
						DstPrefix: containerVethPrefix,
					},
		DisableGatewayService: false,
		Gateway:               gw.String(),
	}

	log.Debugf("Join resp : [ %+v ]", resp)
	return &resp, nil
}

func (d *driver) Leave(r *network.LeaveRequest) error {
	log.Debugf("Leave Called: [ %+v ]", r)
	d.Lock()
	defer d.Unlock()

	genNw := d.getGenNwFromNetworkID(r.NetworkID)
	if genNw == nil {
		return fmt.Errorf("Can not find network [ %s ].", r.NetworkID)
	}

	endpoint := getEndpoint(genNw, r.EndpointID)
	if endpoint == nil {
		return fmt.Errorf("Cannot find endpoint by id: %s", r.EndpointID)
	}

	endpoint.sandboxKey = ""
	return nil
}

func (d *driver) DeleteEndpoint(r *network.DeleteEndpointRequest) error {
	log.Debugf("DeleteEndpoint Called: [ %+v ]", r)

	d.Lock()
	defer d.Unlock()

	genNw := d.getGenNwFromNetworkID(r.NetworkID)
	if genNw == nil {
		return fmt.Errorf("Can not find network [ %s ].", r.NetworkID)
	}

	endpoint := getEndpoint(genNw, r.EndpointID)
	if endpoint == nil {
		return fmt.Errorf("Cannot find endpoint by id: %s", r.EndpointID)
	}

	nw := d.networks[r.NetworkID]

	nw.DeleteEndpoint(endpoint)
	delete(genNw.ndevEndpoints, r.EndpointID)
	return nil
}

func (d *driver) DiscoverNew(r *network.DiscoveryNotification) error {
	log.Debugf("DiscoverNew Called: [ %+v ]", r)
	return nil
}
func (d *driver) DiscoverDelete(r *network.DiscoveryNotification) error {
	log.Debugf("DiscoverDelete Called: [ %+v ]", r)
	return nil
}
func (d *driver) ProgramExternalConnectivity(r *network.ProgramExternalConnectivityRequest) error {
	log.Debugf("ProgramExternalConnectivity Called: [ %+v ]", r)
	return nil
}
func (d *driver) RevokeExternalConnectivity(r *network.RevokeExternalConnectivityRequest) error {
	log.Debugf("RevokeExternalConnectivity Called: [ %+v ]", r)
	return nil
}

func (d *driver) getNetworkByGateway(gateway string) error {
	for _, nw := range d.networks {
		genNw := nw.getGenNw()
		if genNw == nil {
			continue
		}
		if genNw.IPv4Data.Gateway == gateway {
			return fmt.Errorf("nw with same gateway exist %s", gateway)
		}
	}
	return nil
}

func (pt *ptNetwork) CreateNetwork(d *driver, genNw *genericNetwork,
				   nid string, ndevName string,
				   networkMode string,
				   ipv4Data *network.IPAMData) error {
	var err error

	err = d.getNetworkByGateway(ipv4Data.Gateway)
	if err != nil {
		return err
	}
	pt.genNw = genNw

	log.Debugf("PT CreateNetwork : [%s] IPv4Data : [ %+v ]\n", pt.genNw.id, pt.genNw.IPv4Data)
	return nil
}

func (nw *ptNetwork) CreateEndpoint(r *network.CreateEndpointRequest) (*network.CreateEndpointResponse, error) {
	if len(nw.genNw.ndevEndpoints) > 0 {
		return nil, fmt.Errorf("supports only one device")
	}

	ndev := &ptEndpoint {
		devName:	nw.genNw.ndevName,
		Address:	r.Interface.Address,
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
	log.Debugf("PT CreateEndpoint resp interface: [ %+v ] ", resp.Interface)
	return resp, nil
}

func (nw *sriovNetwork) CreateNetwork(d *driver, genNw *genericNetwork,
			       nid string, ndevName string,
			       networkMode string,
			       ipv4Data *network.IPAMData) error {
	var curVFs int
	var err error

	err = d.getNetworkByGateway(ipv4Data.Gateway)
	if err != nil {
		return err
	}

	nw.genNw = genNw

	nw.maxVFCount, err = netdevGetMaxVFCount(ndevName)
	if err != nil {
		return err
	}
	curVFs, err = netdevGetEnabledVFCount(ndevName)
	if err != nil {
		return err
	}
	if curVFs != 0 {
		nw.state = SRIOV_ENABLED
	} else {
		nw.state = SRIOV_DISABLED
	}

	log.Debugf("SRIOV CreateNetwork : [%s] IPv4Data : [ %+v ]\n", nw.genNw.id, nw.genNw.IPv4Data)
	return nil
}


func (nw *sriovNetwork) disableSRIOV() {
	netdevDisableSRIOV(nw.genNw.ndevName)
	nw.state = SRIOV_DISABLED
	nw.vfDevList = nil
	nw.discoveredVFCount = 0
}

func (nw *sriovNetwork) DiscoverVFs() (error) {
	var err error

	if nw.state == SRIOV_DISABLED {
		err = netdevEnableSRIOV(nw.genNw.ndevName)
		if err != nil {
			return err
		}
		nw.state = SRIOV_ENABLED
	}

	// if we haven't discovered VFs yet, try to discover
	if nw.discoveredVFCount == 0 {
		nw.vfDevList, err = vfDevList(nw.genNw.ndevName)
		if err != nil {
			nw.disableSRIOV()
			return err
		}
		nw.discoveredVFCount = len(nw.vfDevList)
	}

	log.Debugf("DiscoverVF vfDev list length : [%d %d]",
		   len(nw.vfDevList), nw.discoveredVFCount)
	return nil
}

func (nw *sriovNetwork) AllocVF(parentNetdev string) (string, string) {
	var allocatedDev string
	var vfNetdevName string

	if len(nw.vfDevList) == 0 {
		return "", ""
	}

	// fetch the last element
	allocatedDev = nw.vfDevList[len(nw.vfDevList) - 1]

	vfNetdevName = vfNetdevNameFromParent(parentNetdev, allocatedDev)
	if vfNetdevName == "" {
		return "", ""
	}

	pciDevName := vfPCIDevNameFromVfDir(parentNetdev, allocatedDev)
	if pciDevName != "" {
		SetVFDefaultMacAddress(parentNetdev, allocatedDev, vfNetdevName)
		unbindVF(parentNetdev, pciDevName)
		bindVF(parentNetdev, pciDevName)
	}

	/* get the new name, as this name can change after unbind-bind sequence */
	vfNetdevName = vfNetdevNameFromParent(parentNetdev, allocatedDev)
	if vfNetdevName == "" {
		return "", ""
	}

	nw.vfDevList = nw.vfDevList[:len(nw.vfDevList) - 1]

	log.Debugf("AllocVF parent [ %+v ] vf:%v vfdev: %v, len %v",
		   parentNetdev, allocatedDev, vfNetdevName, len(nw.vfDevList))
	return allocatedDev, vfNetdevName
}

func (nw *sriovNetwork) FreeVF(name string) {
	log.Debugf("FreeVF %v", name)
	nw.vfDevList = append(nw.vfDevList, name)
}

func (nw *sriovNetwork) CreateEndpoint(r *network.CreateEndpointRequest) (*network.CreateEndpointResponse, error) {
	var netdevName string
	var vfName string
	var err error

	err = nw.DiscoverVFs()
	if err != nil {
		return nil, err
	}

	vfName, netdevName = nw.AllocVF(nw.genNw.ndevName)
	if netdevName == "" {
		return nil, fmt.Errorf("All devices in use [ %s ].", r.NetworkID)
	}
	ndev := &ptEndpoint {
		devName: netdevName,
		vfName: vfName,
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

func (nw *ptNetwork) DeleteEndpoint(endpoint *ptEndpoint) {

}

func (nw *sriovNetwork) DeleteEndpoint(endpoint *ptEndpoint) {

	nw.FreeVF(endpoint.vfName)
	// disable SRIOV when last endpoint is getting deleted
	log.Debugf("DeleteEndpoint  vfDev list length -------------: [ %+d ]", len(nw.vfDevList))
	if len(nw.vfDevList) == nw.discoveredVFCount {
		nw.disableSRIOV()
	}
}


