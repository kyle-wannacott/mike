package main

import (
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gordonklaus/portaudio"
)

const (
	livePidFile = "/tmp/mike-live.pid"
	// Window of audio sent to whisper each cycle (3 seconds gives good context)
	windowSize = 16000 * 3 // 3 seconds at 16kHz
	// Minimum RMS energy to consider as speech (below this = silence)
	silenceThreshold = 0.006
)

// Pre-compiled regexes for cleaning transcription artifacts
var (
	reArtifact   = regexp.MustCompile(`\[.*?\]`)
	reArtifact2  = regexp.MustCompile(`\(.*?\)`)
	reMultiSpace = regexp.MustCompile(`\s+`)
)

// runLive toggles the live dictation mode on/off.
func runLive(cfg *Config) {
	pid, err := readPidFile(livePidFile)
	if err == nil && isProcessRunning(pid) {
		stopLiveDaemon(pid)
		return
	}
	startLiveDaemon(cfg)
}

func startLiveDaemon(cfg *Config) {
	fmt.Println("🎙️  Mike live dictation starting... (press Ctrl+Space again to stop)")

	args := os.Args
	daemonArgs := make([]string, 0, len(args))
	daemonArgs = append(daemonArgs, args[0])
	for _, a := range args[1:] {
		if a == "--live" {
			daemonArgs = append(daemonArgs, "--live-daemon")
		} else {
			daemonArgs = append(daemonArgs, a)
		}
	}

	cmd := exec.Command(daemonArgs[0], daemonArgs[1:]...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		log.Fatalf("Failed to start daemon: %v", err)
	}

	writePidFile(livePidFile, cmd.Process.Pid)
	fmt.Printf("  PID: %d\n", cmd.Process.Pid)
	cmd.Process.Release()
}

func stopLiveDaemon(pid int) {
	fmt.Println("⏹️  Stopping live dictation...")
	if proc, err := os.FindProcess(pid); err == nil {
		proc.Signal(syscall.SIGTERM)
		time.Sleep(200 * time.Millisecond)
	}
	os.Remove(livePidFile)
	fmt.Println("  Stopped.")
}

// runLiveDaemon is the actual background process.
func runLiveDaemon(cfg *Config) {
	writePidFile(livePidFile, os.Getpid())
	defer os.Remove(livePidFile)

	logFile, err := os.Create("/tmp/mike-live.log")
	if err == nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	}
	log.Printf("Live daemon started (PID %d)", os.Getpid())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	if err := portaudio.Initialize(); err != nil {
		log.Fatalf("Audio init error: %v", err)
	}
	defer portaudio.Terminate()
	log.Printf("Audio initialized")

	transcriber, err := NewTranscriber(cfg.ModelPath)
	if err != nil {
		log.Fatalf("Model load error: %v", err)
	}
	transcriber.SetVADModel(cfg.VADModelPath)
	defer transcriber.Close()
	log.Printf("Model loaded (VAD: %v)", cfg.VADModelPath != "")

	stopCh := make(chan struct{})
	go func() {
		<-sigCh
		log.Printf("Stop signal received")
		close(stopCh)
	}()

	runStreamingLoop(stopCh, cfg, transcriber)
	log.Printf("Live dictation stopped")
}

// ---- Streaming dictation with overlapping windows ----

// streamState holds the rolling audio buffer and dedup state.
type streamState struct {
	mu          sync.Mutex
	ring        []float32 // circular buffer of recent audio
	writePos    int       // current write position in ring
	totalWritten int     // total samples ever written
	lastText    string   // last transcribed text (for dedup)
}

func newStreamState() *streamState {
	return &streamState{
		ring: make([]float32, windowSize),
	}
}

func (s *streamState) writeSamples(samples []float32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sample := range samples {
		s.ring[s.writePos] = sample
		s.writePos = (s.writePos + 1) % len(s.ring)
	}
	s.totalWritten += len(samples)
}

// getLastN returns the last N samples from the ring buffer (or fewer if not enough data).
func (s *streamState) getLastN(n int) []float32 {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.totalWritten < n {
		n = s.totalWritten
	}
	if n <= 0 {
		return nil
	}

	result := make([]float32, n)
	start := (s.writePos - n + len(s.ring)) % len(s.ring)
	for i := 0; i < n; i++ {
		result[i] = s.ring[(start+i)%len(s.ring)]
	}
	return result
}

