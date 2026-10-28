//go:build windows

package vdisplay

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Parsec Virtual Display Driver's control interface and requests, as Parsec's own
// client (and NightDesk's VddClient.cs) use them.
var vddInterface = windows.GUID{Data1: 0x00b41627, Data2: 0x04c4, Data3: 0x429e, Data4: [8]byte{0xa2, 0x6e, 0x02, 0x65, 0xcf, 0x50, 0xc8, 0xfa}}

const (
	ioctlAdd     = 0x22E004
	ioctlRemove  = 0x22A008
	ioctlUpdate  = 0x22A00C
	ioctlVersion = 0x22E010

	// The driver removes its displays when it has not heard from the program that made them
	// for a while; Parsec's client says so every 100 ms.
	keepAliveEvery = 100 * time.Millisecond
)

var (
	user32                   = windows.NewLazySystemDLL("user32.dll")
	pEnumDisplayDevices      = user32.NewProc("EnumDisplayDevicesW")
	pEnumDisplaySettings     = user32.NewProc("EnumDisplaySettingsW")
	pChangeDisplaySettingsEx = user32.NewProc("ChangeDisplaySettingsExW")
	pGetSystemMetrics        = user32.NewProc("GetSystemMetrics")
	pGetDisplayConfigSizes   = user32.NewProc("GetDisplayConfigBufferSizes")
	pQueryDisplayConfig      = user32.NewProc("QueryDisplayConfig")
	pSetDisplayConfig        = user32.NewProc("SetDisplayConfig")
	pDisplayConfigGetInfo    = user32.NewProc("DisplayConfigGetDeviceInfo")
)

// ioBuf is what one request to the driver reads and writes. It lives on the heap, which does
// not move, because the driver writes to it after DeviceIoControl has returned.
type ioBuf struct {
	in  [32]byte
	out int32
	n   uint32
	ov  windows.Overlapped
}

// Windows is the System of a Windows PC, with the Parsec driver if it is installed.
type Windows struct {
	mu    sync.Mutex // one request to the driver at a time
	dev   windows.Handle
	buf   *ioBuf
	owned map[int]bool
	stop  chan struct{} // closed to end the keep-alive
}

// NewWindows makes the System. Nothing is opened until it is needed.
func NewWindows() *Windows { return &Windows{buf: new(ioBuf), owned: map[int]bool{}} }

// Driver reports whether the Parsec driver is installed and running, and its version.
func (s *Windows) Driver() (installed bool, version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.openLocked(); err != nil {
		return false, ""
	}
	v, err := s.ioctlLocked(ioctlVersion, nil, 1500*time.Millisecond)
	if err != nil {
		return true, ""
	}
	return true, fmt.Sprintf("%d.%d", v>>16&0xffff, v&0xffff)
}

// errNoDriver is what Ready says when the driver is missing.
var errNoDriver = errors.New("the Parsec Virtual Display Driver is not installed on the host")

func (s *Windows) openLocked() error {
	if s.dev != 0 {
		return nil
	}
	paths, err := windows.CM_Get_Device_Interface_List("", &vddInterface, windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT)
	if err != nil || len(paths) == 0 {
		return errNoDriver
	}
	for _, p := range paths {
		name, err := windows.UTF16PtrFromString(p)
		if err != nil {
			continue
		}
		h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
			nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OVERLAPPED, 0)
		if err == nil {
			s.dev = h
			return nil
		}
	}
	return errors.New("the Parsec Virtual Display Driver cannot be opened")
}

// ioctlLocked sends one request and waits up to timeout for its answer.
func (s *Windows) ioctlLocked(code uint32, in []byte, timeout time.Duration) (int32, error) {
	b := s.buf
	*b = ioBuf{}
	copy(b.in[:], in)
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(ev)
	b.ov.HEvent = ev
	err = windows.DeviceIoControl(s.dev, code, &b.in[0], uint32(len(b.in)), (*byte)(unsafe.Pointer(&b.out)), 4, nil, &b.ov)
	if err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
		return 0, err
	}
	if w, _ := windows.WaitForSingleObject(ev, uint32(timeout.Milliseconds())); w != windows.WAIT_OBJECT_0 {
		windows.CancelIoEx(s.dev, &b.ov)
		windows.GetOverlappedResult(s.dev, &b.ov, &b.n, true)
		return 0, errors.New("the Parsec Virtual Display Driver did not answer")
	}
	if err := windows.GetOverlappedResult(s.dev, &b.ov, &b.n, false); err != nil {
		return 0, err
	}
	return b.out, nil
}

