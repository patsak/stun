package stun

import (
	"container/heap"
	"context"
	"encoding/csv"
	"io"
	"math"
	"math/rand"
	"net/netip"
	"time"

	"github.com/charmbracelet/log"
	"github.com/miekg/dns"
	"github.com/patsak/stun/dnsconfig"
)

type queue struct {
	domain []domainEntity
}

func (q *queue) Len() int {
	return len(q.domain)
}

func (q *queue) Less(i, j int) bool {
	return q.domain[i].updateTime.Before(q.domain[j].updateTime)
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
	domain     string
	updateTime time.Time
	ttl        time.Duration
	retry      int
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

	router, err := newRouter()
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
		defer router.close()

		for {
			select {
			case <-ctx.Done():
				break
			default:

			}
			cnt++
			domainEntity := heap.Pop(&q).(domainEntity)
			if !domainEntity.updateTime.IsZero() {
				time.Sleep(domainEntity.updateTime.Sub(time.Now()))
			}

			m := dns.Msg{}
			m.SetQuestion(domainEntity.domain+".", dns.TypeA)
			cfg := dnsconfig.LoadConfig()
			r, _, err := c.ExchangeContext(ctx, &m, cfg.Servers[0])
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

				domainEntity.ttl = time.Duration(rand.Intn(10)) * time.Second
				domainEntity.updateTime = time.Now().Add(domainEntity.ttl)

				netIP, _ := netip.AddrFromSlice(arecord.A)

				routeExists, err := router.isRouteExists(tunDevice, netIP)
				if err != nil {
					log.Warn("check route to %s failed: %v", netIP, err)
					continue
				}

				if routeExists {
					domainEntity.retry += 1
					newTTL := time.Duration(domainEntity.retry) * time.Second * time.Duration(arecord.Hdr.Ttl)
					newTTL = time.Duration(math.Min(float64(newTTL), float64(time.Hour*24)))

					domainEntity.updateTime = time.Now().Add(newTTL)
					domainEntity.ttl = newTTL
					log.Debugf("route to %s already exists", netIP)
					continue
				}

				domainEntity.retry = 0
				log.Infof("add route to %s for %s and ttl %s", arecord.A, domainEntity.domain, domainEntity.ttl)

				if err := router.addRoute(tunDevice, netIP); err != nil {
					log.Warn("add route", "error", err)
					continue
				}
			}
			heap.Push(&q, domainEntity)
		}
	}()
	return nil
}
