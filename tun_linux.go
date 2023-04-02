package stun

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/charmbracelet/log"
	"github.com/vishvananda/netlink"
)

const (
	cIFFTUN        = 0x0001
	cIFFTAP        = 0x0002
	cIFFNOPI       = 0x1000
	cIFFMULTIQUEUE = 0x0100
)

func InitTunDevice(n int) (ifce TunDevice, err error) {
	type ifReq struct {
		Name  [0x10]byte
		Flags uint16
		pad   [0x28 - 0x10 - 2]byte
	}

	var fd int
	if fd, err = syscall.Open(
		"/dev/net/tun", os.O_RDWR|syscall.O_NONBLOCK, 0); err != nil {
		return nil, err
	}

	var flags uint16 = cIFFTUN | cIFFMULTIQUEUE
	name := "tun" + strconv.Itoa(n)

	var req ifReq
	req.Flags = flags
	copy(req.Name[:], name)

	err = ioctl(uintptr(fd), syscall.TUNSETIFF, uintptr(unsafe.Pointer(&req)))
	if err != nil {
		return
	}

	name = strings.Trim(string(req.Name[:]), "\x00")

	return &tun{
		File: os.NewFile(uintptr(fd), name),
	}, nil
}

func configureServerTunnelDevice(device TunDevice, config ServerConfig) error {
	link, err := netlink.LinkByName(device.LinkName())
	if err != nil {
		return err
	}

	log.Debugf("get link %s", device.LinkName())
	if err := netlink.LinkSetUp(link); err != nil {
		return err
	}

	mtu := link.Attrs().MTU - tmsgMaxHeaderSize
	log.Debugf("set link mtu %d", mtu)
	if err := netlink.LinkSetMTU(link, mtu); err != nil {
		return err
	}

	ip, ipNet, err := net.ParseCIDR(config.NetworkCIDR)
	if err != nil {
		return err
	}

	ipNet.IP = ip

	log.Debugf("add address %s", ipNet)
	if err := netlink.AddrAdd(link, &netlink.Addr{
		IPNet: ipNet,
		Scope: int(netlink.SCOPE_LINK),
	}); err != nil {
		return err
	}

	log.Debugf("add p-p %s", ip)
	if err := netlink.RouteAdd(&netlink.Route{
		Dst: &net.IPNet{
			IP:   ip,
			Mask: net.IPv4Mask(255, 255, 255, 255),
		},
		LinkIndex: link.Attrs().Index,
		Scope:     netlink.SCOPE_LINK,
	}); err != nil {
		return err
	}

	return nil
}

func configureClientTunnelDevice(device TunDevice, config ClientConfig) error {
	link, err := netlink.LinkByName(device.LinkName())
	if err != nil {
		return err
	}

	log.Debugf("configure link %s", link.Attrs().Name)

	if err := netlink.LinkSetMTU(link, DeviceMTU); err != nil {
		return err
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return err
	}

	ip, mask, err := net.ParseCIDR(config.NetworkCIDR)
	if err != nil {
		return err
	}

	log.Debugf("set link address %s", mask)

	if err := netlink.AddrAdd(link, &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   ip,
			Mask: net.IPMask{255, 255, 255, 255},
		},
		Scope: int(netlink.SCOPE_UNIVERSE),
	}); err != nil {
		return err
	}

	return nil
}

func NotifyNetworkAddressesChanges(ctx context.Context) (<-chan any, error) {
	changes := make(chan netlink.AddrUpdate)

	if err := netlink.AddrSubscribe(changes, nil); err != nil {
		return nil, err
	}
	res := make(chan any)
	go func() {
		c := <-changes
		res <- c
	}()
	return res, nil
}
