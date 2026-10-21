//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Windows runs in a visible interactive session. Runtime probes and input-hook startup
// determine independently advertised capture, recording and aggregate input capabilities.

// canCapture says whether this platform can take screenshots and recordings.
const canCapture = false

type window struct {
	app     string // program name without .exe, e.g. "chrome"
	appName string // display name, e.g. "Google Chrome"
	title   string
}

type screen struct {
	state      sessionLifecycleState
	ffmpegPath string

	winMu             sync.Mutex
	hwnd              uintptr
	nid               notifyIconDataW
	taskbarCreatedMsg uint32
	stop              context.CancelFunc
}

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	wtsapi32 = syscall.NewLazyDLL("wtsapi32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")

	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procOpenInputDesktop         = user32.NewProc("OpenInputDesktop")
	procGetUserObjectInformation = user32.NewProc("GetUserObjectInformationW")
	procCloseDesktop             = user32.NewProc("CloseDesktop")
	procRegisterClassExW         = user32.NewProc("RegisterClassExW")
	procCreateWindowExW          = user32.NewProc("CreateWindowExW")
	procDestroyWindow            = user32.NewProc("DestroyWindow")
	procDefWindowProcW           = user32.NewProc("DefWindowProcW")
	procGetMessageW              = user32.NewProc("GetMessageW")
	procTranslateMessage         = user32.NewProc("TranslateMessage")
	procDispatchMessageW         = user32.NewProc("DispatchMessageW")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procPostQuitMessage          = user32.NewProc("PostQuitMessage")
	procLoadIconW                = user32.NewProc("LoadIconW")
	procRegisterWindowMessageW   = user32.NewProc("RegisterWindowMessageW")
	procCreatePopupMenu          = user32.NewProc("CreatePopupMenu")
	procAppendMenuW              = user32.NewProc("AppendMenuW")
	procTrackPopupMenu           = user32.NewProc("TrackPopupMenu")
	procDestroyMenu              = user32.NewProc("DestroyMenu")
	procGetCursorPos             = user32.NewProc("GetCursorPos")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")

	procQueryFullProcessImageName = kernel32.NewProc("QueryFullProcessImageNameW")
	procCreateMutexW              = kernel32.NewProc("CreateMutexW")
	procGetConsoleMode            = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode            = kernel32.NewProc("SetConsoleMode")
	procGetModuleHandleW          = kernel32.NewProc("GetModuleHandleW")
	procGetCurrentProcessId       = kernel32.NewProc("GetCurrentProcessId")
	procProcessIdToSessionId      = kernel32.NewProc("ProcessIdToSessionId")

	procWTSQuerySessionInformationW      = wtsapi32.NewProc("WTSQuerySessionInformationW")
	procWTSFreeMemory                    = wtsapi32.NewProc("WTSFreeMemory")
	procWTSRegisterSessionNotification   = wtsapi32.NewProc("WTSRegisterSessionNotification")
	procWTSUnRegisterSessionNotification = wtsapi32.NewProc("WTSUnRegisterSessionNotification")

	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
)

const (
	processQueryLimitedInformation = 0x1000
	desktopReadObjects             = 0x0001
	uoiName                        = 2
	errorAlreadyExists             = 183

	wtsCurrentServerHandle = uintptr(0)
	wtsCurrentSession      = ^uint32(0)
	wtsConnectStateClass   = 8
	wtsSessionInfoClass    = 24
	wtsSessionInfoExClass  = 25
	notifyForThisSession   = 0

	wsOverlappedWindow = 0x00CF0000

	wmNull             = 0x0000
	wmDestroy          = 0x0002
	wmClose            = 0x0010
	wmQueryEndSession  = 0x0011
	wmEndSession       = 0x0016
	wmCommand          = 0x0111
	wmPowerBroadcast   = 0x0218
	wmWTSSessionChange = 0x02B1
	wmRButtonUp        = 0x0205
	wmContextMenu      = 0x007B
	wmApp              = 0x8000
	wmTrayCallback     = wmApp + 1

	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	idiApplication = 32512

	mfString    = 0x00000000
	mfGrayed    = 0x00000001
	mfSeparator = 0x00000800

	tpmRightButton = 0x0002
	tpmNonotify    = 0x0080
	tpmReturnCmd   = 0x0100

	idTrayStatus = 1000
	idTrayQuit   = 1001
)

type wndClassExW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type winPoint struct {
	X int32
	Y int32
}

type winMsg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      winPoint
	Private uint32
}

var (
	activeLifecycleMu     sync.RWMutex
	activeLifecycleScreen *screen
	lifecycleWndProcOnce  sync.Once
	lifecycleWndProcPtr   uintptr
)

