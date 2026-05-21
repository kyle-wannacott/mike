package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/go-vgo/robotgo"
)

// execCommand runs an external command, used as a utility by other functions.
func execCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s: %w\n%s", name, err, string(output))
	}
	return string(output), nil
}

// TypeText types the given text into the currently focused window.
// It auto-detects the display server (Wayland vs X11) and uses
// the appropriate method for each.
func TypeText(text string) error {
	if text == "" {
		return nil
	}

	switch detectDisplayServer() {
	case "wayland":
		return typeTextWayland(text)
	case "x11":
		return typeTextX11(text)
	default:
		return typeTextX11(text) // best effort fallback
	}
}

// detectDisplayServer returns "wayland", "x11", or "unknown".
func detectDisplayServer() string {
	if session := os.Getenv("XDG_SESSION_TYPE"); strings.EqualFold(session, "wayland") {
		return "wayland"
	}
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return "wayland"
	}
	if os.Getenv("DISPLAY") != "" {
		return "x11"
	}
	return "unknown"
}

// hasTool checks if a command is available in PATH.
func hasTool(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// ---- Wayland methods ----

// typeTextWayland types text on Wayland using available tools.
// Priority: wtype > ydotool > wl-copy fallback
func typeTextWayland(text string) error {
	// Method 1: wtype - best for typing text directly
	if hasTool("wtype") {
		return typeWithWtype(text)
	}

	// Method 2: ydotool - works via uinput
	if hasTool("ydotool") {
		return typeWithYdotool(text)
	}

	// Method 3: wl-copy + simulated paste
	// We can copy to clipboard and attempt paste
	return typeWithWlCopy(text)
}

// typeWithWtype types text using wtype (Wayland-native typing tool).
func typeWithWtype(text string) error {
	// wtype can type text directly character by character
	cmd := exec.Command("wtype", text)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("wtype failed: %w\n%s", err, string(output))
	}
	return nil
}

// typeWithYdotool types text using ydotool (uinput-based tool).
func typeWithYdotool(text string) error {
	// ydotool type types text character by character
	if err := exec.Command("ydotool", "type", text).Run(); err != nil {
		return fmt.Errorf("ydotool type failed: %w", err)
	}
	return nil
}

// typeWithWlCopy copies text to clipboard and attempts to simulate paste.
func typeWithWlCopy(text string) error {
	// Copy to clipboard using wl-copy
	if err := exec.Command("wl-copy", text).Run(); err != nil {
		return fmt.Errorf("wl-copy failed: %w", err)
	}

	// Try to simulate Ctrl+V using various tools
	if hasTool("wtype") {
		// wtype can send key combos
		return exec.Command("wtype", "-k", "ctrl", "v").Run()
	}
	if hasTool("ydotool") {
		return exec.Command("ydotool", "key", "29:1", "25:1", "25:0", "29:0").Run() // ctrl+v
	}

	return fmt.Errorf("cannot simulate paste on Wayland without wtype or ydotool.\n" +
		"Install them: sudo apt install wtype ydotool")
}

// ---- X11 method ----

// typeTextX11 types text on X11 using robotgo (clipboard paste method).
func typeTextX11(text string) error {
	// Save original clipboard
	origClip, _ := robotgo.ReadAll()

	// Set our text to clipboard
	if err := robotgo.WriteAll(text); err != nil {
		return fmt.Errorf("clipboard write failed: %w", err)
	}

	// Paste (Ctrl+V)
	if err := robotgo.KeyTap("v", "ctrl"); err != nil {
		// Restore clipboard on error
		if origClip != "" {
			go func() {
				robotgo.MilliSleep(50)
				robotgo.WriteAll(origClip)
			}()
		}
		return fmt.Errorf("keyboard paste failed: %w", err)
	}

	// Restore original clipboard after a brief delay
	go func() {
		robotgo.MilliSleep(100)
		if origClip != "" {
			robotgo.WriteAll(origClip)
		}
	}()

	return nil
}
