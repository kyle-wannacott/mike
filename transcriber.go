package main

import (
	"fmt"
	"io"
	"os"

	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
)

// Transcriber wraps the whisper model for speech-to-text transcription.
type Transcriber struct {
	model       whisper.Model
	vadModel    string // path to VAD model (empty = no VAD)
}

// NewTranscriber loads a whisper model from the given path.
// If vadModelPath is non-empty, VAD (Voice Activity Detection) is enabled.
func NewTranscriber(modelPath string) (*Transcriber, error) {
	model, err := whisper.New(modelPath)
	if err != nil {
		return nil, fmt.Errorf("loading whisper model from %q: %w", modelPath, err)
	}
	return &Transcriber{model: model}, nil
}

// SetVADModel sets the VAD model path and enables VAD if the file exists.
func (t *Transcriber) SetVADModel(path string) {
	if _, err := os.Stat(path); err == nil {
		t.vadModel = path
	}
}

// Transcribe converts audio samples to text.
// audio: 16 kHz mono float32 PCM samples.
// lang: language code ("en", "auto", etc.).
// threads: number of CPU threads to use.
func (t *Transcriber) Transcribe(audio []float32, lang string, threads int) (string, error) {
	if len(audio) == 0 {
		return "", nil
	}

	ctx, err := t.model.NewContext()
	if err != nil {
		return "", fmt.Errorf("creating whisper context: %w", err)
	}

	ctx.SetLanguage(lang)
	ctx.SetThreads(uint(threads))

	// Enable VAD if model is available
	if t.vadModel != "" {
		ctx.SetVADModelPath(t.vadModel)
		ctx.SetVAD(true)
		ctx.SetVADThreshold(0.5)
		ctx.SetVADMinSpeechMs(250)
		ctx.SetVADMinSilenceMs(100)
		ctx.SetVADMaxSpeechSec(30)
		ctx.SetVADSpeechPadMs(400)
		ctx.SetVADSamplesOverlap(0.1)
	}

	if err := ctx.Process(audio, nil, nil, nil); err != nil {
		return "", fmt.Errorf("whisper processing: %w", err)
	}

	var text string
	for {
		seg, err := ctx.NextSegment()
		if err == io.EOF {
			break
		}
		if err != nil {
			return text, fmt.Errorf("reading segment: %w", err)
		}
		if text != "" {
			text += " "
		}
		text += seg.Text
	}

	return text, nil
}

// Close releases the model resources.
func (t *Transcriber) Close() {
	if t.model != nil {
		t.model.Close()
	}
}