func newScreen() (*screen, error) {
	ensureProcessDPI()
	s := &screen{}
	s.refreshWTSState()
	return s, nil
}

func (s *screen) refreshWTSState() {
	var pidSession uint32
	hasPidSession := false
	if pid, _, _ := procGetCurrentProcessId.Call(); pid != 0 {
		if ok, _, _ := procProcessIdToSessionId.Call(pid, uintptr(unsafe.Pointer(&pidSession))); ok != 0 {
			hasPidSession = true
		}
	}

	if parsed, err := queryWTSSessionInfoEx(); err == nil {
		if hasPidSession && parsed.SessionID == 0 {
			parsed.SessionID = pidSession
		}
		if !parsed.HasLogonTime {
			if info, infoErr := queryWTSSessionInfo(); infoErr == nil && info.HasLogonTime {
				parsed.LogonTime = info.LogonTime
				parsed.HasLogonTime = true
			}
		}
		s.state.initFromWTS(parsed)
		return
	}

	if parsed, err := queryWTSSessionInfo(); err == nil {
		if hasPidSession && parsed.SessionID == 0 {
			parsed.SessionID = pidSession
		}
		s.state.initFromWTS(parsed)
		return
	}

	fallback := parsedWTSSession{Disconnected: true}
	if hasPidSession {
		fallback.SessionID = pidSession
	}
	if state, ok := queryWTSConnectState(); ok {
		fallback.ConnectState = state
		fallback.Disconnected = state != wtsActive
	}
	s.state.initFromWTS(fallback)
}

func queryWTSSessionBytes(infoClass uint32) ([]byte, error) {
	var bufPtr uintptr
	var bytesReturned uint32
	ok, _, callErr := procWTSQuerySessionInformationW.Call(
		wtsCurrentServerHandle,
		uintptr(wtsCurrentSession),
		uintptr(infoClass),
		uintptr(unsafe.Pointer(&bufPtr)),
		uintptr(unsafe.Pointer(&bytesReturned)),
	)
	if ok == 0 || bufPtr == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return nil, callErr
		}
		return nil, errors.New("WTSQuerySessionInformationW failed")
	}
	defer procWTSFreeMemory.Call(bufPtr)
	if bytesReturned == 0 {
		return nil, errors.New("WTSQuerySessionInformationW returned empty buffer")
	}
	raw := make([]byte, int(bytesReturned))
	copy(raw, unsafe.Slice((*byte)(unsafe.Pointer(bufPtr)), int(bytesReturned)))
	return raw, nil
}

func queryWTSSessionInfoEx() (parsedWTSSession, error) {
	raw, err := queryWTSSessionBytes(wtsSessionInfoExClass)
	if err != nil {
		return parsedWTSSession{}, err
	}
	return parseWTSInfoEx(raw)
}

func queryWTSSessionInfo() (parsedWTSSession, error) {
	raw, err := queryWTSSessionBytes(wtsSessionInfoClass)
	if err != nil {
		return parsedWTSSession{}, err
	}
	return parseWTSInfo(raw)
}

func queryWTSConnectState() (int32, bool) {
	raw, err := queryWTSSessionBytes(wtsConnectStateClass)
	if err != nil || len(raw) < 4 {
		return 0, false
	}
	state := *(*int32)(unsafe.Pointer(&raw[0]))
	return state, true
}