// Ready says why a virtual display cannot be used now, or nil.
func (s *Windows) Ready() error {
	s.mu.Lock()
	err := s.openLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	const smRemoteSession = 0x1000
	if r, _, _ := pGetSystemMetrics.Call(smRemoteSession); r != 0 {
		return errors.New("go-nx runs in a Remote Desktop session on the host, which cannot change the console's displays")
	}
	if processRunning("ParsecVDisplay.exe") {
		return errors.New("ParsecVDisplay is managing the virtual displays on the host; close it first")
	}
	if !anyActive(func(d displayDevice) bool { return !d.parsec }) {
		return errors.New("the host has no monitor of its own active to come back to")
	}
	return nil
}

// Add makes a virtual display and waits for Windows to give it a name.
func (s *Windows) Add() (int, string, error) {
	before := map[string]bool{}
	for _, d := range displayDevices() {
		if d.parsec && d.active {
			before[strings.ToUpper(d.name)] = true
		}
	}
	s.mu.Lock()
	if err := s.openLocked(); err != nil {
		s.mu.Unlock()
		return 0, "", err
	}
	index, err := s.ioctlLocked(ioctlAdd, nil, 5*time.Second)
	if err == nil && index < 0 {
		err = errors.New("the driver refused to add a display")
	}
	if err != nil {
		s.mu.Unlock()
		return 0, "", err
	}
	s.owned[int(index)] = true
	if s.stop == nil {
		s.stop = make(chan struct{})
		go s.keepAlive(s.stop)
	}
	s.mu.Unlock()
	for i := 0; i < 50; i++ {
		for _, d := range displayDevices() {
			if d.parsec && d.active && !before[strings.ToUpper(d.name)] {
				return int(index), d.name, nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.Remove(int(index))
	return 0, "", errors.New("Windows did not turn the new virtual display on within 5 s")
}

func (s *Windows) keepAlive(stop chan struct{}) {
	t := time.NewTicker(keepAliveEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.mu.Lock()
			if s.dev != 0 {
				s.ioctlLocked(ioctlUpdate, nil, 500*time.Millisecond)
			}
			s.mu.Unlock()
		}
	}
}

// Remove removes a virtual display this program made.
func (s *Windows) Remove(index int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.owned[index] {
		return fmt.Errorf("virtual display %d was not made by go-nx", index)
	}
	if _, err := s.ioctlLocked(ioctlRemove, []byte{byte(index >> 8), byte(index)}, 1500*time.Millisecond); err != nil {
		return err
	}
	delete(s.owned, index)
	if len(s.owned) == 0 && s.stop != nil {
		close(s.stop)
		s.stop = nil
	}
	return nil
}

// Close removes what is left and lets go of the driver.
func (s *Windows) Close() {
	s.mu.Lock()
	var left []int
	for i := range s.owned {
		left = append(left, i)
	}
	s.mu.Unlock()
	for _, i := range left {
		s.Remove(i)
	}
	s.mu.Lock()
	if s.dev != 0 {
		windows.CloseHandle(s.dev)
		s.dev = 0
	}
	s.mu.Unlock()
}

// devMode is DEVMODEW for a display (220 bytes).
type devMode struct {
	DeviceName         [32]uint16
	SpecVersion        uint16
	DriverVersion      uint16
	Size               uint16
	DriverExtra        uint16
	Fields             uint32
	PositionX          int32
	PositionY          int32
	DisplayOrientation uint32
	DisplayFixedOutput uint32
	Color              int16
	Duplex             int16
	YResolution        int16
	TTOption           int16
	Collate            int16
	FormName           [32]uint16
	LogPixels          uint16
	BitsPerPel         uint32
	PelsWidth          uint32
	PelsHeight         uint32
	DisplayFlags       uint32
	DisplayFrequency   uint32
	ICMMethod          uint32
	ICMIntent          uint32
	MediaType          uint32
	DitherType         uint32
	Reserved1          uint32
	Reserved2          uint32
	PanningWidth       uint32
	PanningHeight      uint32
}

const (
	dmPosition         = 0x20
	dmBitsPerPel       = 0x40000
	dmPelsWidth        = 0x80000
	dmPelsHeight       = 0x100000
	dmDisplayFrequency = 0x400000
	enumCurrent        = 0xFFFFFFFF
	cdsTest            = 0x2
)

func enumSettings(device string, i uint32) (devMode, bool) {
	name, _ := windows.UTF16PtrFromString(device)
	dm := devMode{}
	dm.Size = uint16(unsafe.Sizeof(dm))
	r, _, _ := pEnumDisplaySettings.Call(uintptr(unsafe.Pointer(name)), uintptr(i), uintptr(unsafe.Pointer(&dm)))
	return dm, r != 0
}

// Modes lists the modes the display offers, each once.
func (s *Windows) Modes(device string) ([]Mode, error) {
	seen := map[Mode]bool{}
	var out []Mode
	for i := uint32(0); i < 4096; i++ {
		dm, ok := enumSettings(device, i)
		if !ok {
			break
		}
		m := Mode{int(dm.PelsWidth), int(dm.PelsHeight), int(dm.DisplayFrequency)}
		if dm.BitsPerPel >= 32 && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s offers no modes", device)
	}
	return out, nil
}

// SetMode tests the mode, then sets it until the next change (not saved for the next start).
func (s *Windows) SetMode(device string, m Mode) error {
	dm, ok := enumSettings(device, enumCurrent)
	if !ok {
		return fmt.Errorf("the current mode of %s cannot be read", device)
	}
	dm.Fields = dmPosition | dmBitsPerPel | dmPelsWidth | dmPelsHeight | dmDisplayFrequency
	dm.BitsPerPel = max(dm.BitsPerPel, 32)
	dm.PelsWidth, dm.PelsHeight, dm.DisplayFrequency = uint32(m.W), uint32(m.H), uint32(m.Hz)
	name, _ := windows.UTF16PtrFromString(device)
	if r, _, _ := pChangeDisplaySettingsEx.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&dm)), 0, cdsTest, 0); r != 0 {
		return fmt.Errorf("%s does not accept %s (code %d)", device, m, int32(r))
	}
	if r, _, _ := pChangeDisplaySettingsEx.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&dm)), 0, 0, 0); r != 0 {
		return fmt.Errorf("setting %s on %s failed (code %d)", m, device, int32(r))
	}
	return nil
}

