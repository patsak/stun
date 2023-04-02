package stun

import (
	"encoding/binary"
	"os"
	"runtime"
	"syscall"

	"github.com/google/gopacket/layers"
)

const (
	tunFrameHeaderSize = 4
)

var tunFrameIPV4Header = func() []byte {
	var res [tunFrameHeaderSize]byte

	switch runtime.GOOS {
	case "darwin":
		res[3] = 2
	default:
		binary.BigEndian.PutUint16(res[2:], uint16(layers.EthernetTypeIPv4)) // set IPv4 protocol
	}
	return res[:]
}()

// https://docs.kernel.org/networking/tuntap.html#frame-format
func tunFrameEncode(payload []byte) []byte {
	res := make([]byte, tunFrameHeaderSize+len(payload))
	copy(res, tunFrameIPV4Header)
	copy(res[tunFrameHeaderSize:], payload)
	return res
}

func ioctl(fd uintptr, request uintptr, argp uintptr) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, argp)
	if errno != 0 {
		return os.NewSyscallError("ioctl", errno)
	}
	return nil
}
