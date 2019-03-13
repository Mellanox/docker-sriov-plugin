package driver

import (
	"fmt"
	"github.com/vishvananda/netlink"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	netSysDir        = "/sys/class/net"
	netDevPrefix     = "device"
	netdevDriverDir  = "device/driver"
	netdevUnbindFile = "unbind"
	netdevBindFile   = "bind"

	netDevMaxVFCountFile     = "sriov_totalvfs"
	netDevCurrentVFCountFile = "sriov_numvfs"
	netDevVFDevicePrefix     = "virtfn"
)

func netDevDeviceDir(netDevName string) string {
	devDirName := netSysDir + "/" + netDevName + "/" + netDevPrefix
	return devDirName
}

func netdevGetMaxVFCount(name string) (int, error) {
	devDirName := netDevDeviceDir(name)

	maxDevFile := fileObject{
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

func netdevSetMaxVFCount(name string, maxVFs int) error {
	devDirName := netDevDeviceDir(name)

	maxDevFile := fileObject{
		Path: devDirName + "/" + netDevCurrentVFCountFile,
	}

	return maxDevFile.WriteInt(maxVFs)
}

func netdevGetEnabledVFCount(name string) (int, error) {
	devDirName := netDevDeviceDir(name)

	maxDevFile := fileObject{
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

func netdevEnableSRIOV(name string) error {
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

func netdevDisableSRIOV(name string) error {
	devDirName := netDevDeviceDir(name)

	devExist := dirExists(devDirName)
	if !devExist {
		return fmt.Errorf("device not found")
	}

	return netdevSetMaxVFCount(name, 0)
}

func vfNetdevNameFromParent(parentNetdev string, vfDir string) string {

	devDirName := netDevDeviceDir(parentNetdev)

	vfNetdev, _ := lsFilesWithPrefix(devDirName+"/"+vfDir+"/"+"net", "", false)
	if len(vfNetdev) <= 0 {
		return ""
	} else {
		return vfNetdev[0]
	}
}

func vfPCIDevNameFromVfDir(parentNetdev string, vfDir string) string {
	link := filepath.Join(netSysDir, parentNetdev, netDevPrefix, vfDir)
	pciDevDir, err := os.Readlink(link)
	if err != nil {
		return ""
	}
	if len(pciDevDir) <= 3 {
		return ""
	}

	return pciDevDir[3:len(pciDevDir)]
}

func unbindVF(parentNetdev string, vfPCIDevName string) error {
	cmdFile := filepath.Join(netSysDir, parentNetdev, netdevDriverDir, netdevUnbindFile)

	cmdFileObj := fileObject{
		Path: cmdFile,
	}

	return cmdFileObj.Write(vfPCIDevName)
}

func bindVF(parentNetdev string, vfPCIDevName string) error {
	cmdFile := filepath.Join(netSysDir, parentNetdev, netdevDriverDir, netdevBindFile)

	cmdFileObj := fileObject{
		Path: cmdFile,
	}

	return cmdFileObj.Write(vfPCIDevName)
}

func GetVfPciDevList(name string) ([]string, error) {
	var vfDirList []string
	var i int
	devDirName := netDevDeviceDir(name)

	virtFnDirs, err := lsFilesWithPrefix(devDirName, netDevVFDevicePrefix, true)

	if err != nil {
		return nil, err
	}

	i = 0
	for _, vfDir := range virtFnDirs {
		vfDirList = append(vfDirList, vfDir)
		i++
	}
	return vfDirList, nil
}

func GetVFDefaultMacAddr(vfNetdevName string) (string, error) {

	ethHandle, err1 := netlink.LinkByName(vfNetdevName)
	if err1 != nil {
		return "", err1
	}

	ethAttr := ethHandle.Attrs()
	return ethAttr.HardwareAddr.String(), nil
}

func SetVFDefaultMacAddress(parentNetdev string, vfDir string, vfNetdevName string) error {

	vfIndexStr := strings.TrimPrefix(vfDir, "virtfn")
	vfIndex, _ := strconv.Atoi(vfIndexStr)
	ethHandle, err1 := netlink.LinkByName(vfNetdevName)
	if err1 != nil {
		return err1
	}
	ethAttr := ethHandle.Attrs()

	parentHandle, err1 := netlink.LinkByName(parentNetdev)
	if err1 != nil {
		return err1
	}

	err2 := netlink.LinkSetVfHardwareAddr(parentHandle, vfIndex, ethAttr.HardwareAddr)
	return err2
}

func SetVFVlan(parentNetdev string, vfDir string, vlan int) error {

	vfIndexStr := strings.TrimPrefix(vfDir, "virtfn")
	vfIndex, _ := strconv.Atoi(vfIndexStr)

	parentHandle, err1 := netlink.LinkByName(parentNetdev)
	if err1 != nil {
		return err1
	}

	err2 := netlink.LinkSetVfVlan(parentHandle, vfIndex, vlan)
	return err2
}

func SetVFPrivileged(parentNetdev string, vfDir string, privileged bool) error {

	var spoofChk bool
	var trusted bool

	vfIndexStr := strings.TrimPrefix(vfDir, "virtfn")
	vfIndex, _ := strconv.Atoi(vfIndexStr)

	if privileged {
		spoofChk = false
		trusted = true
	} else {
		spoofChk = true
		trusted = false
	}

	parentHandle, err := netlink.LinkByName(parentNetdev)
	if err != nil {
		return err
	}
	/* do not check for error status as older kernels doesn't
	 * have support for it.
	 */
	netlink.LinkSetVfTrust(parentHandle, vfIndex, trusted)
	netlink.LinkSetVfSpoofchk(parentHandle, vfIndex, spoofChk)
	return err
}

func SetPFLinkUp(parentNetdev string) error {
	var err error

	parentHandle, err1 := netlink.LinkByName(parentNetdev)
	if err1 != nil {
		log.Println("Fail to get parent handle: %v ", parentNetdev, err1)
		return fmt.Errorf("Fail to get link handle for %v: ", parentNetdev, err1)
	}
	netAttr := parentHandle.Attrs()
	if netAttr.OperState != netlink.OperUp {
		err = netlink.LinkSetUp(parentHandle)
	}
	return err
}

func IsSRIOVSupported(netdevName string) bool {

	maxvfs, err := netdevGetMaxVFCount(netdevName)
	if maxvfs == 0 || err != nil {
		return false
	} else {
		return true
	}
}

func FindVFDirForNetdev(pfNetdevName string, vfNetdevName string) (string, error) {

	virtFnDirs, err := GetVfPciDevList(pfNetdevName)
	if err != nil || len(virtFnDirs) == 0 {
		return "", fmt.Errorf("No vfs found for %v", vfNetdevName)
	}
	ndevSearchName := vfNetdevName + "__"

	for _, vfDir := range virtFnDirs {

		vfNetdevPath := filepath.Join(netSysDir, pfNetdevName,
			netDevPrefix, vfDir, "net")
		vfNetdevList, err := lsDirs(vfNetdevPath)
		if err != nil {
			continue
		}
		for _, vfName := range vfNetdevList {
			vfNamePrefixed := vfName + "__"
			if ndevSearchName == vfNamePrefixed {
				return vfDir, nil
			}
		}
	}
	return "", fmt.Errorf("device %s not found", vfNetdevName)
}
