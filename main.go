package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/gordonklaus/portaudio"
)

// recordingResult carries the audio data from the recording goroutine.
type recordingResult struct {
	audio []float32
	err   error
}

func main() {
	showHelp := flag.Bool("help", false, "Show help and configuration settings")
	flag.BoolVar(showHelp, "h", false, "Show help and configuration settings")
	verbose := flag.Bool("verbose", false, "Show detailed debug output")
	flag.BoolVar(verbose, "v", false, "Show detailed debug output")
	testMic := flag.Bool("test-mic", false, "Test microphone: record 3s and transcribe")
	captureSec := flag.Int("capture", 0, "Record for N seconds, transcribe, type, and exit (works as a desktop shortcut)")
	flag.Parse()

	if *showHelp {
		printHelp()
		return
	}

	debug = *verbose

	if *testMic {
		runMicTest()
		return
	}

	if *captureSec > 0 {
		runCapture(*captureSec)
		return
	}

	if detectDisplayServer() == "wayland" && !canReadInputDevices() {
		fmt.Println("╔══════════════════════════════════════════════════════════╗")
		fmt.Println("║  Wayland detected — evdev hotkey won't work.          ║")
		fmt.Println("║                                                       ║")
		fmt.Println("║  Use capture mode instead:                            ║")
		fmt.Println("║    mike --capture 5                                    ║")
		fmt.Println("║                                                       ║")
		fmt.Println("║  Bind it to Ctrl+Space in your desktop settings:      ║")
		fmt.Println("║    Settings → Keyboard → Keyboard Shortcuts           ║")
		fmt.Println("║    → Custom Shortcuts → Add (+)                        ║")
		fmt.Println("║    Name: Mike  Command: /path/to/mike --capture 5      ║")
		fmt.Println("║    Shortcut: Ctrl+Space                                ║")
		fmt.Println("╚══════════════════════════════════════════════════════════╝")
		fmt.Println()
		fmt.Println("Or test the pipeline directly:")
		fmt.Println("  mike --test-mic")
		fmt.Println()
		os.Exit(0)
	}

	RunMainThread(run)
}

var debug bool

func debugf(format string, args ...interface{}) {
	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] "+format+"\n", args...)
	}
}