// rmsEnergy calculates RMS energy of audio samples.
func rmsEnergy(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s * s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// cleanTranscript removes artifact tags from transcribed text.
func cleanTranscript(text string) string {
	text = reArtifact.ReplaceAllString(text, "")
	text = reArtifact2.ReplaceAllString(text, "")
	text = strings.TrimSpace(text)
	text = reMultiSpace.ReplaceAllString(text, " ")
	return text
}

// runStreamingLoop records continuously and transcribes overlapping windows.
// New text is detected by comparing with previous transcription.
func runStreamingLoop(stopCh <-chan struct{}, cfg *Config, transcriber *Transcriber) {
	buf := make([]float32, 2048) // small buffer for microphone reads (128ms)

	stream, err := portaudio.OpenDefaultStream(1, 0, SampleRate, len(buf), buf)
	if err != nil {
		log.Printf("Failed to open audio stream: %v", err)
		return
	}
	defer stream.Close()

	if err := stream.Start(); err != nil {
		log.Printf("Failed to start audio stream: %v", err)
		return
	}
	defer stream.Stop()

	// Drain stale data
	for i := 0; i < 10; i++ {
		if err := stream.Read(); err != nil {
			break
		}
	}

	state := newStreamState()
	lastTyped := "" // text we've already typed

	log.Printf("Streaming loop started")

	// Timer for processing intervals (~1 second)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			log.Printf("Stop signal, flushing...")
			// Do one final transcription of the last 2 seconds
			final := state.getLastN(windowSize)
			if len(final) > SampleRate/2 && rmsEnergy(final) > silenceThreshold {
				text, _ := transcriber.Transcribe(final, cfg.Language, cfg.Threads)
				text = cleanTranscript(text)
				if text != "" && text != lastTyped {
					lastTyped = text
					log.Printf("Final: %q", text)
					typedText(text, &lastTyped)
				}
			}
			return
		case <-ticker.C:
			// Process the latest audio window
			window := state.getLastN(windowSize)
			if len(window) == 0 {
				continue
			}

			energy := rmsEnergy(window)
			if energy < silenceThreshold {
				continue
			}

			text, err := transcriber.Transcribe(window, cfg.Language, cfg.Threads)
			if err != nil {
				log.Printf("Transcription error: %v", err)
				continue
			}

			text = cleanTranscript(text)
			if text == "" {
				continue
			}

			log.Printf("Window: %q  (energy=%.4f)", text, energy)

			// Find new text since last typing
			newPart := findNewText(text, lastTyped)
			if newPart == "" {
				continue
			}

			log.Printf("New: %q", newPart)
			typedText(newPart, &lastTyped)

		default:
			// Read audio from microphone
			if err := stream.Read(); err != nil {
				continue
			}
			chunk := make([]float32, len(buf))
			copy(chunk, buf)
			state.writeSamples(chunk)
		}
	}
}

// findNewText compares new transcription with previously typed text
// and returns only the newly transcribed portion.
func findNewText(newText, oldText string) string {
	newText = strings.TrimSpace(newText)
	if newText == "" {
		return ""
	}
	if oldText == "" {
		return newText
	}

	// Normalize spaces for comparison
	normNew := reMultiSpace.ReplaceAllString(newText, " ")
	normOld := reMultiSpace.ReplaceAllString(oldText, " ")

	// If the new text is shorter or same, nothing new
	if len(normNew) <= len(normOld) {
		return ""
	}

	// Check if old text is a prefix of new text (common case)
	if strings.HasPrefix(strings.ToLower(normNew), strings.ToLower(normOld)) {
		return strings.TrimSpace(newText[len(oldText):])
	}

	// Otherwise, return the entire new text (might be a different take)
	// but only if it's significantly different
	if len(normNew) > len(normOld)*2 || !strings.Contains(strings.ToLower(normNew), strings.ToLower(normOld)) {
		return newText
	}

	return ""
}

// typedText types the text and updates the lastTyped tracker.
func typedText(text string, lastTyped *string) {
	if text == "" {
		return
	}
	if err := TypeText(text + " "); err != nil {
		log.Printf("Type error: %v", err)
		if hasTool("wl-copy") {
			exec.Command("wl-copy", text+" ").Run()
		}
	}
	*lastTyped = text
}

// ---- PID file helpers ----

func readPidFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func writePidFile(path string, pid int) error {
	os.MkdirAll(filepath.Dir(path), 0755)
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", pid)), 0644)
}

func isProcessRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
