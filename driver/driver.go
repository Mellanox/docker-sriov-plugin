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
}

const (
	sriovEnabled	= "enabled"
	sriovDisabled	= "disabled"
	sriovUnsupported = "unsupported"
)

type sriovNetworkState struct {
	vfDevList		[]string	
	discoveredVFCount	int
	maxVFCount		int
	state			string
}

type ptNetwork struct {
	id		string
	IPv4Data	*network.IPAMData
	ndevEndpoints	map[string]*ptEndpoint
	driver		*driver // The network's driver
	ndevName	string
	mode		string	// SRIOV or Passthough
	sriovState	sriovNetworkState 
	lock		sync.Mutex
}

type driver struct {
	// below map maps a network id to ptNetwork object
	networks	map[string]*ptNetwork
	sync.Mutex
}

func (d *driver) RegisterNetwork(nid string, ndevName string,
				 networkMode string,
				 ipv4Data *network.IPAMData) error {
	var err error

	if nw := d.getNetworkByGateway(ipv4Data.Gateway); nw != nil {
		return fmt.Errorf("Exist network [%s] with same gateway [%s]", nw.id, nw.IPv4Data.Gateway)
	}

	ndevs := map[string]*ptEndpoint{}
	nw := ptNetwork {
		id:        nid,
		IPv4Data:  ipv4Data,
		ndevEndpoints: ndevs,
		ndevName: ndevName,
		mode : networkMode,
	}

	if networkMode == networkModeSRIOV {
		var curVFs int
		nw.sriovState.maxVFCount, err = netdevGetMaxVFCount(ndevName)
		if err != nil {
			return err
		}
		curVFs, err = netdevGetEnabledVFCount(ndevName)
		if err != nil {
			return err
		}
		if curVFs != 0 {
			nw.sriovState.state = sriovEnabled
		} else {
			nw.sriovState.state = sriovDisabled
		}
	}

	d.networks[nid] = &nw 
	log.Debugf("RegisterNetwork : [%s] IPv4Data : [ %+v ]\n", nw.id, nw.IPv4Data)
	return nil
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
	err := d.RegisterNetwork(req.NetworkID, name, mode, ipv4Data)
	if err != nil {
		return err
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

	dnetworks := map[string]*ptNetwork{}

	driver := &driver {
		networks: dnetworks,
	}
	return driver, nil
}

func (d *driver) CreatePTEndpoint(r *network.CreateEndpointRequest,
				  nw *ptNetwork) (*network.CreateEndpointResponse, error) {
	if len(nw.ndevEndpoints) > 0 {
		return nil, fmt.Errorf("supports only one device")
	}

	ndev := &ptEndpoint {
		devName:   nw.ndevName,
		Address: r.Interface.Address,
	}
	nw.ndevEndpoints[r.EndpointID] = ndev

	endpointInterface := &network.EndpointInterface{}
	if r.Interface.Address == "" {
		endpointInterface.Address = ndev.Address
	}
	if r.Interface.MacAddress == "" {
		//endpointInterface.MacAddress = ndev.HardwareAddr
	}
	resp := &network.CreateEndpointResponse{Interface: endpointInterface}
	log.Debugf("CreateEndpoint resp interface: [ %+v ] ", resp.Interface)
	return resp, nil
}

func (d *driver) DiscoverVFs(nw *ptNetwork) (error) {

	var err error

	if nw.sriovState.state == sriovDisabled {
		err = netdevEnableSRIOV(nw.ndevName)
		if err != nil {
			return err
		}
	}
	nw.sriovState.state = sriovEnabled

	// if we haven't discovered them yet, try to discover
	if nw.sriovState.discoveredVFCount == 0 {
		nw.sriovState.vfDevList, err = netdevGetVfNetdevList(nw.ndevName)
		if err != nil {
			netdevDisableSRIOV(nw.ndevName)
			return err
		}
		nw.sriovState.discoveredVFCount = len(nw.sriovState.vfDevList)
	}

	log.Debugf("DiscoverVF vfDev list length -------------: [ %+d ]", len(nw.sriovState.vfDevList))
	return nil
}

func (d *driver) AllocVF(sriovState *sriovNetworkState) (string) {
	var allocatedDev string

	if len(sriovState.vfDevList) == 0 {
		return ""
	}

	// fetch the last element
	allocatedDev = sriovState.vfDevList[len(sriovState.vfDevList) - 1]

	sriovState.vfDevList = sriovState.vfDevList[:len(sriovState.vfDevList) - 1]
	return allocatedDev
}

func (d *driver) FreeVF(sriovState *sriovNetworkState, name string) {
	sriovState.vfDevList = append(sriovState.vfDevList, name)
}

func (d *driver) CreateSRIOVEndpoint(r *network.CreateEndpointRequest,
				  nw *ptNetwork) (*network.CreateEndpointResponse, error) {
	var err error
	var newEpName string

	err = d.DiscoverVFs(nw)
	if err != nil {
		return nil, err
	}

	newEpName = d.AllocVF(&nw.sriovState)
	if newEpName == "" {
		return nil, fmt.Errorf("All devices in use [ %s ].", r.NetworkID)
	}
	ndev := &ptEndpoint {
		devName: newEpName,
		Address: r.Interface.Address,
	}
	nw.ndevEndpoints[r.EndpointID] = ndev

	endpointInterface := &network.EndpointInterface{}
	if r.Interface.Address == "" {
		endpointInterface.Address = ndev.Address
	}
	if r.Interface.MacAddress == "" {
		//endpointInterface.MacAddress = ndev.HardwareAddr
	}
	resp := &network.CreateEndpointResponse{Interface: endpointInterface}
	log.Debugf("CreateEndpoint resp interface: [ %+v ] ", resp.Interface)
	return resp, nil
}

func (d *driver) CreateEndpoint(r *network.CreateEndpointRequest) (*network.CreateEndpointResponse, error) {
	d.Lock()
	defer d.Unlock()

	log.Debugf("CreateEndpoint Called: [ %+v ]", r)
	log.Debugf("r.Interface: [ %+v ]", r.Interface)

	nw := d.networks[r.NetworkID]
	if nw == nil {
		return nil, fmt.Errorf("Can not find network [ %s ].", r.NetworkID)
	}

	if (nw.mode == networkModePT) {
		return d.CreatePTEndpoint(r, nw)
	} else {
		return d.CreateSRIOVEndpoint(r, nw)
	}
}

func (d *driver) EndpointInfo(r *network.InfoRequest) (*network.InfoResponse, error) {
	log.Debugf("EndpointInfo Called: [ %+v ]", r)
	d.Lock()
	defer d.Unlock()

	nw := d.networks[r.NetworkID]
	if nw == nil {
		return nil, fmt.Errorf("Can not find network [ %s ].", r.NetworkID)
	}

	endpoint := nw.ndevEndpoints[r.EndpointID]
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

	nw := d.networks[r.NetworkID]
	if nw == nil {
		return nil, fmt.Errorf("Can not find network [ %s ].", r.NetworkID)
	}

	endpoint := nw.ndevEndpoints[r.EndpointID]
	if endpoint == nil {
		return nil, fmt.Errorf("Cannot find endpoint by id: %s", r.EndpointID)
	}

	if endpoint.sandboxKey != "" {
		return nil, fmt.Errorf("Endpoint [%s] has bean bind to sandbox [%s]", r.EndpointID, endpoint.sandboxKey)
	}
	gw, _, err := net.ParseCIDR(nw.IPv4Data.Gateway)
	if err != nil {
		return nil, fmt.Errorf("Parse gateway [%s] error: %s", nw.IPv4Data.Gateway, err.Error())
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

	nw := d.networks[r.NetworkID]
	if nw == nil {
		return fmt.Errorf("Can not find network [ %s ].", r.NetworkID)
	}

	endpoint := nw.ndevEndpoints[r.EndpointID]
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

	nw := d.networks[r.NetworkID]
	if nw == nil {
		return fmt.Errorf("Can not find network [ %s ].", r.NetworkID)
	}

	endpoint := nw.ndevEndpoints[r.EndpointID]
	if endpoint == nil {
		return fmt.Errorf("Cannot find endpoint by id: %s", r.EndpointID)
	}

	if (nw.mode == networkModeSRIOV) {
		d.FreeVF(&nw.sriovState, endpoint.devName)
		// disable SRIOV when last endpoint is getting deleted
		log.Debugf("DeleteEndpoint  vfDev list length -------------: [ %+d ]", len(nw.sriovState.vfDevList))
		if len(nw.sriovState.vfDevList) == nw.sriovState.discoveredVFCount {
			netdevDisableSRIOV(nw.ndevName)
		}
	} 

	delete(nw.ndevEndpoints, r.EndpointID)
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

func (d *driver) getNetworkByGateway(gateway string) *ptNetwork {
	for _, nw := range d.networks {
		if nw.IPv4Data.Gateway == gateway {
			return nw
		}
	}
	return nil
}
