package stun

import (
	"crypto/aes"
	"errors"
	"fmt"
	"net"
	"net/netip"
)

type msgType byte

const (
	msgTypeConnect   msgType = 0
	msgTypeData      msgType = 1
	msgTypeAck       msgType = 2
	msgTypeKeepAlive msgType = 3
)

const tmsgMaxHeaderSize = 1 /*cmd*/ + net.IPv6len /* max ip size */

type tmsg struct {
	tp      msgType
	addr    netip.Addr
	payload []byte
}

var b, _ = aes.NewCipher([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})

func (t tmsg) MarshalBinary() ([]byte, error) {
	const byteSize = 8
	l := len(t.payload)
	if !t.addr.IsValid() {
		t.addr = netip.IPv4Unspecified()
	}
	res := make([]byte, 0, l+t.addr.BitLen()/byteSize+1+1)

	res = append(res, byte(t.tp))

	res = append(res, byte(t.addr.BitLen()/byteSize))
	addr, err := t.addr.MarshalBinary()
	if err != nil {
		return nil, err
	}

	res = append(res, addr...)
	res = append(res, t.payload...)

	return res, nil
}

func (t *tmsg) UnmarshalBinary(bts []byte) error {
	const minHeaderSize = 6
	if len(bts) < minHeaderSize {
		return errors.New(fmt.Sprintf("slice length %d less than minimum size %d", len(bts), minHeaderSize))
	}
	t.tp = msgType(bts[0])
	ipLen := bts[1]
	if 2+int(ipLen) > len(bts) {
		return errors.New(fmt.Sprintf("ip length %d greater than input slice length %d", ipLen, len(bts)))
	}

	ip, ok := netip.AddrFromSlice(bts[2 : 2+ipLen])
	if !ok {
		return errors.New(fmt.Sprintf("can't parse bytes as ip address %+v", bts[2:2+bts[1]]))
	}

	t.addr = ip
	t.payload = bts[2+ipLen:]
	return nil
}
