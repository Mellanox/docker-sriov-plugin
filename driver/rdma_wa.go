package driver

import (
	"github.com/Mellanox/rdmamap"
	"path/filepath"
)

func setRoceHopLimitWA(netdevice string, hopLimit uint8) error {
	rdmadev, err := rdmamap.GetRdmaDeviceForNetdevice(netdevice)
	if err != nil {
		return err
	}

	file := filepath.Join(rdmamap.RdmaClassDir, rdmadev, "ttl", "1", "ttl")

	ttlFile := fileObject{
		Path: file,
	}

	return ttlFile.WriteInt(int(hopLimit))
}