func printHelp() {
	cfg, err := LoadConfig()
	if err != nil {
		log.Printf("Warning: could not load config: %v", err)
		cfg = DefaultConfig()
	}

	fmt.Println("mike — voice-to-text CLI tool")
	fmt.Println()
	fmt.Println("USAGE:")
	fmt.Println("  mike                  Start the voice transcription daemon (X11 only)")
	fmt.Println("  mike --capture N      Record for N seconds, transcribe & type, then exit")
	fmt.Println("  mike -h, --help       Show this help message")
	fmt.Println("  mike -v               Run with verbose debug output")
	fmt.Println("  mike --test-mic       Test microphone and transcription")
	fmt.Println()
	fmt.Println("HOW IT WORKS:")
	fmt.Println("  Press and hold the hotkey, speak into your microphone,")
	fmt.Println("  release the hotkey, and the transcribed text is typed")
	fmt.Println("  into whatever window has focus.")
	fmt.Println()
	fmt.Println("CURRENT SETTINGS:")
	fmt.Printf("  Hotkey:           %s\n", describeHotkey(cfg))
	fmt.Printf("  Model:            %s\n", cfg.ModelPath)
	fmt.Printf("  Language:         %s\n", cfg.Language)
	fmt.Printf("  CPU Threads:      %d\n", cfg.Threads)
	fmt.Printf("  Max Recording:    %d seconds\n", cfg.MaxDuration)
	fmt.Printf("  Display Server:   %s\n", detectDisplayServer())
	fmt.Println()
	fmt.Println("TYPING METHOD:")
	switch detectDisplayServer() {
	case "wayland":
		if hasTool("wtype") {
			fmt.Println("  Using: wtype (Wayland native typing)")
		} else if hasTool("ydotool") {
			fmt.Println("  Using: ydotool (uinput keyboard)")
		} else {
			fmt.Println("  WARNING: No Wayland typing tool found!")
			fmt.Println("  Install: sudo apt install wtype")
		}
	case "x11":
		fmt.Println("  Using: robotgo (XTest keyboard)")
	default:
		fmt.Println("  Unknown display server")
	}
	fmt.Println()
	fmt.Println("HOTKEY DETECTION:")
	switch detectDisplayServer() {
	case "wayland":
		fmt.Println("  Using: evdev (Linux input subsystem)")
		if !canReadInputDevices() {
			fmt.Println("  ⚠️  Cannot read /dev/input/ devices!")
			fmt.Println("  Run: sudo usermod -a -G input $USER")
			fmt.Println("  Then log out and back in.")
		}
	case "x11":
		fmt.Println("  Using: X11 (via XGrabKey)")
	}
	fmt.Println()
	fmt.Println("CONFIG FILE:")
	fmt.Printf("  %s\n", ConfigPath())
	fmt.Println("  Edit this file to change settings.")
	fmt.Println()
	fmt.Println("HOTKEY FORMAT:")
	fmt.Println("  Valid modifiers: ctrl, shift, alt, mod4 (super/win)")
	fmt.Println("  Valid keys:      a-z, 0-9, space, enter, tab, escape,")
	fmt.Println("                   delete, arrows, f1-f20")
	fmt.Println()
	fmt.Println("ENVIRONMENT:")
	fmt.Printf("  MIKE_CONFIG_DIR   Config directory (default: ~/.config/mike)\n")
	fmt.Println()
	fmt.Println("INSTALL:")
	fmt.Println("  sudo cp mike /usr/local/bin/    # Install system-wide")
	fmt.Println()
	fmt.Println("EXAMPLES:")
	fmt.Println("  mike                             # Run the daemon (X11)")
	fmt.Println("  mike --capture 5                 # Capture & transcribe 5 seconds")
	fmt.Println("  mike -v                          # Run with verbose logging")
	fmt.Println("  MIKE_CONFIG_DIR=./config mike    # Use custom config dir")
	fmt.Println()
	fmt.Println("WAYLAND USERS:")
	fmt.Println("  Bind 'mike --capture 5' to Ctrl+Space in:")
	fmt.Println("    Settings → Keyboard → Keyboard Shortcuts → Custom Shortcuts")
	fmt.Println()
	os.Exit(0)
}

