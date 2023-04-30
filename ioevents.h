#include <ctype.h>
#include <stdlib.h>
#include <stdio.h>

#include <mach/mach_port.h>
#include <mach/mach_interface.h>
#include <mach/mach_init.h>

#include <IOKit/pwr_mgt/IOPMLib.h>
#include <IOKit/IOMessage.h>

io_connect_t root_port;
IONotificationPortRef notifyPortRef;
io_object_t notifierObject;
void *refCon;
CFRunLoopRef runLoop;

void MySleepCallBack(void *refCon, io_service_t service, natural_t messageType, void *messageArgument) {
    switch (messageType) {
    case kIOMessageCanSystemSleep:
        if (CanSleep()) {
            IOAllowPowerChange(root_port, (long)messageArgument);
        } else {
            IOCancelPowerChange(root_port, (long)messageArgument);
        }

        break;
    case kIOMessageSystemWillSleep:
        WillSleep();
        IOAllowPowerChange(root_port, (long)messageArgument);
        break;
    case kIOMessageSystemWillPowerOn:
        WillWake();
        break;
    case kIOMessageSystemHasPoweredOn:
        break;
    default:
        break;
    }
}

// registerNotifications is called from go to register wake/sleep notifications
void registerNotifications() {
    root_port = IORegisterForSystemPower(refCon, &notifyPortRef, MySleepCallBack, &notifierObject);
    if (root_port == 0) {
        printf("IORegisterForSystemPower failed\n");
    }

    CFRunLoopAddSource(CFRunLoopGetCurrent(),
                    IONotificationPortGetRunLoopSource(notifyPortRef), kCFRunLoopCommonModes);

    runLoop = CFRunLoopGetCurrent();
    CFRunLoopRun();
}

// unregisterNotifications is called from go to remove wake/sleep notifications
void unregisterNotifications() {
    CFRunLoopRemoveSource(runLoop,
                        IONotificationPortGetRunLoopSource(notifyPortRef),
                        kCFRunLoopCommonModes);

    IODeregisterForSystemPower(&notifierObject);
    IOServiceClose(root_port);
    IONotificationPortDestroy(notifyPortRef);
    CFRunLoopStop(runLoop);
}
