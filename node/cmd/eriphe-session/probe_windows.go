//go:build windows

package main

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The calls x/sys/windows has no wrapper for, from the system's own DLLs.
var (
	procGetLastInputInfo  = windows.NewLazySystemDLL("user32.dll").NewProc("GetLastInputInfo")
	procGetTickCount      = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount")
	procSHQueryUserNotifs = windows.NewLazySystemDLL("shell32.dll").NewProc("SHQueryUserNotificationState")
	procWTSQuerySession   = windows.NewLazySystemDLL("wtsapi32.dll").NewProc("WTSQuerySessionInformationW")
)

// read reads this session: each part on its own, a part that fails stays
// unknown. A few system calls, no window, no privilege.
func read() (probe, error) {
	var p probe
	p.Idle, p.IdleOK = idle()
	p.Locked = locked()
	p.QUNS = notificationState()
	p.Image = frontImage()
	return p, nil
}

// idle: since the session's last input. GetLastInputInfo gives the tick
// count (milliseconds since boot, 32 bits) of the last input; GetTickCount
// is the same clock, and the difference survives its wrap after 49 days.
func idle() (time.Duration, bool) {
	var li struct{ size, time uint32 }
	li.size = uint32(unsafe.Sizeof(li))
	if r, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&li))); r == 0 {
		return 0, false
	}
	now, _, _ := procGetTickCount.Call()
	return time.Duration(uint32(now)-li.time) * time.Millisecond, true
}

// wtsInfoEx is the start of WTSINFOEXW at level 1 (WTSINFOEX_LEVEL1_W): the
// union after Level is 8-aligned, for its LARGE_INTEGERs.
type wtsInfoEx struct {
	Level     uint32
	_         uint32
	SessionID uint32
	State     int32 // WTS_CONNECTSTATE_CLASS: 0, WTSActive, at the screen
	Flags     int32 // WTS_SESSIONSTATE_LOCK 0, _UNLOCK 1, _UNKNOWN -1
}

const (
	wtsCurrentSession = 0xFFFFFFFF
	wtsSessionInfoEx  = 25 // WTS_INFO_CLASS WTSSessionInfoEx
)

// locked: the screen is locked, or the session is not the one at the
// screen (another user's turn, a remote session that went away).
func locked() bool {
	var info *wtsInfoEx
	var n uint32
	r, _, _ := procWTSQuerySession.Call(0, wtsCurrentSession, wtsSessionInfoEx,
		uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(&n)))
	if r == 0 || info == nil {
		return false
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(info)))
	if n < uint32(unsafe.Sizeof(*info)) || info.Level != 1 {
		return false
	}
	return info.State != 0 || info.Flags == 0
}

// notificationState is SHQueryUserNotificationState's answer, 0 if none.
func notificationState() int {
	var state int32
	if hr, _, _ := procSHQueryUserNotifs.Call(uintptr(unsafe.Pointer(&state))); hr != 0 {
		return 0
	}
	return int(state)
}

// frontImage is the full path of the program whose window is in front, ""
// when there is none or it cannot be read (a protected process). Read with
// PROCESS_QUERY_LIMITED_INFORMATION, which an ordinary user has on their
// own programs, elevated ones included.
func frontImage() string {
	hwnd := windows.GetForegroundWindow()
	if hwnd == 0 {
		return ""
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err != nil || pid == 0 {
		return ""
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}
