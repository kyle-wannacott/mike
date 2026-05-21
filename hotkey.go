package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.design/x/hotkey"
	"golang.design/x/hotkey/mainthread"
)

// HotkeyListener provides platform-independent hotkey detection.
// On X11, it uses the golang.design/x/hotkey package.
// On Wayland, it uses evdev (Linux input subsystem) directly.
type HotkeyListener struct {
	mods []hotkey.Modifier
	key  hotkey.Key

	// X11 hotkey
	x11hk *hotkey.Hotkey

	// Wayland evdev
	evdev *EvdevWatcher

	// Keydown/keyup channels (same interface regardless of backend)
	keydown chan struct{}
	keyup   chan struct{}
}

// NewHotkeyListener creates a hotkey listener for the given combination.
func NewHotkeyListener(modifiers []string, key string) (*HotkeyListener, error) {
	hl := &HotkeyListener{
		keydown: make(chan struct{}, 64),
		keyup:   make(chan struct{}, 64),
	}

	switch detectDisplayServer() {
	case "wayland":
		return hl.initWayland(modifiers, key)
	default:
		return hl.initX11(modifiers, key)
	}
}

func (hl *HotkeyListener) initX11(modifiers []string, key string) (*HotkeyListener, error) {
	var mods []hotkey.Modifier
	for _, m := range modifiers {
		mod, ok := modMap[m]
		if !ok {
			return nil, fmt.Errorf("unknown modifier: %s", m)
		}
		mods = append(mods, mod)
	}
	k, ok := keyMap[key]
	if !ok {
		return nil, fmt.Errorf("unknown key: %s", key)
	}
	hl.mods = mods
	hl.key = k

	// Create but don't register yet (registration needs main thread)
	hl.x11hk = hotkey.New(mods, k)
	return hl, nil
}

func (hl *HotkeyListener) initWayland(modifiers []string, key string) (*HotkeyListener, error) {
	// Convert string modifiers to evdev key codes
	modCodes, err := parseModifiersEvdev(modifiers)
	if err != nil {
		return nil, err
	}
	keyCode, err := parseKeyEvdev(key)
	if err != nil {
		return nil, fmt.Errorf("invalid key '%s': %w", key, err)
	}

	watcher, err := NewEvdevWatcher(keyCode, modCodes, hl.keydown, hl.keyup)
	if err != nil {
		return nil, fmt.Errorf("evdev init failed: %w", err)
	}
	hl.evdev = watcher
	return hl, nil
}

// Register activates the hotkey listener.
func (hl *HotkeyListener) Register() error {
	switch detectDisplayServer() {
	case "wayland":
		if hl.evdev == nil {
			return errors.New("evdev not initialized")
		}
		return hl.evdev.Start()
	default:
		if hl.x11hk == nil {
			return errors.New("X11 hotkey not initialized")
		}
		return hl.x11hk.Register()
	}
}

// Unregister deactivates the hotkey listener.
func (hl *HotkeyListener) Unregister() error {
	switch detectDisplayServer() {
	case "wayland":
		if hl.evdev != nil {
			hl.evdev.Stop()
		}
		return nil
	default:
		if hl.x11hk != nil {
			return hl.x11hk.Unregister()
		}
		return nil
	}
}

// Keydown returns a channel that fires when the hotkey combo is pressed.
func (hl *HotkeyListener) Keydown() <-chan struct{} {
	switch detectDisplayServer() {
	case "wayland":
		return hl.keydown
	default:
		if hl.x11hk != nil {
			// Convert hotkey.Event to struct{}
			ch := make(chan struct{}, 64)
			go func() {
				for range hl.x11hk.Keydown() {
					ch <- struct{}{}
				}
			}()
			return ch
		}
		return hl.keydown
	}
}

// Keyup returns a channel that fires when the hotkey combo is released.
func (hl *HotkeyListener) Keyup() <-chan struct{} {
	switch detectDisplayServer() {
	case "wayland":
		return hl.keyup
	default:
		if hl.x11hk != nil {
			ch := make(chan struct{}, 64)
			go func() {
				for range hl.x11hk.Keyup() {
					ch <- struct{}{}
				}
			}()
			return ch
		}
		return hl.keyup
	}
}

// RunMainThread runs the given function on the main OS thread
// (required by the X11 hotkey package). On Wayland, this is a no-op.
func RunMainThread(fn func()) {
	if detectDisplayServer() == "wayland" {
		fn()
	} else {
		mainthread.Init(fn)
	}
}

// ---- Key code tables ----

// evdevKeyCodes maps string key names to Linux evdev key codes.
var evdevKeyCodes = map[string]uint16{
	"space":  57,
	"enter":  28,
	"return": 28,
	"tab":    15,
	"escape": 1,
	"esc":    1,
	"delete": 111,
	"left":   105,
	"right":  106,
	"up":     103,
	"down":   108,
	"home":   102,
	"end":    107,
	"f1":     59,
	"f2":     60,
	"f3":     61,
	"f4":     62,
	"f5":     63,
	"f6":     64,
	"f7":     65,
	"f8":     66,
	"f9":     67,
	"f10":    68,
	"f11":    87,
	"f12":    88,
	"a":      30,
	"b":      48,
	"c":      46,
	"d":      32,
	"e":      18,
	"f":      33,
	"g":      34,
	"h":      35,
	"i":      23,
	"j":      36,
	"k":      37,
	"l":      38,
	"m":      50,
	"n":      49,
	"o":      24,
	"p":      25,
	"q":      16,
	"r":      19,
	"s":      31,
	"t":      20,
	"u":      22,
	"v":      47,
	"w":      17,
	"x":      45,
	"y":      21,
	"z":      44,
	"0":      11,
	"1":      2,
	"2":      3,
	"3":      4,
	"4":      5,
	"5":      6,
	"6":      7,
	"7":      8,
	"8":      9,
	"9":      10,
}

// evdevModCodes maps string modifier names to evdev key codes.
var evdevModCodes = map[string][]uint16{
	"ctrl":  {29, 97},  // KEY_LEFTCTRL, KEY_RIGHTCTRL
	"shift": {42, 54},  // KEY_LEFTSHIFT, KEY_RIGHTSHIFT
	"alt":   {56, 100}, // KEY_LEFTALT, KEY_RIGHTALT
	"mod1":  {56},      // ALT
	"mod2":  {99},      // KEY_COMPOSE / KEY_LEFTMETA ... varies
	"mod4":  {125, 126}, // KEY_LEFTMETA, KEY_RIGHTMETA (Super/Win)
}

func parseModifiersEvdev(modifiers []string) ([]uint16, error) {
	var codes []uint16
	for _, m := range modifiers {
		m = strings.ToLower(m)
		modCodes, ok := evdevModCodes[m]
		if !ok {
			return nil, fmt.Errorf("unknown modifier '%s' on Wayland (supported: ctrl, shift, alt, mod4)", m)
		}
		codes = append(codes, modCodes...)
	}
	return codes, nil
}

func parseKeyEvdev(key string) (uint16, error) {
	key = strings.ToLower(key)
	code, ok := evdevKeyCodes[key]
	if !ok {
		return 0, fmt.Errorf("unsupported key '%s' on Wayland", key)
	}
	return code, nil
}

// ---- Detect input group membership ----

func canReadInputDevices() bool {
	// Try to read one of the input devices
	devices, err := findKeyboardDevices()
	if err != nil || len(devices) == 0 {
		return false
	}
	f, err := os.Open(devices[0])
	if err != nil {
		return false
	}
	f.Close()
	return true
}
