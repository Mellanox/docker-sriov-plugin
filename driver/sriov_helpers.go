package driver

import (
	"fmt"
)

const (
	netSysDir = "/sys/class/net"
	netDevPrefix = "device"

	netDevMaxVFCountFile = "sriov_totalvfs"
	netDevCurrentVFCountFile = "sriov_numvfs"
	netDevVFDevicePrefix = "virtfn"
)

func netDevDeviceDir(netDevName string) (string) {
	devDirName := netSysDir + "/" + netDevName + "/" + netDevPrefix
	return devDirName
}

func netdevGetMaxVFCount(name string) (int, error) {
	devDirName := netDevDeviceDir(name)

	maxDevFile := fileObject {
				Path: devDirName + "/" + netDevMaxVFCountFile,
		       }

	maxVfs, err := maxDevFile.ReadInt()
	if err != nil {
		return 0, err
	} else {
		fmt.Println("max_vfs = ", maxVfs)
		return maxVfs, nil
	}
}

func netdevSetMaxVFCount(name string, maxVFs int) (error) {
	devDirName := netDevDeviceDir(name)

	maxDevFile := fileObject {
				Path: devDirName + "/" + netDevCurrentVFCountFile,
		       }

	return maxDevFile.WriteInt(maxVFs)
}

func netdevGetEnabledVFCount(name string) (int, error) {
	devDirName := netDevDeviceDir(name)

	maxDevFile := fileObject {
				Path: devDirName + "/" + netDevCurrentVFCountFile,
		       }

	curVfs, err := maxDevFile.ReadInt()
	if err != nil {
		return 0, err
	} else {
		fmt.Println("cur_vfs = ", curVfs)
		return curVfs, nil
	}
}

func netdevEnableSRIOV(name string) (error) {
	var maxVFCount int
	var err error

	devDirName := netDevDeviceDir(name)

	devExist := dirExists(devDirName)
	if !devExist {
		return fmt.Errorf("device not found")
	}

	maxVFCount, err = netdevGetMaxVFCount(name)
	if err != nil {
		fmt.Println("netdevice found", name, maxVFCount)
		return err
	}

	if maxVFCount != 0 {
		return netdevSetMaxVFCount(name, maxVFCount)	
	} else {
		return fmt.Errorf("sriov unsupported")
		return nil
	}
}

func netdevDisableSRIOV(name string) (error) {
	devDirName := netDevDeviceDir(name)

	devExist := dirExists(devDirName)
	if !devExist {
		return fmt.Errorf("device not found")
	}

	return netdevSetMaxVFCount(name, 0)	
}

func netdevGetVfNetdevList(name string) ([]string, error) {
	var vfNetdevNames []string
	var i int
	devDirName := netDevDeviceDir(name)

	virtFnDirs, err := lsFilesWithPrefix(devDirName, netDevVFDevicePrefix, true)

	if (err != nil) {
		return nil, err
	}

	i = 0
	for _, vfDir := range virtFnDirs {
		fname, _ := lsFilesWithPrefix(devDirName + "/" + vfDir + "/" + "net", "", false)

		if len(fname) <= 0 {
			continue;
		}
		if len(fname) > 1 {
			return nil, nil
		}

		vfNetdevNames = append(vfNetdevNames, fname[0])
		fmt.Println("virtual device name = ", vfNetdevNames[i])
		i++
	}
	return vfNetdevNames, nil
}
