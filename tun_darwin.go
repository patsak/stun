package stun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"reflect"
	"syscall"
	"unsafe"

	"github.com/charmbracelet/log"
	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

const (
	MAX_KCTL_NAME     = 96
	UTUN_CONTROL_NAME = "com.apple.net.utun_control"
	AF_SYS_CONTROL    = 2
	SYSPROTO_EVENT    = 1
	UTUN_OPT_IFNAME   = 2

	IOCTL_W = 0x80000000
	IOCTL_R = 0x40000000

	KEV_VENDOR_APPLE  = 1
	KEV_NETWORK_CLASS = 1
	KEV_INET_SUBCLASS = 1
)

// bsd/sys/ioccom.h
func ioctlMacrosIO[T any](op int, subsystem byte, command int, dataType T) uintptr {
	const IOCPARM_MASK = 0x1fff
	s := int(unsafe.Sizeof(dataType))
	return uintptr(op | ((s & IOCPARM_MASK) << 16) | ((int)(subsystem) << 8) | command)
}

var (
	ioctlCTLIOCGINFO  = ioctlMacrosIO(IOCTL_W|IOCTL_R, 'N', 3, ctl_info{})
	ioctlSIOCSKEVFILT = ioctlMacrosIO(IOCTL_W, 'e', 2, kev_request{})
)

type ifreq_addr struct {
	name    [16]byte
	in_addr unix.RawSockaddrInet4
}

type ifreq_flags struct {
	name  [16]byte
	flags uint16
}

type ifreq_int struct {
	name  [16]byte
	value int32
}

/*
ctl_info copy from bsd/sys/kern_control.h
Controls destined to the Controller Manager.
*/
type ctl_info struct {
	ctl_id   uint32              // The kernel control id, filled out upon return.
	ctl_name [MAX_KCTL_NAME]byte // The kernel control name to find
}

// bsd/sys/kern_event.h
type kev_request struct {
	vendor_code  uint32
	kev_class    uint32
	kev_subclass uint32
}

func InitTunDevice(tunNumber int) (TunDevice, error) {
	fd, err := syscall.Socket(syscall.AF_SYSTEM, syscall.SOCK_DGRAM, AF_SYS_CONTROL)
	if err != nil {
		return nil, err
	}

	ctl := &ctl_info{}
	copy(ctl.ctl_name[:], UTUN_CONTROL_NAME)

	if err := ioctl(uintptr(fd), ioctlCTLIOCGINFO, uintptr(unsafe.Pointer(ctl))); err != nil {
		return nil, err
	}

	type sockaddr_ctl struct {
		len      uint8
		family   uint8
		addr     uint16
		id       uint32
		unit     uint32
		reserved [5]uint32
	}

	sockAddr := unsafe.Pointer(&sockaddr_ctl{
		len:    uint8(unsafe.Sizeof(sockaddr_ctl{})),
		family: syscall.AF_SYSTEM,
		addr:   AF_SYS_CONTROL,
		id:     ctl.ctl_id,
		unit:   uint32(tunNumber),
	})

	if _, _, err := syscall.Syscall(syscall.SYS_CONNECT, uintptr(fd), uintptr(sockAddr), unsafe.Sizeof(sockaddr_ctl{})); err > 0 {
		return nil, err
	}

	var name [16]byte
	ifNameSize := len(name)

	if _, _, err := syscall.Syscall6(
		syscall.SYS_GETSOCKOPT,
		uintptr(fd),
		uintptr(AF_SYS_CONTROL),
		uintptr(UTUN_OPT_IFNAME),
		uintptr(unsafe.Pointer(&name)),
		uintptr(unsafe.Pointer(&ifNameSize)), 0); err > 0 {

		return nil, err
	}

	if err := syscall.SetNonblock(fd, true); err != nil {
		return nil, err
	}

	f := os.NewFile(uintptr(fd), string(bytes.Trim(name[:], "\x00")))

	log.Infof("tunnel device %s is ready", string(name[:]))

	return tun{f}, nil
}

