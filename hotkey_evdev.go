package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	evKey          = 0x01
	evSyn          = 0x00
	keyStatePress  = 1
	keyStateRel    = 0
	keyStateRepeat = 2
)

type inputEvent struct {
	Sec  int64
	Usec int64
	Type uint16
	Code uint16
	Val  int32
}

type evdevEvent struct {
	Code    uint16
	Pressed bool
}

type EvdevWatcher struct {
	keyCode     uint16
	modCodes    []uint16
	keydown     chan<- struct{}
	keyup       chan<- struct{}
	devices     []string
	fds         []*os.File
	events      chan evdevEvent
	active      bool
	stopCh      chan struct{}
	wg          sync.WaitGroup
	mu          sync.Mutex
	pressedKeys map[uint16]bool
}

func NewEvdevWatcher(keyCode uint16, modCodes []uint16, keydown, keyup chan<- struct{}) (*EvdevWatcher, error) {
	devices, err := findKeyboardDevices()
	if err != nil {
		return nil, fmt.Errorf("finding keyboard devices: %w", err)
	}
	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Found %d potential keyboard devices:\n", len(devices))
		for _, d := range devices {
			fmt.Fprintf(os.Stderr, "[DEBUG]   %s\n", d)
		}
	}
	if len(devices) == 0 {
		return nil, errors.New("no keyboard devices found in /dev/input/")
	}
	return &EvdevWatcher{
		keyCode:     keyCode,
		modCodes:    modCodes,
		keydown:     keydown,
		keyup:       keyup,
		devices:     devices,
		events:      make(chan evdevEvent, 256),
		pressedKeys: make(map[uint16]bool),
		stopCh:      make(chan struct{}),
	}, nil
}

func (ew *EvdevWatcher) Start() error {
	ew.mu.Lock()
	defer ew.mu.Unlock()
	if ew.active {
		return nil
	}
	ew.active = true

	for _, dev := range ew.devices {
		f, err := os.Open(dev)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: couldn't open %s: %v\n", dev, err)
			continue
		}
		ew.fds = append(ew.fds, f)

		ew.wg.Add(1)
		go func(fd *os.File) {
			defer ew.wg.Done()
			ew.readDevice(fd)
		}(f)
	}

	if len(ew.fds) == 0 {
		ew.active = false
		return errors.New("could not open any keyboard device.\n" +
			"Try: sudo usermod -a -G input $USER")
	}

	fmt.Printf("Watching keyboard devices for hotkey...\n")

	ew.wg.Add(1)
	go ew.processLoop()

	return nil
}

func (ew *EvdevWatcher) Stop() {
	ew.mu.Lock()
	defer ew.mu.Unlock()
	if !ew.active {
		return
	}
	ew.active = false

	for _, f := range ew.fds {
		f.Close()
	}

	close(ew.stopCh)
	ew.wg.Wait()
	ew.fds = nil
}

func (ew *EvdevWatcher) readDevice(f *os.File) {
	buf := make([]byte, 24)
	for {
		n, err := f.Read(buf)
		if err != nil {
			return
		}
		if n < 24 {
			continue
		}

		var ev inputEvent
		ev.Sec = int64(binary.NativeEndian.Uint64(buf[0:8]))
		ev.Usec = int64(binary.NativeEndian.Uint64(buf[8:16]))
		ev.Type = binary.NativeEndian.Uint16(buf[16:18])
		ev.Code = binary.NativeEndian.Uint16(buf[18:20])
		ev.Val = int32(binary.NativeEndian.Uint32(buf[20:24]))

		if ev.Type != evKey || ev.Val == keyStateRepeat {
			continue
		}

		select {
		case ew.events <- evdevEvent{Code: ev.Code, Pressed: ev.Val == keyStatePress}:
		case <-ew.stopCh:
			return
		}
	}
}

