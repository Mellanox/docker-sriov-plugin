package driver

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
)

const (
	persistConfigPath = "/etc/docker/mellanox/docker-sriov-plugin"
)

/* Configuration layout
config/
	version.json
		nw-1/
			config.json
			ep1.json
			ep2.json
			ep3.json
		nw-2/
		nw-3/
*/

/* Network config.json */
type Db_Network_Info struct {
	Version    uint32 `json:"Version"`
	Netdev     string `json:"Netdevice"`
	Mode       string `json:"Mode"`
	Subnet     string `json:"Subnet"`
	Gateway    string `json:"Gateway"`
	Vlan       int    `json:"vlan"`
	Privileged bool   `json:"Privileged"`
}

/* Endpoint config.json */
type DB_Endpoint struct {
	Version   uint32 `json:"Version"`
	HwAddress string `json:"Hw_Address"`
}

type Db_Network struct {
	NetworkID string
	Info      Db_Network_Info
}

func Write_Nw_Config_to_DB(nwKey string, nw *Db_Network_Info) error {
	rawData, err := json.Marshal(nw)
	if err != nil {
		return err
	}

	err = createDir(persistConfigPath)
	if err != nil {
		return err
	}

	nwDir := filepath.Join(persistConfigPath, nwKey)
	err = createDir(nwDir)
	if err != nil {
		return err
	}

	nwFile := filepath.Join(persistConfigPath, nwKey, "config.json")
	err = ioutil.WriteFile(nwFile, rawData, os.FileMode(0644))
	return err
}

func Read_Nw_Config_From_DB(nwKey string) (*Db_Network_Info, error) {

	nwFile := filepath.Join(persistConfigPath, nwKey, "config.json")
	_, err := os.Lstat(nwFile)
	if err != nil {
		return nil, err
	} else if os.IsNotExist(err) {
		return nil, nil
	}

	rawData, err2 := ioutil.ReadFile(nwFile)
	if err2 != nil {
		return nil, err2
	}

	nw := Db_Network_Info{}
	err = json.Unmarshal(rawData, &nw)
	if err != nil {
		return nil, err
	} else {
		return &nw, nil
	}
}

func Del_Nw_Config_From_DB(nwKey string) error {

	nwDir := filepath.Join(persistConfigPath, nwKey)
	os.RemoveAll(nwDir)
	return nil
}

func Read_Past_Config(configDir string) ([]*Db_Network, error) {
	var nwList []*Db_Network

	_, err := os.Lstat(configDir)
	if err != nil {
		return nil, nil
	} else if os.IsNotExist(err) {
		return nil, nil
	}

	handle, err2 := os.Open(configDir)
	if err2 != nil {
		return nil, err2
	}
	defer handle.Close()

	nwKeys, err3 := handle.Readdir(-1)
	if err3 != nil {
		return nil, err3
	}

	for i := range nwKeys {
		nwInfo, err3 := Read_Nw_Config_From_DB(nwKeys[i].Name())
		if err3 != nil {
			return nil, err3
		}
		nw := Db_Network{}
		nw.NetworkID = nwKeys[i].Name()
		nw.Info = *nwInfo
		nwList = append(nwList, &nw)
	}
	return nwList, nil
}
