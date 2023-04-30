//go:build darwin

package stun

import (
	"C"
)

var systemEvents = make(chan SystemEvent, 1)

type SystemEvent int

const (
	SystemEventSleep  SystemEvent = 0
	SystemEventWakeUp SystemEvent = 1
)

//export CanSleep
func CanSleep() C.int {
	return 1
}

//export WillWake
func WillWake() {
	systemEvents <- SystemEventWakeUp
}

//export WillSleep
func WillSleep() {
	systemEvents <- SystemEventSleep
}