// displayDevice is a display output, from EnumDisplayDevices.
type displayDevice struct {
	name   string // \\.\DISPLAY3
	desc   string
	active bool // attached to the desktop
	parsec bool
}

// displayDeviceW is DISPLAY_DEVICEW.
type displayDeviceW struct {
	cb           uint32
	DeviceName   [32]uint16
	DeviceString [128]uint16
	StateFlags   uint32
	DeviceID     [128]uint16
	DeviceKey    [128]uint16
}

func displayDevices() []displayDevice {
	const attachedToDesktop = 0x1
	var out []displayDevice
	for i := uint32(0); i < 64; i++ {
		dd := displayDeviceW{}
		dd.cb = uint32(unsafe.Sizeof(dd))
		if r, _, _ := pEnumDisplayDevices.Call(0, uintptr(i), uintptr(unsafe.Pointer(&dd)), 0); r == 0 {
			break
		}
		desc, id := windows.UTF16ToString(dd.DeviceString[:]), windows.UTF16ToString(dd.DeviceID[:])
		out = append(out, displayDevice{
			name: windows.UTF16ToString(dd.DeviceName[:]), desc: desc, active: dd.StateFlags&attachedToDesktop != 0,
			parsec: strings.Contains(strings.ToLower(desc), "parsec") || strings.Contains(strings.ToUpper(id), "PSCCDD"),
		})
	}
	return out
}

func anyActive(match func(displayDevice) bool) bool {
	for _, d := range displayDevices() {
		if d.active && match(d) {
			return true
		}
	}
	return false
}

func processRunning(exe string) bool {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err := windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), exe) {
			return true
		}
	}
	return false
}

// Flags of QueryDisplayConfig and SetDisplayConfig. The encoding is in topology.go.
const (
	qdcOnlyActive  = 0x2
	sdcUseSupplied = 0x20
	sdcValidate    = 0x40
	sdcApply       = 0x80
	sdcAllowChange = 0x400
)

