//go:build windows

package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// On Windows the agent reports the front application and window, screen locks and sign-in.
// Screenshots, recordings, live view and key/click counts are not built for Windows yet:
// see WINDOWS.md for the part a Windows developer still has to add.

// canCapture says whether this platform can take screenshots and recordings.
const canCapture = false

var errNoWindowsCapture = errors.New("screen capture is not available on Windows yet")

type window struct {
	app     string // program name without .exe, e.g. "chrome"
	appName string // display name, e.g. "Google Chrome"
	title   string
}

type screen struct{}

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procOpenInputDesktop         = user32.NewProc("OpenInputDesktop")
	procGetUserObjectInformation = user32.NewProc("GetUserObjectInformationW")
	procCloseDesktop             = user32.NewProc("CloseDesktop")

	procQueryFullProcessImageName = kernel32.NewProc("QueryFullProcessImageNameW")
	procCreateMutexW              = kernel32.NewProc("CreateMutexW")
	procGetConsoleMode            = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode            = kernel32.NewProc("SetConsoleMode")
)

const (
	processQueryLimitedInformation = 0x1000
	desktopReadObjects             = 0x0001
	uoiName                        = 2
	errorAlreadyExists             = 183
)

func newScreen() (*screen, error) { return &screen{}, nil }

// locked reports whether the lock screen (or another secure desktop such as a UAC prompt) is
// showing: then the input desktop is not this session's "Default" desktop, or cannot be opened.
func (s *screen) locked() bool {
	h, _, _ := procOpenInputDesktop.Call(0, 0, desktopReadObjects)
	if h == 0 {
		return true
	}
	defer procCloseDesktop.Call(h)
	var buf [64]uint16
	var n uint32
	ok, _, _ := procGetUserObjectInformation.Call(h, uoiName, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2), uintptr(unsafe.Pointer(&n)))
	if ok == 0 {
		return true
	}
	return !strings.EqualFold(syscall.UTF16ToString(buf[:]), "Default")
}

// activeWindow reads the front window's title and the program that owns it.
func (s *screen) activeWindow() (window, error) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return window{}, errors.New("no window in front")
	}
	var w window
	if n, _, _ := procGetWindowTextLengthW.Call(hwnd); n > 0 {
		buf := make([]uint16, n+1)
		procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		w.title = syscall.UTF16ToString(buf)
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid != 0 {
		w.app = processName(pid)
	}
	if w.app == "" {
		return window{}, errors.New("unknown window")
	}
	w.appName = friendlyName(w.app, w.title)
	return w, nil
}

// processName is the program file name of a process, lower case and without ".exe".
func processName(pid uint32) string {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return ""
	}
	defer syscall.CloseHandle(h)
	buf := make([]uint16, syscall.MAX_LONG_PATH)
	size := uint32(len(buf))
	if ok, _, _ := procQueryFullProcessImageName.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size))); ok == 0 {
		return ""
	}
	name := filepath.Base(syscall.UTF16ToString(buf[:size]))
	return strings.TrimSuffix(strings.ToLower(name), ".exe")
}

var knownApps = map[string]string{
	"chrome": "Google Chrome", "msedge": "Microsoft Edge", "firefox": "Firefox", "brave": "Brave", "opera": "Opera",
	"code": "Visual Studio Code", "devenv": "Visual Studio", "idea64": "IntelliJ IDEA", "pycharm64": "PyCharm",
	"explorer": "File Explorer", "notepad": "Notepad", "notepad++": "Notepad++", "windowsterminal": "Terminal",
	"cmd": "Command Prompt", "powershell": "PowerShell", "pwsh": "PowerShell",
	"winword": "Microsoft Word", "excel": "Microsoft Excel", "powerpnt": "Microsoft PowerPoint", "outlook": "Microsoft Outlook",
	"olk": "Microsoft Outlook", "onenote": "OneNote", "teams": "Microsoft Teams", "ms-teams": "Microsoft Teams",
	"slack": "Slack", "zoom": "Zoom", "telegram": "Telegram", "whatsapp": "WhatsApp", "thunderbird": "Thunderbird",
	"postman": "Postman", "pgadmin4": "pgAdmin 4", "figma": "Figma", "acrord32": "Adobe Acrobat", "acrobat": "Adobe Acrobat",
	"applicationframehost": "Windows app", "lockapp": "Lock screen",
}

// friendlyName turns a program name into what people call it. Store apps all run inside
// ApplicationFrameHost, so for them the window title is the better name.
func friendlyName(process, title string) string {
	if process == "applicationframehost" && title != "" {
		return title
	}
	if n, ok := knownApps[process]; ok {
		return n
	}
	return process
}

func (s *screen) screenshot(context.Context, string, int, int) error { return errNoWindowsCapture }
func (s *screen) record(context.Context, string, time.Duration, func() bool) error {
	return errNoWindowsCapture
}

// watchInput: counting key presses and clicks on Windows is not built yet.
func (s *screen) watchInput(context.Context, func(), func()) error { return errors.ErrUnsupported }

// sessionStart: the agent starts at sign-in, so its own start time is used.
func (s *screen) sessionStart() (time.Time, bool) { return time.Time{}, false }

// singleInstance makes sure only one agent runs in this sign-in session: a second copy finds
// the session's named mutex already there.
func singleInstance() (func(), error) {
	name, _ := syscall.UTF16PtrFromString(`Local\HRLensAgent`)
	h, _, err := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return nil, err
	}
	if errno, ok := err.(syscall.Errno); ok && errno == errorAlreadyExists {
		syscall.CloseHandle(syscall.Handle(h))
		return nil, errors.New("the agent is already running in this session")
	}
	return func() { syscall.CloseHandle(syscall.Handle(h)) }, nil
}

// echoOff stops the console showing what is typed (for the password) and returns the undo.
func echoOff() func() {
	in := uintptr(syscall.Stdin)
	var mode uint32
	if ok, _, _ := procGetConsoleMode.Call(in, uintptr(unsafe.Pointer(&mode))); ok == 0 {
		return nil
	}
	const enableEchoInput = 0x0004
	procSetConsoleMode.Call(in, uintptr(mode&^enableEchoInput))
	return func() { procSetConsoleMode.Call(in, uintptr(mode)) }
}