func NotifyNetworkAddressesChanges(ctx context.Context) (<-chan any, error) {
	// bsd/sys/kern_event.h
	type kern_event_msg struct {
		total_size   uint32
		vendor_code  uint32
		kev_class    uint32
		kev_subclass uint32
		id           uint32
		event_code   uint32
		event_data   uint32
	}

	fd, err := syscall.Socket(syscall.AF_SYSTEM, syscall.SOCK_RAW, SYSPROTO_EVENT)
	if err != nil {
		return nil, err
	}
	req := kev_request{
		vendor_code:  KEV_VENDOR_APPLE,
		kev_class:    KEV_NETWORK_CLASS,
		kev_subclass: KEV_INET_SUBCLASS,
	}

	if err := ioctl(uintptr(fd), ioctlSIOCSKEVFILT, uintptr(unsafe.Pointer(&req))); err != nil {
		return nil, err
	}

	netEvents := make(chan any)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:

			}
			buf := make([]byte, 1024)

			_, err := unix.Read(fd, buf)
			if err != nil {
				log.Warn("can't read network event", "error", err)
				continue
			}

			hdr := (*reflect.SliceHeader)(unsafe.Pointer(&buf))
			msg := (*kern_event_msg)(unsafe.Pointer(hdr.Data))

			log.Debug("receive event", "id", msg.id)

			netEvents <- struct{}{}
		}
	}()

	ioEvents, cancelSubscription := SubscribeSystemEvents()
	out := make(chan any)
	go func() {
		defer cancelSubscription()
		for {
			select {
			case <-ctx.Done():
				return
			case <-netEvents:
				out <- struct{}{}
			case v := <-ioEvents:
				if v == SystemEventWakeUp {
					out <- struct{}{}
				}
			}
		}
	}()

	return out, nil

}

func configureClientTunnelDevice(device TunDevice, config ClientConfig) error {
	sockfd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, syscall.AF_UNSPEC)
	if err != nil {
		return err
	}
	addr, mask, err := net.ParseCIDR(config.NetworkCIDR)
	if err != nil {
		return err
	}

	netAddr, ok := netip.AddrFromSlice(addr)
	if !ok {
		return errors.New(fmt.Sprintf("can't get ip address from %s", addr))
	}

	// set mtu
	log.Debugf("device %s set mtu", device.LinkName())
	mtu := &ifreq_int{}
	copy(mtu.name[:], device.LinkName())
	mtu.value = DeviceMTU
	if err := ioctl(uintptr(sockfd), syscall.SIOCSIFMTU, uintptr(unsafe.Pointer(mtu))); err != nil {
		return err
	}

	// interface up
	log.Debugf("device %s up", device.LinkName())
	fl := &ifreq_flags{}
	copy(fl.name[:], device.LinkName())
	fl.flags = syscall.IFF_UP

	if err := ioctl(uintptr(sockfd), syscall.SIOCSIFFLAGS, uintptr(unsafe.Pointer(fl))); err != nil {
		return err
	}

	log.Debugf("set address %s", addr)
	in := ifreq_addr{}
	copy(in.name[:], device.LinkName())
	in.in_addr.Family = unix.AF_INET
	copy((&in.in_addr).Addr[:], addr.To4())
	if err := ioctl(uintptr(sockfd), syscall.SIOCSIFADDR, uintptr(unsafe.Pointer(&in))); err != nil {
		return err
	}

	log.Debugf("set network mask /32")
	in = ifreq_addr{}
	copy(in.name[:], device.LinkName())
	in.in_addr.Family = unix.AF_INET
	in.in_addr.Addr = [4]byte{255, 255, 255, 255}
	if err := ioctl(uintptr(sockfd), syscall.SIOCSIFNETMASK, uintptr(unsafe.Pointer(&in))); err != nil {
		return err
	}

	log.Debugf("set destination address %s", addr)
	in = ifreq_addr{}
	copy(in.name[:], device.LinkName())
	in.in_addr.Family = unix.AF_INET
	copy((&in.in_addr).Addr[:], addr.To4())
	if err := ioctl(uintptr(sockfd), syscall.SIOCSIFDSTADDR, uintptr(unsafe.Pointer(&in))); err != nil {
		return err
	}

	// add route to vpn network
	routefd, err := syscall.Socket(syscall.AF_ROUTE, syscall.SOCK_RAW, syscall.AF_UNSPEC)
	if err != nil {
		return err
	}
	defer syscall.Close(routefd)

	var maskb [4]byte
	copy(maskb[:], mask.Mask)

	log.Debugf("add route to %s via %s", netAddr.Unmap(), device.LinkName())
	msg := route.RouteMessage{
		Type: syscall.RTM_ADD,
		Seq:  1,
		ID:   uintptr(os.Getpid()),
		Addrs: []route.Addr{
			syscall.RTAX_DST:     &route.Inet4Addr{IP: netAddr.As4()},
			syscall.RTAX_NETMASK: &route.Inet4Addr{IP: maskb},
			syscall.RTAX_GATEWAY: &route.LinkAddr{Name: device.LinkName()},
		},
	}

	bts, err := msg.Marshal()
	if err != nil {
		return err
	}
	_, err = syscall.Write(routefd, bts)
	if err != nil {
		return err
	}
	return nil
}

func configureServerTunnelDevice(_ TunDevice, _ ServerConfig) error {
	panic(errors.New("server mode not allowed for darwin"))
}
