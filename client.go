package stun

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"golang.org/x/net/context"
)

type client struct {
	mu         sync.RWMutex
	conn       *net.UDPConn
	device     Device
	ackChannel chan struct{}
}

func RunClient(ctx context.Context, tun TunDevice, config ClientConfig) error {
	if err := configureClientTunnelDevice(tun, config); err != nil {
		return err
	}

	conn, err := newClientConnection(ctx, tun, config)
	if err != nil {
		return err
	}

	tunDeviceCh := make(chan []byte, 1)

	go conn.readDevicePackets(ctx, tun, tunDeviceCh)

	go conn.processPacketsFromDevice(ctx, tunDeviceCh)

	go conn.processPacketsFromConnection(ctx, tun)

	return nil
}

func newClientConnection(ctx context.Context, tun TunDevice, config ClientConfig) (*client, error) {
	networkChanges, err := NotifyNetworkAddressesChanges(ctx)
	if err != nil {
		return nil, err
	}

	udpConnection, err := dial(config)
	if err != nil {
		return nil, err
	}

	c := &client{
		conn:       udpConnection,
		device:     tun.LookupDeviceInfo(),
		ackChannel: make(chan struct{}, 1),
	}

	if err := c.handshake(tun, config); err != nil {
		log.Error("error on handshake", err)
		return nil, err
	}

	go func() {
		var retry <-chan time.Time

		forceReconnect := time.NewTicker(KeepAliveMaxDuration)
		defer forceReconnect.Stop()
		keepAlive := time.NewTicker(KeepAliveRequestDuration)
		defer keepAlive.Stop()
		for {
			select {
			case <-ctx.Done():
				if c := c.get(); c != nil {
					c.Close()
				}
				return

			case <-keepAlive.C:
				if err := c.keepAlive(); err != nil {
					log.Warn("error on keep alive", err)
					retry = time.Tick(RetryDelay)
				}
				continue
			case <-c.ackChannel:
				forceReconnect.Reset(KeepAliveMaxDuration)
				keepAlive.Reset(KeepAliveRequestDuration)
				continue
			case <-retry:
			case <-networkChanges:
			case <-forceReconnect.C:
			}

			if err := c.handshake(tun, config); err != nil {
				log.Error("error on handshake", err)
				retry = time.After(RetryDelay)
			}
		}
	}()

	return c, nil
}

func (c *client) handshake(tun TunDevice, config ClientConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		c.conn.Close()
	}

	cc, err := dial(config)
	if err != nil {
		return err
	}

	c.device = tun.LookupDeviceInfo()
	request := tmsg{
		tp:   msgTypeConnect,
		addr: c.device.Addr,
	}

	bts, err := request.MarshalBinary()
	if err != nil {
		return err
	}
	if _, err := cc.Write(bts); err != nil {
		return err
	}

	var response tmsg
	bts = make([]byte, c.bufSize())

	// handshake timeout
	if err := cc.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}

	n, err := cc.Read(bts)
	if err != nil {
		return err
	}

	if err := response.UnmarshalBinary(bts[:n:n]); err != nil {
		return err
	}

	if response.tp != msgTypeAck {
		return errors.New(fmt.Sprintf("unexpected message type %d instead %d in handshake.", response.tp, msgTypeAck))
	}

	if err := cc.SetReadDeadline(time.Time{}); err != nil {
		return err
	}

	c.conn = cc

	log.Infof("connection to %s established", config.ServerInternetAddress)

	return nil
}

func (c *client) keepAlive() error {
	log.Debugf("send keep alive message")

	request := tmsg{
		tp:   msgTypeKeepAlive,
		addr: c.device.Addr,
	}

	bts, err := request.MarshalBinary()
	if err != nil {
		return err
	}

	conn := c.get()

	if _, err := conn.Write(bts); err != nil {
		return err
	}

	return nil
}

func (c *client) get() *net.UDPConn {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn
}

func (c *client) set(cc *net.UDPConn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn = cc
}

func (c *client) processPacketsFromConnection(ctx context.Context, tun TunDevice) {
	for {
		select {
		case <-ctx.Done():
			return
		default:

		}
		buf := make([]byte, c.bufSize())
		n, addr, err := c.get().ReadFromUDP(buf)
		if errors.Is(err, net.ErrClosed) {
			continue
		}
		if err != nil {
			log.Warn("read socket", "error", err)
			continue
		}

		log.Debugf("receive packet from %s", addr.IP)

		buf = buf[:n:n]
		var msg tmsg
		if err = msg.UnmarshalBinary(buf); err != nil {
			log.Warn("deserialize device packet error", err)
			continue
		}

		if msg.tp == msgTypeAck {
			c.ackChannel <- struct{}{}
			continue
		}

		if _, err := tun.Write(tunFrameEncode(msg.payload)); err != nil {
			log.Warn("write to device", "error", err)
			continue
		}
	}
}

func (c *client) readDevicePackets(ctx context.Context, tun TunDevice, tunDeviceCh chan<- []byte) {
	for {
		select {
		case <-ctx.Done():
			return
		default:

		}
		buf := make([]byte, c.bufSize())
		n, err := tun.Read(buf)
		if err != nil {
			log.Warn("read device", "error", err)
			continue
		}

		tunDeviceCh <- buf[tunFrameHeaderSize:n:n]
	}
}

func (c *client) processPacketsFromDevice(ctx context.Context, tunDeviceCh <-chan []byte) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		buf, ok := <-tunDeviceCh
		if !ok {
			break
		}

		log.Debugf("send packet to %s", ipv4Dst(buf))

		msg := tmsg{
			tp:      msgTypeData,
			addr:    c.device.Addr,
			payload: buf,
		}

		outBytes, err := msg.MarshalBinary()
		if err != nil {
			log.Warn("client marshal data", "error", err)
			continue
		}

		if _, err := c.get().Write(outBytes); err != nil {
			log.Warn("client write", "error", err)
			continue
		}
	}
}

func (c *client) bufSize() int {
	return c.device.MTU + tunFrameHeaderSize
}

func dial(config ClientConfig) (*net.UDPConn, error) {
	return net.DialUDP("udp",
		net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.MustParseAddr("0.0.0.0"), uint16(config.ClientPort))),
		net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.MustParseAddr(config.ServerInternetAddress), uint16(config.ServerPort))))
}
