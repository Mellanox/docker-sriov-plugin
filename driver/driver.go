package driver

import (
	"fmt"
	"net"
	"reflect"
	"strconv"
	"sync"

	"github.com/Mellanox/sriovnet"
	"github.com/docker/go-plugins-helpers/network"
	"github.com/docker/libnetwork/netlabel"
	"github.com/docker/libnetwork/options"

	log "github.com/Sirupsen/logrus"
)

const (
	containerVethPrefix = "eth"
	networkDevice       = "netdevice" // netdevice interface -o netdevice

	networkMode       = "mode"
	networkModePT     = "passthrough"
	networkModeSRIOV  = "sriov"
	sriovVlan         = "vlan"
	networkPrivileged = "privileged"
	ethPrefix         = "prefix"
)

type ptEndpoint struct {
	/* key */
	id string

	/* value */
	HardwareAddr string
	devName      string
	mtu          int
	Address      string
	sandboxKey   string
	vfName       string
	vfObj        *sriovnet.VfObj
}

type genericNetwork struct {
	id            string
	lock          sync.Mutex
	IPv4Data      *network.IPAMData
	ndevEndpoints map[string]*ptEndpoint
	driver        *driver // The network's driver
	mode          string  // SRIOV or Passthough
	ethPrefix     string

	ndevName string
}

type ptNetwork struct {
	genNw *genericNetwork
}

type NwIface interface {
	CreateNetwork(d *driver, genNw *genericNetwork,
		nid string, options map[string]string,
		ipv4Data *network.IPAMData) error
	DeleteNetwork(d *driver, req *network.DeleteNetworkRequest)

	CreateEndpoint(r *network.CreateEndpointRequest) (*network.CreateEndpointResponse, error)
	DeleteEndpoint(endpoint *ptEndpoint)

	getGenNw() *genericNetwork
}

type driver struct {
	// below map maps a network id to NwInterface object
	networks map[string]NwIface
	sync.Mutex
}

func createGenNw(nid string, ndevName string,
	networkMode string, ethPrefix string, ipv4Data *network.IPAMData) *genericNetwork {

	genNw := genericNetwork{}
	ndevs := map[string]*ptEndpoint{}
	genNw.id = nid
	genNw.mode = networkMode
	genNw.IPv4Data = ipv4Data
	genNw.ndevEndpoints = ndevs
	genNw.ndevName = ndevName
	genNw.ethPrefix = ethPrefix

	return &genNw
}

func (d *driver) GetCapabilities() (*network.CapabilitiesResponse, error) {
	return &network.CapabilitiesResponse{Scope: network.LocalScope}, nil
}

// parseNetworkGenericOptions parses generic driver docker network options
func parseNetworkGenericOptions(data interface{}) (map[string]string, error) {
	var err error

	options := make(map[string]string)

	switch opt := data.(type) {
	case map[string]interface{}:
		for key, value := range opt {
			switch key {
			case networkDevice:
				options[key] = fmt.Sprintf("%s", value)
			case networkMode:
				options[key] = fmt.Sprintf("%s", value)
			case sriovVlan:
				options[key] = fmt.Sprintf("%s", value)
			case networkPrivileged:
				options[key] = fmt.Sprintf("%s", value)
			case ethPrefix:
				options[key] = fmt.Sprintf("%s", value)
			}
		}
		log.Debugf("parseNetworkGenericOptions %v", options)
	default:
		log.Debugf("unrecognized network config format: %v\n", reflect.TypeOf(opt))
	}

	if options[networkMode] == "" {
		// default to passthrough
		options[networkMode] = networkModePT
	} else {
		if options[networkMode] != networkModePT &&
			options[networkMode] != networkModeSRIOV {
			return options, fmt.Errorf("valid modes are: passthrough and sriov")
		}
	}
	if options[networkDevice] == "" {
		if options[networkMode] == networkModeSRIOV {
			return options, fmt.Errorf("sriov mode requires netdevice")
		} else {
			return options, fmt.Errorf("passthrough mode requires netdevice")
		}
	}

	if options[ethPrefix] == "" {
		options[ethPrefix] = containerVethPrefix
	}

	return options, err
}

func parseNetworkOptions(id string, option options.Generic) (map[string]string, error) {

	// parse generic labels first
	genData, ok := option[netlabel.GenericData]
	if ok && genData != nil {
		options, err := parseNetworkGenericOptions(genData)

		return options, err
	}
	return nil, fmt.Errorf("invalid options")
}

