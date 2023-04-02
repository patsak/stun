package stun

import (
	"net"

	"github.com/google/gopacket/layers"
)

type rawPacket []byte

func ipv4Dst(raw rawPacket) net.IP {
	return net.IP(raw[16 : 16+4])
}

func ipv4Proto(raw rawPacket) layers.IPProtocol {
	return layers.IPProtocol(raw[9])
}
