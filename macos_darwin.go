package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit -framework UserNotifications -framework ServiceManagement
#include <stdbool.h>
#include <stdlib.h>

void notifyInit(void);
void notify(const char *title, const char *body, const char *url, const char *thread);
void openURL(const char *url);
const char *prompt(const char *title, const char *message, const char *button, const char *placeholder,
                   const char *value, bool multiline);
void showAlert(const char *title, const char *message);
bool loginItemEnabled(void);
const char *setLoginItem(bool enabled);
*/
import "C"

import (
	"errors"
	"unsafe"
)

// notifyInit must run before the app finishes launching, so clicks that relaunch the app are delivered.
func notifyInit() {
	C.notifyInit()
}

func notify(title, body, url, thread string) {
	t, b, u, th := C.CString(title), C.CString(body), C.CString(url), C.CString(thread)
	defer C.free(unsafe.Pointer(t))
	defer C.free(unsafe.Pointer(b))
	defer C.free(unsafe.Pointer(u))
	defer C.free(unsafe.Pointer(th))
	C.notify(t, b, u, th)
}

func openURL(url string) {
	u := C.CString(url)
	defer C.free(unsafe.Pointer(u))
	C.openURL(u)
}

// prompt shows a text input dialog and reports false if it was cancelled.
func prompt(title, message, button, placeholder, value string, multiline bool) (string, bool) {
	args := []*C.char{C.CString(title), C.CString(message), C.CString(button), C.CString(placeholder), C.CString(value)}
	defer func() {
		for _, a := range args {
			C.free(unsafe.Pointer(a))
		}
	}()
	s := C.prompt(args[0], args[1], args[2], args[3], args[4], C.bool(multiline))
	if s == nil {
		return "", false
	}
	defer C.free(unsafe.Pointer(s))
	return C.GoString(s), true
}

func showAlert(title, message string) {
	t, m := C.CString(title), C.CString(message)
	defer C.free(unsafe.Pointer(t))
	defer C.free(unsafe.Pointer(m))
	C.showAlert(t, m)
}

func loginItemEnabled() bool {
	return bool(C.loginItemEnabled())
}

func setLoginItem(enabled bool) error {
	msg := C.setLoginItem(C.bool(enabled))
	if msg == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(msg))
	return errors.New(C.GoString(msg))
}
