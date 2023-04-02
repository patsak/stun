//go:build linux || freebsd || darwin
// +build linux freebsd darwin

package stun

import (
	"io"
	"net"
	"net/netip"
	"os"
)

type TunDevice interface {
	io.Reader
	io.Writer
	io.Closer
	LookupDeviceInfo() Device
	LinkName() string
}

type Device struct {
	Addr netip.Addr
	Mask net.IPMask
	MTU  int
	FD   uintptr
}

var _ TunDevice = (*tun)(nil)

type tun struct {
	*os.File
}

func (t tun) LookupDeviceInfo() Device {
	in, err := net.InterfaceByName(t.Name())
	if err != nil {
		panic(err)
	}
	addrs, err := in.Addrs()
	if err != nil {
		panic(err)
	}

	var res Device
	for _, a := range addrs {
		ipaddr, ok := a.(*net.IPNet)
		if ok && len(ipaddr.IP.To4()) == net.IPv4len {
			res.Addr, _ = netip.AddrFromSlice(ipaddr.IP)
			res.Addr = res.Addr.Unmap()
			res.Mask = ipaddr.Mask
			res.MTU = in.MTU
		}
	}
	res.FD = t.File.Fd()
	return res
}

func (t tun) LinkName() string {
	return t.File.Name()
}
