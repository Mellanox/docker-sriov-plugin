package driver

import (
	"os"
	"fmt"
	"path/filepath"
	"os/exec"
	"strings"
	"strconv"
)

func checkMultiPortDevice(netdevName string) bool {
	var pciPath string
	var parentPciPath string

	parentPciPath = filepath.Join(netSysDir, netdevName, netDevPrefix)
	parentPciPath, _ = os.Readlink(parentPciPath)
	if parentPciPath == "" {
		return false
	}

	flist, err := lsDirs(netSysDir)
	if err != nil {
		return false
	}
	for i := range flist {
		/* ignore the self device in the search */
		if flist[i] == netdevName {
			continue
		}

		pciPath = filepath.Join(netSysDir, flist[i], netDevPrefix)
		pciPath, _ = os.Readlink(pciPath)
		if (pciPath == "") {
		} else {
			if pciPath == parentPciPath {
				return true
			}
		}
	}
	return false
}

func ibdev2netdevString() ([]string, error) {
	var outStr string

	out, err := exec.Command("/tmp/tools/ibdev2netdev").Output()
	if err != nil {
		return nil, err
	}
	outStr = string(out)
	/* get each entry for ibdevice, port to netdevice */
	outLines := strings.Split(outStr, "\n")
	return outLines, nil
}

type ndevPortMap struct {
	ndevName string
	port	int
}

func GetNetdevicePortMap() ([]ndevPortMap) {
	var npMap []ndevPortMap

	outLines, err := ibdev2netdevString()
	if err != nil {
		return nil
	}
	for _, line := range outLines {
		wordsInLine := strings.Split(line, " ")
		if len(wordsInLine) < 6 {
			continue
		}

		port, err2 := strconv.Atoi(wordsInLine[2])
		if err2 != nil {
			continue
		}
		npEntry := new(ndevPortMap)
		npEntry.ndevName = wordsInLine[4]
		npEntry.port = port
		npMap = append(npMap, *npEntry)
	}
	return npMap
}

func findPhyPort(netdevName string) (int) {
	var npMap []ndevPortMap
	var port int

	npMap = GetNetdevicePortMap()
	if npMap == nil {
		return -1
	}

	port = -1
	ndevSearchName := netdevName + "__"
	for _, entry := range npMap {
		ibndevName :=  entry.ndevName + "__"
		if ndevSearchName == ibndevName {
			fmt.Printf("FOUND device = %s port = %d\n", entry.ndevName, entry.port)
			port = entry.port
		}
	}
	return port
}

func GetChildNetdevListByPort(netdevName string) ([]string, error) {
	var netdevList []string
	var port int

	port = findPhyPort(netdevName)
	if port <= 0 {
		return nil, fmt.Errorf("Fail to find physical port")
	}

	npMap := GetNetdevicePortMap()
	if npMap == nil {
		return nil, fmt.Errorf("fail to build port netdevice map")
	}

	for _, entry := range npMap {
		/* ignore if the port is not matching */
		if entry.port != port {
			continue
		}
		/* skip the self */
		if entry.ndevName == netdevName {
			continue
		}
		netdevList = append(netdevList, entry.ndevName)
	}
	fmt.Println("ndev list lengh = ", len(netdevList))
	return netdevList, nil
}