// Snapshot is the active topology.
func (s *Windows) Snapshot() (Topology, error) {
	for attempt := 0; attempt < 5; attempt++ {
		var np, nm uint32
		if r, _, _ := pGetDisplayConfigSizes.Call(qdcOnlyActive, uintptr(unsafe.Pointer(&np)), uintptr(unsafe.Pointer(&nm))); r != 0 {
			return Topology{}, fmt.Errorf("reading the size of the display layout: %w", windows.Errno(r))
		}
		if np == 0 || np > 128 || nm == 0 || nm > 512 {
			return Topology{}, fmt.Errorf("the display layout has %d paths and %d modes", np, nm)
		}
		paths, modes := make([]byte, np*pathSize), make([]byte, nm*modeSize)
		r, _, _ := pQueryDisplayConfig.Call(qdcOnlyActive, uintptr(unsafe.Pointer(&np)), uintptr(unsafe.Pointer(&paths[0])),
			uintptr(unsafe.Pointer(&nm)), uintptr(unsafe.Pointer(&modes[0])), 0)
		if windows.Errno(r) == windows.ERROR_INSUFFICIENT_BUFFER {
			continue // it changed between the two calls
		}
		if r != 0 {
			return Topology{}, fmt.Errorf("reading the display layout: %w", windows.Errno(r))
		}
		t := Topology{Paths: paths[:np*pathSize], Modes: modes[:nm*modeSize]}
		return t, validTopology(t)
	}
	return Topology{}, errors.New("the display layout kept changing")
}

// UseOnly leaves device as the only active display, at the origin of the desktop.
func (s *Windows) UseOnly(device string) error {
	cur, err := s.Snapshot()
	if err != nil {
		return err
	}
	modes := append([]byte(nil), cur.Modes...)
	var paths []byte
	for off := 0; off < len(cur.Paths); off += pathSize {
		name, err := sourceName(cur.Paths[off : off+pathSize])
		if err != nil {
			return err
		}
		if !strings.EqualFold(name, device) {
			continue
		}
		src := binary.LittleEndian.Uint32(cur.Paths[off+12:])
		if src == 0xFFFFFFFF || int(src) >= len(modes)/modeSize || binary.LittleEndian.Uint32(modes[int(src)*modeSize:]) != 1 {
			return errors.New("the virtual display has no source mode")
		}
		clear(modes[int(src)*modeSize+28 : int(src)*modeSize+36]) // its position: the origin
		paths = append(paths, cur.Paths[off:off+pathSize]...)
	}
	if len(paths) == 0 {
		return fmt.Errorf("%s is not in the active display layout", device)
	}
	if err := s.Restore(Topology{Paths: paths, Modes: modes}); err != nil {
		return err
	}
	after, err := s.Snapshot()
	if err != nil {
		return err
	}
	names, err := sourceNames(after)
	if err != nil {
		return err
	}
	if len(names) != 1 || !strings.EqualFold(names[0], device) {
		return fmt.Errorf("Windows left %v active, not only %s", names, device)
	}
	return nil
}

// Restore validates, then applies, a topology.
func (s *Windows) Restore(t Topology) error {
	if err := validTopology(t); err != nil {
		return err
	}
	np, nm := uintptr(len(t.Paths)/pathSize), uintptr(len(t.Modes)/modeSize)
	call := func(flags uintptr) error {
		r, _, _ := pSetDisplayConfig.Call(np, uintptr(unsafe.Pointer(&t.Paths[0])), nm, uintptr(unsafe.Pointer(&t.Modes[0])), flags)
		if r != 0 {
			return windows.Errno(r)
		}
		return nil
	}
	if err := call(sdcUseSupplied | sdcValidate | sdcAllowChange); err != nil {
		return fmt.Errorf("Windows refused the display layout: %w", err)
	}
	if err := call(sdcUseSupplied | sdcApply | sdcAllowChange); err != nil {
		return fmt.Errorf("applying the display layout: %w", err)
	}
	return nil
}

// sourceName is the GDI name (\\.\DISPLAY3) of a path's source.
func sourceName(path []byte) (string, error) {
	req := make([]byte, 84)                   // DISPLAYCONFIG_SOURCE_DEVICE_NAME
	binary.LittleEndian.PutUint32(req[0:], 1) // DISPLAYCONFIG_DEVICE_INFO_GET_SOURCE_NAME
	binary.LittleEndian.PutUint32(req[4:], 84)
	copy(req[8:16], path[0:8])   // the source's adapter
	copy(req[16:20], path[8:12]) // and id
	if r, _, _ := pDisplayConfigGetInfo.Call(uintptr(unsafe.Pointer(&req[0]))); r != 0 {
		return "", fmt.Errorf("reading the name of a display source: %w", windows.Errno(r))
	}
	u := unsafe.Slice((*uint16)(unsafe.Pointer(&req[20])), 32)
	return windows.UTF16ToString(u), nil
}

func sourceNames(t Topology) ([]string, error) {
	var out []string
	for off := 0; off < len(t.Paths); off += pathSize {
		n, err := sourceName(t.Paths[off : off+pathSize])
		if err != nil {
			return nil, err
		}
		if !containsFold(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
