package stun

import (
	"container/heap"
	"context"
	"encoding/csv"
	"io"
	"math"
	"net/netip"
	"os"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
	"github.com/miekg/dns"
	"golang.org/x/net/route"
)

type queue struct {
	domain []domainEntity
}

func (q *queue) Len() int {
	return len(q.domain)
}

func (q *queue) Less(i, j int) bool {
	return q.domain[i].ttl.Before(q.domain[j].ttl)
}

func (q *queue) Swap(i, j int) {
	q.domain[i], q.domain[j] = q.domain[j], q.domain[i]
}

func (q *queue) Push(x any) {
	q.domain = append(q.domain, x.(domainEntity))
}

func (q *queue) Pop() any {
	res := q.domain[len(q.domain)-1]
	q.domain = q.domain[:len(q.domain)-1]
	return res
}

type domainEntity struct {
	domain string
	ttl    time.Time
}

var _ heap.Interface = (*queue)(nil)

func KeepRoutesToDomains(ctx context.Context, tunDevice TunDevice, dnsServer string, domainsReader io.ReadCloser) error {
	csvReader := csv.NewReader(domainsReader)
	var domains []domainEntity
	for {
		row, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		domain := row[0]
		domains = append(domains, domainEntity{
			domain: domain,
		})
	}

	routefd, err := syscall.Socket(syscall.AF_ROUTE, syscall.SOCK_RAW, syscall.AF_UNSPEC)
	if err != nil {
		log.Warn("can't init route", err)
		return err
	}

	q := queue{domain: domains}
	c := dns.Client{
		Timeout: RetryDelay,
	}
	go func() {
		cnt := 0
		defer syscall.Close(routefd)

		for {
			select {
			case <-ctx.Done():
				break
			default:

			}
			cnt++
			domainEntity := heap.Pop(&q).(domainEntity)
			if !domainEntity.ttl.IsZero() {
				time.Sleep(domainEntity.ttl.Sub(time.Now()))
			}

			m := dns.Msg{}
			m.SetQuestion(domainEntity.domain+".", dns.TypeA)

			r, _, err := c.ExchangeContext(ctx, &m, dnsServer+":53")
			if err != nil {
				log.Warn("get dns record error", err)
				heap.Push(&q, domainEntity)
				continue
			}

			for _, ans := range r.Answer {
				arecord, ok := ans.(*dns.A)
				if !ok {
					continue
				}

				domainEntity.ttl = time.Now().Add(time.Second * time.Duration(arecord.Hdr.Ttl))

				netIP, _ := netip.AddrFromSlice(arecord.A)

				routeExists, err := isRouteExists(uintptr(routefd), tunDevice, netIP)
				if err != nil {
					log.Warn("check route to %s failed: %v", netIP, err)
					continue
				}

				if routeExists {
					log.Debugf("route to %s already exists", netIP)
					continue
				}

				log.Infof("add route to %s for %s and ttl %s", arecord.A, domainEntity.domain, (time.Second * time.Duration(arecord.Hdr.Ttl)).String())

				if err := addRoute(uintptr(routefd), tunDevice, netIP); err != nil {
					log.Warn("add route error", err)
					continue
				}
			}
			heap.Push(&q, domainEntity)
		}
	}()
	return nil
}

func isRouteExists(fd uintptr, device TunDevice, dst netip.Addr) (bool, error) {
	msg := route.RouteMessage{
		Type: syscall.RTM_GET,
		ID:   uintptr(os.Getpid()),
		Addrs: []route.Addr{
			syscall.RTAX_DST: &route.Inet4Addr{IP: dst.As4()},
		},
	}
	bts, err := msg.Marshal()
	if err != nil {
		return false, err
	}
	_, err = syscall.Write(int(fd), bts)
	if err != nil {
		return false, err
	}

	bts = make([]byte, math.MaxUint16)
	n, err := syscall.Read(int(fd), bts)
	if err != nil {
		return false, err
	}

	rawRouteMessage, err := route.ParseRIB(route.RIBTypeRoute, bts[:n])
	if err != nil {
		return false, err
	}

	if len(rawRouteMessage) == 0 {
		return false, nil
	}

	for _, m := range rawRouteMessage {
		routeMessage, ok := m.(*route.RouteMessage)
		if !ok {
			continue
		}

		linkAdd, ok := routeMessage.Addrs[syscall.RTAX_GATEWAY].(*route.LinkAddr)

		return ok && linkAdd.Name == device.LinkName(), nil
	}

	return false, nil

}

func addRoute(fd uintptr, device TunDevice, dst netip.Addr) error {
	msg := route.RouteMessage{
		Type: syscall.RTM_ADD,
		ID:   uintptr(os.Getpid()),
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
	_, err = syscall.Write(int(fd), bts)
	if err != nil {
		return err
	}

	// skip response
	_, err = syscall.Read(int(fd), make([]byte, math.MaxUint16))
	if err != nil {
		return err
	}

	return nil
}
