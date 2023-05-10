package stun

import (
	"net"
	"net/netip"

	"github.com/vishvananda/netlink"
)

type routes struct {
}

func newRouter() (*routes, error) {

	return &routes{}, nil
}

func (r *routes) close() {}

func (r *routes) isRouteExists(device TunDevice, dst netip.Addr) (bool, error) {
	dstRoutes, err := netlink.RouteGet(dst.AsSlice())
	if err != nil {
		return false, err
	}
	link, err := netlink.LinkByName(device.LinkName())
	if err != nil {
		return false, err
	}

	for _, rr := range dstRoutes {
		if rr.LinkIndex == link.Attrs().Index {
			return true, nil
		}
	}

	return false, nil
}

func (r *routes) addRoute(device TunDevice, dst netip.Addr) error {
	addr := device.LookupDeviceInfo()
	return netlink.RouteAdd(&netlink.Route{
		Dst: &net.IPNet{
			IP:   dst.AsSlice(),
			Mask: net.IPv4Mask(255, 255, 255, 255),
		},
		Gw: addr.Addr.AsSlice(),
	})
}