func (ew *EvdevWatcher) processLoop() {
	defer ew.wg.Done()
	for {
		select {
		case <-ew.stopCh:
			return
		case evt := <-ew.events:
			if debug {
				action := "release"
				if evt.Pressed {
					action = "press  "
				}
				name := evdevKeyName(evt.Code)
				fmt.Fprintf(os.Stderr, "[DEBUG] evdev event: code=%3d (%s) %s\n", evt.Code, name, action)
			}

			ew.mu.Lock()

			if evt.Pressed {
				ew.pressedKeys[evt.Code] = true
			} else {
				delete(ew.pressedKeys, evt.Code)
			}

			allPressed := ew.pressedKeys[ew.keyCode]
			for _, mod := range ew.modCodes {
				if !ew.pressedKeys[mod] {
					allPressed = false
					break
				}
			}

			if debug {
				var keysDown []string
				for k, v := range ew.pressedKeys {
					if v {
						keysDown = append(keysDown, evdevKeyName(k))
					}
				}
				fmt.Fprintf(os.Stderr, "[DEBUG] Keys down: %v  allPressed=%v\n", keysDown, allPressed)
			}

			if allPressed && evt.Pressed {
				if debug {
					fmt.Fprintf(os.Stderr, "[DEBUG] Hotkey COMBO PRESSED! sending keydown\n")
				}
				select {
				case ew.keydown <- struct{}{}:
				default:
				}
			}

			if !allPressed && !evt.Pressed {
				if evt.Code == ew.keyCode {
					if debug {
						fmt.Fprintf(os.Stderr, "[DEBUG] Main key released, sending keyup\n")
					}
					select {
					case ew.keyup <- struct{}{}:
					default:
					}
				}
				for _, mod := range ew.modCodes {
					if evt.Code == mod {
						if debug {
							fmt.Fprintf(os.Stderr, "[DEBUG] Modifier released, sending keyup\n")
						}
						select {
						case ew.keyup <- struct{}{}:
						default:
						}
						break
					}
				}
			}

			ew.mu.Unlock()
		}
	}
}

func findKeyboardDevices() ([]string, error) {
	byPathDir := "/dev/input/by-path"
	entries, err := os.ReadDir(byPathDir)
	if err != nil {
		return scanEventDevices()
	}

	var devices []string
	for _, entry := range entries {
		name := entry.Name()
		if containsKbd(name) {
			resolved, err := filepath.EvalSymlinks(filepath.Join(byPathDir, name))
			if err == nil {
				devices = append(devices, resolved)
			}
		}
	}
	if len(devices) == 0 {
		return scanEventDevices()
	}
	return devices, nil
}

func scanEventDevices() ([]string, error) {
	devDir := "/dev/input"
	entries, err := os.ReadDir(devDir)
	if err != nil {
		return nil, err
	}
	var devices []string
	for _, entry := range entries {
		name := entry.Name()
		if len(name) > 5 && name[:5] == "event" {
			devices = append(devices, filepath.Join(devDir, name))
		}
	}
	return devices, nil
}

// evdevKeyName returns a human-readable name for evdev key codes.
func evdevKeyName(code uint16) string {
	names := map[uint16]string{
		1:   "ESC", 2: "1", 3: "2", 4: "3", 5: "4", 6: "5", 7: "6", 8: "7", 9: "8",
		10: "9", 11: "0", 12: "-", 13: "=", 14: "BKSP", 15: "TAB",
		16: "Q", 17: "W", 18: "E", 19: "R", 20: "T", 21: "Y", 22: "U",
		23: "I", 24: "O", 25: "P", 26: "[", 27: "]", 28: "ENTER",
		29: "L_CTRL", 30: "A", 31: "S", 32: "D", 33: "F", 34: "G", 35: "H",
		36: "J", 37: "K", 38: "L", 39: ";", 40: "'", 41: "`",
		42: "L_SHIFT", 43: "\\", 44: "Z", 45: "X", 46: "C", 47: "V",
		48: "B", 49: "N", 50: "M", 51: ",", 52: ".", 53: "/",
		54: "R_SHIFT", 55: "*", 56: "L_ALT", 57: "SPACE",
		58: "CAPS", 59: "F1", 60: "F2", 61: "F3", 62: "F4",
		63: "F5", 64: "F6", 65: "F7", 66: "F8", 67: "F9", 68: "F10",
		87: "F11", 88: "F12",
		97: "R_CTRL", 100: "R_ALT",
		105: "LEFT", 106: "RIGHT", 103: "UP", 108: "DOWN",
		125: "L_META", 126: "R_META",
	}
	if name, ok := names[code]; ok {
		return name
	}
	return fmt.Sprintf("KEY_%d", code)
}

func containsKbd(name string) bool {
	indicators := []string{"-kbd", "-keyboard"}
	lower := name
	for _, ind := range indicators {
		if len(lower) >= len(ind) {
			for i := 0; i <= len(lower)-len(ind); i++ {
				match := true
				for j := 0; j < len(ind); j++ {
					c1 := lower[i+j]
					c2 := ind[j]
					if c1 >= 'A' && c1 <= 'Z' {
						c1 += 32
					}
					if c2 >= 'A' && c2 <= 'Z' {
						c2 += 32
					}
					if c1 != c2 {
						match = false
						break
					}
				}
				if match {
					return true
				}
			}
		}
	}
	return false
}
