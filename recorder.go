package main

import (
	"github.com/gordonklaus/portaudio"
)

const (
	// SampleRate is the audio sample rate required by whisper (16 kHz).
	SampleRate = 16000.0

	// FramesPerBuffer is the number of frames per audio buffer.
	// Smaller values give more responsive stop detection (~16ms at 16kHz).
	FramesPerBuffer = 256
)

// RecordAudio records audio from the default microphone until stopCh is closed.
// It returns 16 kHz mono float32 PCM samples suitable for whisper.
func RecordAudio(stopCh <-chan struct{}) ([]float32, error) {
	buf := make([]float32, FramesPerBuffer)

	stream, err := portaudio.OpenDefaultStream(1, 0, SampleRate, FramesPerBuffer, buf)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	if err := stream.Start(); err != nil {
		return nil, err
	}
	defer stream.Stop()

	// Drain any stale audio data that may be buffered
	for i := 0; i < 4; i++ {
		if err := stream.Read(); err != nil {
			break
		}
	}

	var recorded []float32
	for {
		// Check if we should stop (non-blocking)
		select {
		case <-stopCh:
			return recorded, nil
		default:
		}

		if err := stream.Read(); err != nil {
			return recorded, err
		}

		// Copy buffer (buf is reused on next Read)
		frame := make([]float32, len(buf))
		copy(frame, buf)
		recorded = append(recorded, frame...)
	}
}
