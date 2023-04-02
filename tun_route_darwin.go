package stun

import (
	"math"
	"net/netip"
	"os"
	"sync/atomic"
	"syscall"

	"golang.org/x/net/route"
)

type routes struct {
	fd  int
	seq int32
}

func newRouter() (*routes, error) {
	routefd, err := syscall.Socket(syscall.AF_ROUTE, syscall.SOCK_RAW, syscall.AF_UNSPEC)
	if err != nil {
		return nil, err
	}

	return &routes{
		fd: routefd,
	}, nil
}

func (r *routes) close() {
	syscall.Close(r.fd)
}

func (r *routes) isRouteExists(device TunDevice, dst netip.Addr) (bool, error) {
	msg := route.RouteMessage{
		Type: syscall.RTM_GET,
		ID:   uintptr(os.Getpid()),
		Seq:  int(atomic.AddInt32(&r.seq, 1)),
		Addrs: []route.Addr{
			syscall.RTAX_DST:     &route.Inet4Addr{IP: dst.As4()},
			syscall.RTAX_NETMASK: &route.Inet4Addr{IP: [4]byte{255, 255, 255, 255}},
		},
	}
	bts, err := msg.Marshal()
	if err != nil {
		return false, err
	}
	_, err = syscall.Write(r.fd, bts)
	if err != nil {
		return false, err
	}

	bts = make([]byte, math.MaxUint16)

	for {
		n, err := syscall.Read(r.fd, bts)
		if err != nil {
			return false, err
		}

		rawRouteMessage, err := route.ParseRIB(route.RIBTypeRoute, bts[:n])
		if err != nil {
			return false, err
		}

		var responseNotMatch bool
		for _, m := range rawRouteMessage {
			switch v := m.(type) {
			case *route.RouteMessage:
				responseNotMatch = responseNotMatch || (v.Seq != msg.Seq || v.ID != msg.ID || v.Type != msg.Type)
			default:
				responseNotMatch = true
			}
		}

		if responseNotMatch {
			continue
		}

		var routeFound bool
		for _, m := range rawRouteMessage {
			var addrs []route.Addr
			switch v := m.(type) {
			case *route.InterfaceMulticastAddrMessage:
				addrs = v.Addrs
			case *route.RouteMessage:
				addrs = v.Addrs
			default:
				continue
			}

			linkAdd, ok := addrs[syscall.RTAX_GATEWAY].(*route.LinkAddr)
			if ok && linkAdd.Name == device.LinkName() {
				routeFound = true
				break
			}
		}
		return routeFound, nil
	}

}

func (r *routes) addRoute(device TunDevice, dst netip.Addr) error {
	msg := route.RouteMessage{
		Type: syscall.RTM_ADD,
		ID:   uintptr(os.Getpid()),
		Seq:  int(atomic.AddInt32(&r.seq, 1)),
		Addrs: []route.Addr{
			syscall.RTAX_DST:     &route.Inet4Addr{IP: dst.As4()},
			syscall.RTAX_NETMASK: &route.Inet4Addr{IP: [4]byte{255, 255, 255, 255}},
			syscall.RTAX_GATEWAY: &route.LinkAddr{Name: device.LinkName()},
		},
	}
	bts, err := msg.Marshal()
	if err != nil {
		return err
	}
	var retErr error
	_, err = syscall.Write(r.fd, bts)
	if err != nil {
		retErr = err
	}

	// skip response
	_, err = syscall.Read(r.fd, make([]byte, math.MaxUint16))
	if err != nil {
		return err
	}

	return retErr
}
