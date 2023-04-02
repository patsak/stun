package stun

import (
	"context"
	"net"
	"net/netip"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/jellydator/ttlcache/v3"
)

const (
	KeepAliveMaxDuration     = 40 * time.Second
	KeepAliveRequestDuration = KeepAliveMaxDuration - 10*time.Second
	RetryDelay               = 2 * time.Second
)

type server struct {
	conn               *net.UDPConn
	tun                TunDevice
	network            atomic.Pointer[net.IPNet]
	config             ServerConfig
	knownLocalPeers    *ttlcache.Cache[netip.Addr, peer]
	knownInetAddresses *ttlcache.Cache[netip.Addr, peer]
}

type peer struct {
	peerAddress netip.Addr
	inetAddress netip.AddrPort
}

func RunServer(ctx context.Context, tun TunDevice, config ServerConfig) error {
	var err error
	err = configureServerTunnelDevice(tun, config)
	if err != nil {
		return err
	}

	peersByLocalAddress := ttlcache.New[netip.Addr, peer]()
	peersByInetAddress := ttlcache.New[netip.Addr, peer]()

	addressChanges, err := NotifyNetworkAddressesChanges(ctx)
	if err != nil {
		return err
	}

	var conn *net.UDPConn
	conn, err = net.ListenUDP("udp",
		net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.MustParseAddr("0.0.0.0"), uint16(config.ServerPort))))
	if err != nil {
		return err
	}

	addr := tun.LookupDeviceInfo()

	srv := server{
		conn:               conn,
		tun:                tun,
		knownLocalPeers:    peersByLocalAddress,
		knownInetAddresses: peersByInetAddress,
	}

	srv.network.Store(&net.IPNet{
		IP:   addr.Addr.AsSlice(),
		Mask: addr.Mask,
	})

	go func() {
		for range addressChanges {
			addr := tun.LookupDeviceInfo()
			srv.network.Store(&net.IPNet{
				IP:   addr.Addr.AsSlice(),
				Mask: addr.Mask,
			})
		}
	}()

	go func() {
		for b := range srv.readConnLoop(ctx, runtime.NumCPU()) {
			go srv.receiveClientPacket(b.buf, b.netAddr)
		}
	}()

	go func() {
		for b := range srv.readTunLoop(ctx, runtime.NumCPU()) {
			go srv.receiveDevicePacket(b)
		}
	}()

	return nil
}

func (s *server) send(payload []byte, dst net.IP) error {
	switch {
	case s.isOwn(dst):
		return s.handleSelf(payload)
	case s.inNetwork(dst):
		ip, _ := netip.AddrFromSlice(dst)
		p := s.knownLocalPeers.Get(ip.Unmap())
		if p == nil {
			if err := s.handleUnknownHost(payload); err != nil {
				return err
			}
			return nil
		}
		bts, err := tmsg{
			tp:      msgTypeData,
			addr:    p.Value().peerAddress,
			payload: payload,
		}.MarshalBinary()
		if err != nil {
			return err
		}
		log.Debugf("send data to %s", p.Value().inetAddress)
		_, err = s.conn.WriteToUDPAddrPort(bts, p.Value().inetAddress)
		return err
	default:
		log.Debugf("write data in device to %s", dst)
		_, err := s.tun.Write(tunFrameEncode(payload))
		return err
	}
}

type connReadResult struct {
	netAddr netip.AddrPort
	buf     []byte
}

