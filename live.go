package main

import (
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gordonklaus/portaudio"
)

const (
	livePidFile = "/tmp/mike-live.pid"
	chunkDur    = 3 * time.Second
)

var chunkSamples = int(SampleRate * chunkDur.Seconds())

// runLive toggles the live dictation mode on/off.
// First call: starts background daemon. Second call: stops it.
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

	// Re-invoke ourselves as a background daemon
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
		// Give it a moment to clean up
		time.Sleep(200 * time.Millisecond)
	}
	os.Remove(livePidFile)
	fmt.Println("  Stopped.")
}

// runLiveDaemon is the actual background process.
func runLiveDaemon(cfg *Config) {
	writePidFile(livePidFile, os.Getpid())
	defer os.Remove(livePidFile)

	// Log to file
	logFile, err := os.Create("/tmp/mike-live.log")
	if err == nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	}
	log.Printf("Live daemon started (PID %d)", os.Getpid())

	// Handle stop signals
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
	defer transcriber.Close()
	log.Printf("Model loaded")

	stopCh := make(chan struct{})
	go func() {
		<-sigCh
		log.Printf("Stop signal received")
		close(stopCh)
	}()

	runDictationLoop(stopCh, cfg, transcriber)
	log.Printf("Live dictation stopped")
}

// rmsEnergy calculates the root mean square energy of audio samples.
// Typical values: silence < 0.005, quiet speech ~0.01-0.05, loud speech ~0.05-0.3
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

// silenceThreshold is the energy below which audio is considered silence.
const silenceThreshold = 0.008

func runDictationLoop(stopCh <-chan struct{}, cfg *Config, transcriber *Transcriber) {
	buf := make([]float32, chunkSamples)

	stream, err := portaudio.OpenDefaultStream(1, 0, SampleRate, chunkSamples, buf)
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
	for i := 0; i < 4; i++ {
		if err := stream.Read(); err != nil {
			break
		}
	}

	log.Printf("Dictation loop started")

	var context []float32
	wasSilent := true

	for {
		select {
		case <-stopCh:
			if len(context) > 0 {
				flushAudio(context, cfg, transcriber)
			}
			return
		default:
		}

		if err := stream.Read(); err != nil {
			log.Printf("Stream read error: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}

		chunk := make([]float32, len(buf))
		copy(chunk, buf)

		// ---- Silence detection ----
		energy := rmsEnergy(chunk)
		if energy < silenceThreshold {
			if !wasSilent {
				log.Printf("Silence detected (energy=%.4f), stopping typing", energy)
				wasSilent = true
			}
			continue
		}
		wasSilent = false

		// Keep context buffer for final flush
		context = append(context, chunk...)
		maxCtx := chunkSamples * 2
		if len(context) > maxCtx {
			context = context[len(context)-maxCtx:]
		}

		// ---- Transcribe ----
		text, err := transcriber.Transcribe(chunk, cfg.Language, cfg.Threads)
		if err != nil {
			log.Printf("Transcription error: %v", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		text = strings.TrimSpace(text)

		// Filter out common hallucination patterns
		if text == "" || isHallucination(text) {
			continue
		}

		log.Printf("Spoke: %q  (energy=%.4f)", text, energy)

		// ---- Type ----
		if err := TypeText(text + " "); err != nil {
			log.Printf("Type error: %v", err)
			if hasTool("wl-copy") {
				exec.Command("wl-copy", text+" ").Run()
			}
		}

		// Brief pause to let the typing complete before next chunk
		time.Sleep(200 * time.Millisecond)
	}
}

// isHallucination checks if whisper produced a non-speech artifact.
func isHallucination(text string) bool {
	lower := strings.ToLower(text)
	hallucinations := []string{
		"[blank_audio]", "[silence]", "[music]", "[laughter]",
		"[applause]", "[noise]", "[typing]", "[ringing]", "[bell]",
		"[clicking]", "[cough]", "[sigh]", "[clapping]", "[sniff]",
		"(cough)", "(sighing)", "(sniffing)", "(clicking)",
	}
	for _, h := range hallucinations {
		if strings.Contains(lower, h) {
			return true
		}
	}
	return false
}

func flushAudio(audio []float32, cfg *Config, transcriber *Transcriber) {
	if len(audio) < int(SampleRate/2) {
		return
	}
	text, err := transcriber.Transcribe(audio, cfg.Language, cfg.Threads)
	if err != nil {
		log.Printf("Final transcription error: %v", err)
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	log.Printf("Final flush: %q", text)
	TypeText(text)
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