func run() {
	// ---- Load configuration ----
	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	fmt.Printf("Config loaded from: %s\n", ConfigPath())
	fmt.Printf("  Model: %s\n", cfg.ModelPath)
	fmt.Printf("  Key:   %s\n", describeHotkey(cfg))
	fmt.Printf("  Display: %s\n", detectDisplayServer())

	// ---- Check preconditions ----
	if detectDisplayServer() == "wayland" && !hasTool("wtype") && !hasTool("ydotool") {
		fmt.Println("\n⚠️  WARNING: No Wayland typing tool found!")
		fmt.Println("   Install one for text typing to work:")
		fmt.Println("     sudo apt install wtype")
		fmt.Println()
	}

	if detectDisplayServer() == "wayland" && !canReadInputDevices() {
		fmt.Println("\n⚠️  WARNING: Cannot read keyboard input devices!")
		fmt.Println("   Run this command then log out and back in:")
		fmt.Println("     sudo usermod -a -G input $USER")
		fmt.Println()
	}

	// ---- Initialize PortAudio ----
	debugf("Initializing PortAudio...")
	if err := portaudio.Initialize(); err != nil {
		log.Fatalf("Failed to initialize audio: %v\n"+
			"Make sure you have a microphone connected and pulseaudio/alsa is working.", err)
	}
	defer portaudio.Terminate()
	fmt.Println("Audio system initialized.")

	// ---- Initialize whisper model ----
	debugf("Loading whisper model from %s...", cfg.ModelPath)
	transcriber, err := NewTranscriber(cfg.ModelPath)
	if err != nil {
		log.Fatalf("Failed to initialize transcriber: %v\n"+
			"Make sure the model file exists at: %s\n"+
			"Run 'make model' to download it.", err, cfg.ModelPath)
	}
	defer transcriber.Close()
	fmt.Println("Whisper model loaded.")

	// ---- Register global hotkey ----
	debugf("Creating hotkey listener for %s...", describeHotkey(cfg))
	hk, err := NewHotkeyListener(cfg.HotkeyModifiers, cfg.HotkeyKey)
	if err != nil {
		log.Fatalf("Failed to create hotkey: %v", err)
	}
	if err := hk.Register(); err != nil {
		log.Fatalf("Failed to register hotkey %s: %v", describeHotkey(cfg), err)
	}
	defer hk.Unregister()
	fmt.Printf("Hotkey registered: %s\n", describeHotkey(cfg))
	fmt.Printf("  Detection: %s\n", func() string {
		if detectDisplayServer() == "wayland" {
			return "evdev (Linux input subsystem)"
		}
		return "X11"
	}())

	// ---- Handle Ctrl+C gracefully ----
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		os.Exit(0)
	}()

	// ---- Main hotkey loop ----
	fmt.Println("\n┌────────────────────────────────────────────────────────┐")
	fmt.Printf("│  mike ready! Press %-37s│\n", describeHotkey(cfg))
	fmt.Println("│  Press and hold to record, release to transcribe.     │")
	fmt.Println("│  Press Ctrl+C to exit.                                │")
	fmt.Println("└────────────────────────────────────────────────────────┘")

	var (
		recording bool
		stopCh    chan struct{}
		resultCh  chan recordingResult
	)

	for {
		select {
		case <-hk.Keydown():
			if recording {
				debugf("Hotkey down but already recording, ignoring repeat")
				continue
			}
			recording = true
			debugf("Hotkey DOWN detected, starting recording")

			fmt.Print("\n🎤 Recording... (release to stop) ")

			stopCh = make(chan struct{})
			resultCh = make(chan recordingResult, 1)

			// Launch recording goroutine
			go func(stop chan struct{}, result chan recordingResult) {
				debugf("Recording goroutine started")
				audio, err := RecordAudio(stop)
				debugf("Recording goroutine finished, %d samples", len(audio))
				result <- recordingResult{audio, err}
			}(stopCh, resultCh)

		case <-hk.Keyup():
			if !recording {
				debugf("Hotkey up but not recording, ignoring")
				continue
			}
			recording = false
			debugf("Hotkey UP detected, stopping recording")
			close(stopCh)
			fmt.Println("✓")

			// Process result in a goroutine so main loop keeps listening for hotkeys
			go func() {
				debugf("Waiting for recording result...")
				res := <-resultCh
				debugf("Got recording result: %d samples, err=%v", len(res.audio), res.err)

				if res.err != nil {
					log.Printf("Recording error: %v", res.err)
					fmt.Println("Ready.")
					return
				}
				if len(res.audio) == 0 {
					fmt.Println("No audio recorded.")
					fmt.Println("Ready.")
					return
				}

				// Audio length in seconds
				audioSecs := float64(len(res.audio)) / SampleRate
				debugf("Audio duration: %.2f seconds (%d samples)", audioSecs, len(res.audio))

				if audioSecs < 0.1 {
					fmt.Printf("Recording too short (%.1f seconds).\n", audioSecs)
					fmt.Println("Ready.")
					return
				}

				fmt.Print("Transcribing... ")
				debugf("Starting transcription (%d samples, lang=%s, threads=%d)", len(res.audio), cfg.Language, cfg.Threads)
				startTime := time.Now()
				text, err := transcriber.Transcribe(res.audio, cfg.Language, cfg.Threads)
				elapsed := time.Since(startTime)
				debugf("Transcription took %v, err=%v", elapsed, err)

				if err != nil {
					fmt.Println("error!")
					log.Printf("Transcription error: %v", err)
					fmt.Println("Ready.")
					return
				}
				fmt.Println("✓")

				if text == "" {
					fmt.Println("No speech detected.")
					fmt.Println("Ready.")
					return
				}

				debugf("Transcribed text (%d chars): %s", len(text), text)
				fmt.Printf("Text: %s\n", text)

				fmt.Print("Typing... ")
				debugf("Attempting to type text...")
				if err := TypeText(text); err != nil {
					fmt.Println("error!")
					log.Printf("Type error: %v", err)
					debugf("Trying clipboard fallback...")
					// One more try: just copy to clipboard
					if hasTool("wl-copy") {
						execCommand("wl-copy", text)
						fmt.Println("(text copied to clipboard instead)")
					}
				} else {
					fmt.Println("✓")
					debugf("Text typed successfully")
				}
				fmt.Println("Ready.")
			}()
		}
	}
}

