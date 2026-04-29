//go:build darwin

package macos

/*
#include <dispatch/dispatch.h>
#include <stdbool.h>
#include <string.h>

#import <Foundation/Foundation.h>
#import <ServiceManagement/ServiceManagement.h>
#import <ServiceManagement/SMErrors.h>

static void gwim_dispatch_main(void (^block)(void)) {
	if ([NSThread isMainThread]) {
		block();
	} else {
		dispatch_sync(dispatch_get_main_queue(), block);
	}
}

// gwim_launch_at_login_supported is true when the process runs from a .app
// bundle and the OS provides SMAppService (macOS 13+).
static bool gwim_launch_at_login_supported(void) {
	if (![[[NSBundle mainBundle] bundlePath] containsString:@".app"]) {
		return false;
	}
	if (@available(macOS 13.0, *)) {
		return true;
	}
	return false;
}

// gwim_launch_at_login_on returns whether the login item should appear checked:
// enabled or awaiting user approval in System Settings.
static bool gwim_launch_at_login_on(void) {
	if (!gwim_launch_at_login_supported()) {
		return false;
	}
	__block bool on = false;
	gwim_dispatch_main(^{
		if (@available(macOS 13.0, *)) {
			SMAppServiceStatus st = [SMAppService mainAppService].status;
			on = (st == SMAppServiceStatusEnabled ||
				st == SMAppServiceStatusRequiresApproval);
		}
	});
	return on;
}

static void gwim_copy_err(NSError *err, char *buf, size_t buflen) {
	if (buf == NULL || buflen == 0) {
		return;
	}
	buf[0] = '\0';
	if (err == nil) {
		return;
	}
	NSString *s = [err localizedDescription];
	if (s == nil) {
		return;
	}
	const char *utf8 = [s UTF8String];
	if (utf8 == NULL) {
		return;
	}
	strncpy(buf, utf8, buflen - 1);
	buf[buflen - 1] = '\0';
}

// gwim_set_launch_at_login registers or unregisters the main app as a login
// item. errbuf receives a localized message on failure (may be empty).
// enable: non-zero to register, zero to unregister.
// Returns 0 on success, -1 on failure.
static int gwim_set_launch_at_login(int enable, char *errbuf, size_t errlen) {
	if (!gwim_launch_at_login_supported()) {
		gwim_copy_err(nil, errbuf, errlen);
		if (errbuf && errlen > 0) {
			strncpy(errbuf, "open at login requires macOS 13+ and GWiM.app", errlen - 1);
			errbuf[errlen - 1] = '\0';
		}
		return -1;
	}

	__block int ret = 0;
	__block NSError *blockErr = nil;

	gwim_dispatch_main(^{
		if (@available(macOS 13.0, *)) {
			SMAppService *svc = [SMAppService mainAppService];
			NSError *err = nil;
			if (enable != 0) {
				BOOL ok = [svc registerAndReturnError:&err];
				if (!ok && err != nil &&
					[[err domain] isEqualToString:(__bridge NSString *)kSMErrorDomainFramework] &&
					(long)[err code] == (long)kSMErrorAlreadyRegistered) {
					ok = YES;
				}
				if (!ok) {
					blockErr = err;
					ret = -1;
				}
			} else {
				BOOL ok = [svc unregisterAndReturnError:&err];
				if (!ok && err != nil &&
					[[err domain] isEqualToString:(__bridge NSString *)kSMErrorDomainFramework] &&
					(long)[err code] == (long)kSMErrorJobNotFound) {
					ok = YES;
				}
				if (!ok) {
					blockErr = err;
					ret = -1;
				}
			}
		}
	});

	if (ret != 0) {
		gwim_copy_err(blockErr, errbuf, errlen);
	}
	return ret;
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// LaunchAtLoginSupported reports whether Open at Login can be used: macOS 13+
// or later and the process was launched from a .app bundle (not a bare binary).
func LaunchAtLoginSupported() bool {
	return bool(C.gwim_launch_at_login_supported())
}

// LaunchAtLoginEnabled reports whether the app is registered to open at login
// or is waiting for the user to approve it in System Settings.
func LaunchAtLoginEnabled() bool {
	return bool(C.gwim_launch_at_login_on())
}

// SetLaunchAtLogin registers (true) or unregisters (false) the main
// application as a login item via SMAppService.
func SetLaunchAtLogin(enable bool) error {
	if !LaunchAtLoginSupported() {
		return errors.New("open at login requires macOS 13+ and GWiM.app")
	}
	var cEnable C.int
	if enable {
		cEnable = 1
	}
	var buf [512]C.char
	rc := C.gwim_set_launch_at_login(cEnable, (*C.char)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)))
	if rc != 0 {
		msg := C.GoString((*C.char)(unsafe.Pointer(&buf[0])))
		if msg == "" {
			msg = "open at login failed"
		}
		return errors.New(msg)
	}
	return nil
}