func (d *driver) _CreateNetwork(nid string, options map[string]string,
	ipv4Data *network.IPAMData, storeConfig bool) error {
	var err error

	genNw := createGenNw(nid, options[networkDevice], options[networkMode], options[ethPrefix], ipv4Data)

	if options[networkMode] == "passthrough" {
		nw := ptNetwork{}
		err = nw.CreateNetwork(d, genNw, nid, options, ipv4Data)
		if err != nil {
			return err
		}
		d.networks[nid] = &nw
	} else {
		var multiport bool

		multiport = checkMultiPortDevice(options[networkDevice])
		if multiport == true {
			nw := dpSriovNetwork{}
			err = nw.CreateNetwork(d, genNw, nid, options, ipv4Data)
			if err != nil {
				return err
			}
			d.networks[nid] = &nw
		} else {
			nw := sriovNetwork{}
			err = nw.CreateNetwork(d, genNw, nid, options, ipv4Data)
			if err != nil {
				return err
			}
			d.networks[nid] = &nw
		}
	}

	if storeConfig == true {
		nwDbEntry := Db_Network_Info{}
		nwDbEntry.Mode = options[networkMode]
		nwDbEntry.Netdev = options[networkDevice]
		nwDbEntry.Vlan, _ = strconv.Atoi(options[sriovVlan])
		nwDbEntry.Gateway = ipv4Data.Gateway

		if options[networkPrivileged] == "1" {
			nwDbEntry.Privileged = true
		} else {
			nwDbEntry.Privileged = false
		}

		err = Write_Nw_Config_to_DB(nid, &nwDbEntry)
		if err != nil {
			return err
		}
	}

	return nil
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

	options, ret := parseNetworkOptions(req.NetworkID, req.Options)
	if ret != nil {
		log.Debugf("CreateNetwork network options parse error")
		return ret
	}

	ipv4Data := req.IPv4Data[0]

	err = d._CreateNetwork(req.NetworkID, options, ipv4Data, true)
	return err
}

func (d *driver) AllocateNetwork(r *network.AllocateNetworkRequest) (*network.AllocateNetworkResponse, error) {
	log.Debugf("AllocateNetwork Called: [ %+v ]", r)
	return nil, nil
}

func (d *driver) DeleteNetwork(req *network.DeleteNetworkRequest) error {

	log.Debugf("DeleteNetwork Called: [ %+v ]", req)

	d.Lock()
	defer d.Unlock()

	nw := d.networks[req.NetworkID]
	if nw != nil {
		nw.DeleteNetwork(d, req)
	}

	delete(d.networks, req.NetworkID)

	Del_Nw_Config_From_DB(req.NetworkID)
	return nil
}

func (d *driver) FreeNetwork(r *network.FreeNetworkRequest) error {
	log.Debugf("FreeNetwork Called: [ %+v ]", r)
	return nil
}

func BuildNetworkOptions(nwDbEntry *Db_Network_Info) (map[string]string, error) {

	options := make(map[string]string)

	options[networkDevice] = nwDbEntry.Netdev
	options[networkMode] = nwDbEntry.Mode
	options[sriovVlan] = strconv.Itoa(nwDbEntry.Vlan)
	if nwDbEntry.Privileged {
		options[networkPrivileged] = "1"
	} else {
		options[networkPrivileged] = "0"
	}
	return options, nil
}

func (d *driver) CreatePersistentNetworks() error {
	nwList, err := Read_Past_Config(persistConfigPath)
	if err != nil {
		return err
	}

	for _, n := range nwList {
		options, _ := BuildNetworkOptions(&n.Info)

		ipv4Data := network.IPAMData{}
		ipv4Data.Gateway = n.Info.Gateway

		/* Create nw, but ignore the error.
		 * This can happen when plugin is stopped and networks are
		 * Deleted at the docker engine level, which plugin is
		 * completely unaware of.
		 */
		_ = d._CreateNetwork(n.NetworkID, options, &ipv4Data, false)
	}
	return nil
}

func StartDriver() (*driver, error) {

	// allocate an empty map of network objects that can
	// be later on referred by using id passed in CreateNetwork, DeleteNetwork
	// etc operations.

	//dnetworks := make(map[string]interface{})
	dnetworks := make(map[string]NwIface)

	driver := &driver{
		networks: dnetworks,
	}

	err := driver.CreatePersistentNetworks()
	if err != nil {
		return nil, err
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
		InterfaceName: network.InterfaceName{
			SrcName:   endpoint.devName,
			DstPrefix: genNw.ethPrefix,
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
	nid string, options map[string]string,
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

func (pt *ptNetwork) DeleteNetwork(d *driver, req *network.DeleteNetworkRequest) {

}

func (nw *ptNetwork) CreateEndpoint(r *network.CreateEndpointRequest) (*network.CreateEndpointResponse, error) {
	if len(nw.genNw.ndevEndpoints) > 0 {
		return nil, fmt.Errorf("supports only one device")
	}

	ndev := &ptEndpoint{
		devName: nw.genNw.ndevName,
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
	log.Debugf("PT CreateEndpoint resp interface: [ %+v ] ", resp.Interface)
	return resp, nil
}

func (nw *ptNetwork) DeleteEndpoint(endpoint *ptEndpoint) {

}
