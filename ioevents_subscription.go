package stun

// #cgo LDFLAGS: -framework CoreFoundation -framework IOKit
// int CanSleep();
// void WillWake();
// void WillSleep();
// #include "ioevents.h"
import "C"

import (
	"sync"
	"sync/atomic"
)

var ioEventBus = struct {
	subscribers []subscription
	mu          sync.RWMutex
}{}

type subscription struct {
	c  chan SystemEvent
	id uint64
}

var cnt uint64

func init() {
	go func() {
		C.registerNotifications()
	}()
	go func() {
		for {
			ev := <-systemEvents
			ioEventBus.mu.RLock()
			for _, s := range ioEventBus.subscribers {
				s.c <- ev
			}
			ioEventBus.mu.RUnlock()
		}
	}()
}

func SubscribeSystemEvents() (events <-chan SystemEvent, cancel func()) {
	ss := subscription{
		c:  make(chan SystemEvent, 1),
		id: atomic.AddUint64(&cnt, 1),
	}
	ioEventBus.mu.Lock()
	defer ioEventBus.mu.Unlock()
	ioEventBus.subscribers = append(ioEventBus.subscribers, ss)

	return ss.c, func() {
		ioEventBus.mu.Lock()
		defer ioEventBus.mu.Unlock()

		for i := range ioEventBus.subscribers {
			if ioEventBus.subscribers[i].id == ss.id {
				ioEventBus.subscribers = append(ioEventBus.subscribers[0:i], ioEventBus.subscribers[i+1:]...)
			}
		}
	}
}