func (s *server) readConnLoop(ctx context.Context, nQueue int) <-chan connReadResult {
	res := make(chan connReadResult, nQueue)

	go func() {
		log.Infof("start listen connections")

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			buf := make([]byte, 1504)
			var n int
			var netAddr netip.AddrPort
			n, netAddr, err := s.conn.ReadFromUDPAddrPort(buf)
			if err != nil {
				log.Warn("read error", err)
				continue
			}
			buf = buf[:n:n]

			select {
			case res <- connReadResult{
				buf:     buf,
				netAddr: netAddr,
			}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return res
}

func (s *server) readTunLoop(ctx context.Context, nQueue int) <-chan []byte {
	res := make(chan []byte, nQueue)
	go func() {
		log.Infof("start device read loop")
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			buf := make([]byte, 1504)
			n, err := s.tun.Read(buf)
			if err != nil {
				log.Printf("read error %v", err)
				continue
			}
			buf = buf[tunFrameHeaderSize:n:n]

			select {
			case res <- buf:
			case <-ctx.Done():
				return
			}
		}
	}()
	return res
}

type packet struct {
	ip      layers.IPv4
	icmp    layers.ICMPv4
	payload gopacket.Payload
}

func (s *server) decode(buf []byte) (packet, error) {
	var packet packet
	parser := gopacket.NewDecodingLayerParser(layers.LayerTypeIPv4, &packet.ip, &packet.icmp, &packet.payload)
	parser.IgnoreUnsupported = true
	decodedLayers := make([]gopacket.LayerType, 0, 3)
	if err := parser.DecodeLayers(buf, &decodedLayers); err != nil {
		return packet, err
	}

	return packet, nil
}

func (s *server) receiveDevicePacket(buf []byte) {
	dstIP := ipv4Dst(buf)
	log.Debugf("receive %d bytes to %s", len(buf), dstIP)
	if err := s.send(buf, dstIP); err != nil {
		log.Warn("can't route payload with error %s", err)
	}
}

func (s *server) receiveClientPacket(buf []byte, netAddr netip.AddrPort) {
	log.Debugf("read packet (%d size)", len(buf))

	proto := tmsg{}
	if err := proto.UnmarshalBinary(buf); err != nil {
		log.Warnf("can't unmarshal %s", err)
		return
	}

	switch proto.tp {
	case msgTypeConnect:
		if proto.addr.IsUnspecified() {
			log.Debugf("drop connect from with empty ip peer and net address %s", netAddr)
			return
		}

		ipNet := *s.network.Load()

		if !ipNet.Contains(proto.addr.AsSlice()) {
			log.Debugf("drop connect from with unexpected network in address %s. Only %s allowed", proto.addr, ipNet)
			return
		}

		p := peer{
			peerAddress: proto.addr,
			inetAddress: netAddr,
		}

		bts, err := tmsg{tp: msgTypeAck}.MarshalBinary()
		if err != nil {
			log.Warn("can't marshal ack response", err)
			return
		}

		if _, err := s.conn.WriteToUDPAddrPort(bts, netAddr); err != nil {
			log.Warn("send handshake response error", err)
			return
		}

		s.knownLocalPeers.Set(p.peerAddress, p, KeepAliveMaxDuration)
		s.knownInetAddresses.Set(p.inetAddress.Addr(), p, KeepAliveMaxDuration)

		log.Infof("connect peer %s, inet address %s", proto.addr, netAddr)
	case msgTypeKeepAlive:
		log.Debugf("keep alive")
		p := peer{
			peerAddress: proto.addr,
			inetAddress: netAddr,
		}
		if s.knownInetAddresses.Get(netAddr.Addr().Unmap()) == nil {
			return
		}
		s.knownLocalPeers.Touch(p.peerAddress)
		s.knownInetAddresses.Touch(p.inetAddress.Addr().Unmap())

		bts, err := tmsg{tp: msgTypeAck}.MarshalBinary()
		if err != nil {
			log.Warn("can't marshal ack response", err)
			return
		}

		_, err = s.conn.WriteToUDPAddrPort(bts, netAddr)
		if err != nil {
			log.Warnf("can't write request")
			return
		}

	default:
		if proto.addr.IsUnspecified() {
			log.Warnf("empty address in packet from %s", netAddr)
			return
		}
		if err := s.send(proto.payload, ipv4Dst(proto.payload)); err != nil {
			log.Warn("write error", err)
			return
		}
	}

}

func (s *server) gopacketOptions() gopacket.SerializeOptions {
	return gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}
}

func (s *server) inNetwork(ip net.IP) bool {
	return s.network.Load().Contains(ip)
}

func (s *server) isOwn(ip net.IP) bool {
	return s.network.Load().IP.Equal(ip)
}

func (s *server) handleUnknownHost(buf []byte) error {
	input, err := s.decode(buf)
	if err != nil {
		return err
	}

	addr := *s.network.Load()

	ipvh := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		DstIP:    input.ip.SrcIP,
		SrcIP:    addr.IP,
	}

	icmp := &layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeDestinationUnreachable, layers.ICMPv4CodeHost),
		Id:       input.icmp.Id,
		Seq:      input.icmp.Seq,
	}

	sbuf := gopacket.NewSerializeBuffer()
	err = gopacket.SerializeLayers(sbuf, s.gopacketOptions(), &ipvh, icmp, input.payload)
	if err != nil {
		return err
	}

	return s.send(sbuf.Bytes(), input.ip.SrcIP)
}

func (s *server) handleSelf(raw []byte) error {
	if ipv4Proto(raw) != layers.IPProtocolICMPv4 {
		_, err := s.tun.Write(raw)
		if err != nil {
			return err
		}
	}

	originalPacket, err := s.decode(raw)
	if err != nil {
		return err
	}

	if originalPacket.icmp.TypeCode.Type() != layers.ICMPv4TypeEchoRequest {
		return nil
	}

	addr := *s.network.Load()

	ip := layers.IPv4{
		Version:  4,
		TTL:      originalPacket.ip.TTL - 1,
		Protocol: layers.IPProtocolICMPv4,
		DstIP:    originalPacket.ip.SrcIP,
		SrcIP:    addr.IP,
	}
	icmp := &layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoReply, layers.ICMPv4CodeNet),
		Id:       originalPacket.icmp.Id,
		Seq:      originalPacket.icmp.Seq,
	}

	buf := gopacket.NewSerializeBuffer()
	err = gopacket.SerializeLayers(buf, s.gopacketOptions(), &ip, icmp, originalPacket.payload)
	if err != nil {
		return err
	}

	return s.send(buf.Bytes(), ip.DstIP)
}