// secureDesktop reports whether a non-default desktop (such as UAC / credential prompt or
// Winlogon lock desktop) currently holds input focus.
func (s *screen) secureDesktop() bool {
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

// locked reports whether the Windows session is actually locked via WTS session state.
// UAC prompts on the secure desktop never count as session lock/unlock events.
func (s *screen) locked() bool {
	return s.state.attendanceLocked()
}

func (s *screen) attendanceLocked() bool {
	return s.state.attendanceLocked()
}

// captureSuspended reports whether activity/input/screen capture must pause because the
// session is locked, disconnected, power-suspended, or displaying a UAC/secure desktop.
func (s *screen) captureSuspended() bool {
	return s.state.captureSuspended(s.secureDesktop())
}

func (s *screen) canCaptureNow() bool {
	return !s.captureSuspended()
}

func (s *screen) sessionKey() string {
	s.state.mu.RLock()
	defer s.state.mu.RUnlock()
	if !s.state.hasSessionID {
		return ""
	}
	return fmt.Sprintf("session-%d", s.state.sessionID)
}

// sessionStart returns the user's Windows logon time from WTS session information.
func (s *screen) sessionStart() (time.Time, bool) {
	if t, ok := s.state.sessionStart(); ok {
		return t, true
	}
	s.refreshWTSState()
	return s.state.sessionStart()
}

// startLifecycle synchronously initializes the top-level hidden window, WTS session notifications,
// and visible tray icon before collection begins, failing closed if visible status cannot start.
func (s *screen) startLifecycle(ctx context.Context, stop context.CancelFunc) (func(), error) {
	s.state.mu.RLock()
	hasSID := s.state.hasSessionID
	sid := s.state.sessionID
	s.state.mu.RUnlock()
	if !hasSID || sid == 0 {
		return nil, errors.New("hrlens-agent must run inside an interactive user session, not Session 0")
	}

	initCh := make(chan error, 1)
	doneCh := make(chan struct{})

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(doneCh)
		defer stop()

		lifecycleWndProcOnce.Do(func() {
			lifecycleWndProcPtr = syscall.NewCallback(lifecycleWndProc)
		})

		activeLifecycleMu.Lock()
		activeLifecycleScreen = s
		activeLifecycleMu.Unlock()
		defer func() {
			activeLifecycleMu.Lock()
			if activeLifecycleScreen == s {
				activeLifecycleScreen = nil
			}
			activeLifecycleMu.Unlock()
		}()

		s.winMu.Lock()
		s.stop = stop
		if msgName, err := syscall.UTF16PtrFromString("TaskbarCreated"); err == nil {
			msg, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(msgName)))
			s.taskbarCreatedMsg = uint32(msg)
		}
		s.winMu.Unlock()

		hInst, _, _ := procGetModuleHandleW.Call(0)
		hIcon, _, _ := procLoadIconW.Call(0, uintptr(idiApplication))
		if hIcon == 0 {
			initCh <- errors.New("cannot load Windows status icon")
			return
		}

		className, _ := syscall.UTF16PtrFromString("HRLensAgentLifecycleWindow")
		windowTitle, _ := syscall.UTF16PtrFromString("HR Lens Agent")
		wc := wndClassExW{
			CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			LpfnWndProc:   lifecycleWndProcPtr,
			HInstance:     hInst,
			HIcon:         hIcon,
			LpszClassName: className,
		}
		_, _, _ = procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

		// Top-level hidden window (parent 0, never HWND_MESSAGE) so broadcast WM_QUERYENDSESSION,
		// WM_ENDSESSION, WM_POWERBROADCAST, and Explorer TaskbarCreated messages are delivered.
		hwnd, _, createErr := procCreateWindowExW.Call(
			0,
			uintptr(unsafe.Pointer(className)),
			uintptr(unsafe.Pointer(windowTitle)),
			wsOverlappedWindow,
			0, 0, 0, 0,
			0, // top-level window (not HWND_MESSAGE)
			0,
			hInst,
			0,
		)
		if hwnd == 0 {
			initCh <- fmt.Errorf("cannot create lifecycle window: %w", createErr)
			return
		}

		if ok, _, wtsErr := procWTSRegisterSessionNotification.Call(hwnd, notifyForThisSession); ok == 0 {
			_, _, _ = procDestroyWindow.Call(hwnd)
			initCh <- fmt.Errorf("cannot register WTS session notification: %w", wtsErr)
			return
		}

		var nid notifyIconDataW
		nid.CbSize = uint32(unsafe.Sizeof(nid))
		nid.HWnd = hwnd
		nid.UID = 1
		nid.UFlags = nifMessage | nifIcon | nifTip
		nid.UCallbackMessage = wmTrayCallback
		nid.HIcon = hIcon
		setNotifyIconTip(&nid, "HR Lens Agent — Running")

		if ok, _, trayErr := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid))); ok == 0 {
			_, _, _ = procWTSUnRegisterSessionNotification.Call(hwnd)
			_, _, _ = procDestroyWindow.Call(hwnd)
			initCh <- fmt.Errorf("cannot initialize visible tray status icon: %w", trayErr)
			return
		}

		s.winMu.Lock()
		s.hwnd = hwnd
		s.nid = nid
		s.winMu.Unlock()

		// Refresh WTS state now that notifications are registered on this window to avoid missing
		// any lock/disconnect transition during startup.
		s.refreshWTSState()

		watcherDone := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				_, _, _ = procPostMessageW.Call(hwnd, wmClose, 0, 0)
			case <-watcherDone:
			}
		}()

		initCh <- nil

		var msg winMsg
		for {
			ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(ret) <= 0 {
				break
			}
			_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
		}

		close(watcherDone)

		s.winMu.Lock()
		cleanNID := s.nid
		cleanHWND := s.hwnd
		s.hwnd = 0
		s.winMu.Unlock()

		if cleanNID.HWnd != 0 {
			_, _, _ = procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&cleanNID)))
		}
		if cleanHWND != 0 {
			_, _, _ = procWTSUnRegisterSessionNotification.Call(cleanHWND)
			_, _, _ = procDestroyWindow.Call(cleanHWND)
		}
	}()

	if err := <-initCh; err != nil {
		return nil, err
	}

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			s.winMu.Lock()
			hwnd := s.hwnd
			s.winMu.Unlock()
			if hwnd != 0 {
				_, _, _ = procPostMessageW.Call(hwnd, wmClose, 0, 0)
			}
			<-doneCh
		})
	}
	return cleanup, nil
}

