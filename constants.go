package stun

import "time"

const (
	KeepAliveMaxDuration           = 40 * time.Second
	KeepAliveRequestDuration       = KeepAliveMaxDuration - 10*time.Second
	RetryDelay                     = 2 * time.Second
	HandshakeDelay                 = 5 * time.Second
	DeviceMTU                int32 = 1280
	DeviceBufferSize               = tmsgMaxHeaderSize + int(DeviceMTU)
)