// runCapture records for N seconds, transcribes, and types the text.
// This is designed to be triggered by a desktop keyboard shortcut.
func runCapture(seconds int) {
	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if err := portaudio.Initialize(); err != nil {
		log.Fatalf("Failed to initialize audio: %v", err)
	}
	defer portaudio.Terminate()

	transcriber, err := NewTranscriber(cfg.ModelPath)
	if err != nil {
		log.Fatalf("Failed to load model: %v", err)
	}
	defer transcriber.Close()

	stopCh := make(chan struct{})
	go func() {
		time.Sleep(time.Duration(seconds) * time.Second)
		close(stopCh)
	}()

	audio, err := RecordAudio(stopCh)
	if err != nil {
		log.Printf("Recording error: %v", err)
		return
	}
	if len(audio) == 0 {
		return
	}

	text, err := transcriber.Transcribe(audio, cfg.Language, cfg.Threads)
	if err != nil {
		log.Printf("Transcription error: %v", err)
		return
	}
	if text == "" {
		return
	}

	// Try to type, fall back to clipboard
	if err := TypeText(text); err != nil {
		if hasTool("wl-copy") {
			execCommand("wl-copy", text)
		}
	}
}

// runMicTest records 3 seconds of audio and transcribes it for testing.
func runMicTest() {
	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	fmt.Println("=== Microphone Test ===")
	fmt.Printf("Model: %s\n", cfg.ModelPath)
	fmt.Println()

	if err := portaudio.Initialize(); err != nil {
		log.Fatalf("Failed to initialize audio: %v", err)
	}
	defer portaudio.Terminate()
	fmt.Println("Audio initialized.")

	transcriber, err := NewTranscriber(cfg.ModelPath)
	if err != nil {
		log.Fatalf("Failed to load model: %v", err)
	}
	defer transcriber.Close()
	fmt.Println("Whisper model loaded.")
	fmt.Println()

	fmt.Println("Recording for 3 seconds... Speak now!")
	stopCh := make(chan struct{})
	go func() {
		time.Sleep(3 * time.Second)
		close(stopCh)
	}()
	audio, err := RecordAudio(stopCh)
	if err != nil {
		log.Fatalf("Recording failed: %v", err)
	}
	fmt.Printf("Recorded %d samples (%.1f seconds)\n", len(audio), float64(len(audio))/SampleRate)

	if len(audio) == 0 {
		fmt.Println("No audio captured!")
		return
	}

	fmt.Print("Transcribing... ")
	text, err := transcriber.Transcribe(audio, cfg.Language, cfg.Threads)
	if err != nil {
		log.Fatalf("Transcription failed: %v", err)
	}
	fmt.Println(" done!")
	fmt.Println()
	fmt.Printf("Transcribed text: \"%s\"\n", text)
	fmt.Println()
	fmt.Println("Typing test...")
	if err := TypeText(text); err != nil {
		fmt.Printf("Type error: %v\n", err)
		fmt.Println("Text copied to clipboard instead.")
		execCommand("wl-copy", text)
	} else {
		fmt.Println("Text typed into focused window!")
	}
}

func describeHotkey(cfg *Config) string {
	s := ""
	for i, m := range cfg.HotkeyModifiers {
		if i > 0 {
			s += "+"
		}
		s += m
	}
	if s != "" {
		s += "+"
	}
	s += cfg.HotkeyKey
	return s
}