func (s *screen) restoreTrayIcon() bool {
	s.winMu.Lock()
	nid := s.nid
	s.winMu.Unlock()
	if nid.HWnd == 0 {
		return false
	}
	if ok, _, _ := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid))); ok != 0 {
		return true
	}
	if ok, _, _ := procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid))); ok != 0 {
		return true
	}
	return false
}

func (s *screen) showTrayMenu(hwnd uintptr) {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	label := "HR Lens Agent (Monitoring Active)"
	if s.captureSuspended() {
		label = "HR Lens Agent (Capture Paused)"
	}
	statusLabel, _ := syscall.UTF16PtrFromString(label)
	quitLabel, _ := syscall.UTF16PtrFromString("Quit HR Lens Agent")
	_, _, _ = procAppendMenuW.Call(menu, mfString|mfGrayed, idTrayStatus, uintptr(unsafe.Pointer(statusLabel)))
	_, _, _ = procAppendMenuW.Call(menu, mfSeparator, 0, 0)
	_, _, _ = procAppendMenuW.Call(menu, mfString, idTrayQuit, uintptr(unsafe.Pointer(quitLabel)))

	var pt winPoint
	_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	_, _, _ = procSetForegroundWindow.Call(hwnd)
	cmd, _, _ := procTrackPopupMenu.Call(
		menu,
		tpmRightButton|tpmNonotify|tpmReturnCmd,
		uintptr(pt.X),
		uintptr(pt.Y),
		0,
		hwnd,
		0,
	)
	_, _, _ = procPostMessageW.Call(hwnd, wmNull, 0, 0)
	if cmd == idTrayQuit {
		s.requestStop()
		_, _, _ = procPostMessageW.Call(hwnd, wmClose, 0, 0)
	}
}

func (s *screen) requestStop() {
	s.winMu.Lock()
	stop := s.stop
	s.winMu.Unlock()
	if stop != nil {
		stop()
	}
}

func lifecycleWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	activeLifecycleMu.RLock()
	s := activeLifecycleScreen
	activeLifecycleMu.RUnlock()

	if s != nil {
		s.winMu.Lock()
		taskbarMsg := s.taskbarCreatedMsg
		s.winMu.Unlock()

		if taskbarMsg != 0 && msg == taskbarMsg {
			if !s.restoreTrayIcon() {
				s.requestStop()
				_, _, _ = procPostQuitMessage.Call(1)
			}
			return 0
		}

		switch msg {
		case wmWTSSessionChange:
			if s.state.applyWTSSessionChange(wParam) {
				s.requestStop()
			}
			return 0
		case wmPowerBroadcast:
			s.state.applyPowerBroadcast(wParam)
			return 1
		case wmQueryEndSession:
			s.state.mu.Lock()
			s.state.ending = true
			s.state.mu.Unlock()
			return 1
		case wmEndSession:
			s.state.mu.Lock()
			s.state.ending = wParam != 0
			if wParam != 0 {
				s.state.signedOut = true
			}
			s.state.mu.Unlock()
			if wParam != 0 {
				s.requestStop()
			}
			return 0
		case wmTrayCallback:
			event := uint32(lParam & 0xffff)
			if event == wmRButtonUp || event == wmContextMenu {
				s.showTrayMenu(hwnd)
			}
			return 0
		case wmCommand:
			if uintptr(wParam&0xffff) == idTrayQuit {
				s.requestStop()
				_, _, _ = procPostMessageW.Call(hwnd, wmClose, 0, 0)
				return 0
			}
		case wmClose:
			s.requestStop()
			s.winMu.Lock()
			nid := s.nid
			s.nid.HWnd = 0
			s.hwnd = 0
			s.winMu.Unlock()
			if nid.HWnd != 0 {
				_, _, _ = procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
			}
			_, _, _ = procWTSUnRegisterSessionNotification.Call(hwnd)
			_, _, _ = procDestroyWindow.Call(hwnd)
			return 0
		case wmDestroy:
			s.requestStop()
			_, _, _ = procPostQuitMessage.Call(0)
			return 0
		}
	}

	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
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
