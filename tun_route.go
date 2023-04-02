package stun

import (
	"encoding/csv"
	"io"
	"net"
	"net/netip"
	"os"
	"syscall"

	"github.com/charmbracelet/log"
	"golang.org/x/net/route"
)

func LoadRoutes(tunDevice TunDevice, domains io.Reader) error {
	routefd, err := syscall.Socket(syscall.AF_ROUTE, syscall.SOCK_RAW, syscall.AF_UNSPEC)
	if err != nil {
		return err
	}
	defer syscall.Close(routefd)

	csvReader := csv.NewReader(domains)

	for {
		row, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		domain := row[0]
		ips, err := net.LookupIP(domain)
		if err != nil {
			return err
		}

		for _, ip := range ips {
			if len(ip.To4()) != net.IPv4len {
				continue
			}
			netIP, _ := netip.AddrFromSlice(ip.To4())

			log.Infof("add route to %s", ip)
			msg := route.RouteMessage{
				Type: syscall.RTM_ADD,
				Seq:  1,
				ID:   uintptr(os.Getpid()),
				Addrs: []route.Addr{
					syscall.RTAX_DST:     &route.Inet4Addr{IP: netIP.As4()},
					syscall.RTAX_NETMASK: &route.Inet4Addr{IP: [4]byte{255, 255, 255, 255}},
					syscall.RTAX_GATEWAY: &route.LinkAddr{Name: tunDevice.LinkName()},
				},
			}

			bts, err := msg.Marshal()
			if err != nil {
				return err
			}
			_, err = syscall.Write(routefd, bts)
			if err != nil {
				log.Warn("can't init route", err)
			}
		}
	}

	return nil
}
