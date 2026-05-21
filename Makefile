# VoiceWriter - Voice-to-text CLI tool
# =====================================

# Project directories
WHISPER_DIR := whisper.cpp
WHISPER_BUILD := $(WHISPER_DIR)/build
MODEL_DIR := models
BINARY := mike

# Whisper model (tiny - ~75MB, lowest resource usage)
MODEL_URL := https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-tiny.bin
MODEL_FILE := $(MODEL_DIR)/ggml-tiny.bin

# Silero VAD model (~864KB, for voice activity detection)
VAD_MODEL_URL := https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v6.2.0.bin
VAD_MODEL_FILE := $(MODEL_DIR)/ggml-silero-vad.bin

# Detect OS
UNAME_S := $(shell uname -s)

# CGO flags for linking against the built whisper library
# Use absolute paths so cgo can find them regardless of the working directory
CGO_CFLAGS := -I$(abspath $(WHISPER_DIR)/include) -I$(abspath $(WHISPER_DIR)/ggml/include)
CGO_LDFLAGS := -L$(abspath $(WHISPER_BUILD)/src) -L$(abspath $(WHISPER_BUILD)/ggml/src) -lwhisper -lggml -lggml-base -lggml-cpu -lm -lstdc++

ifeq ($(UNAME_S), Linux)
CGO_LDFLAGS += -fopenmp
endif

export CGO_CFLAGS
export CGO_LDFLAGS

.PHONY: all clean model whisper-build build run

all: whisper-build model build
	@echo ""
	@echo "✓ mike built successfully!"
	@echo "  Run: ./$(BINARY)"
	@echo "  Config: ~/.config/mike/config.json"
	@echo "  Help:  ./$(BINARY) -h"
	@echo ""

# ---- Whisper C library ----

$(WHISPER_DIR)/CMakeLists.txt:
	@echo "Cloning whisper.cpp..."
	git clone --depth 1 https://github.com/ggml-org/whisper.cpp.git $(WHISPER_DIR)
	@echo "whisper.cpp cloned."

$(WHISPER_BUILD)/src/libwhisper.a: $(WHISPER_DIR)/CMakeLists.txt
	@echo "Building whisper C library (this may take a few minutes)..."
	cmake -S $(WHISPER_DIR) -B $(WHISPER_BUILD) \
		-DCMAKE_BUILD_TYPE=Release \
		-DBUILD_SHARED_LIBS=OFF
	cmake --build $(WHISPER_BUILD) --target whisper -j$$(nproc 2>/dev/null || echo 4)
	@echo "whisper C library built."

whisper-build: $(WHISPER_BUILD)/src/libwhisper.a

# ---- Download model ----

$(MODEL_FILE):
	@echo "Downloading whisper tiny model (75MB)..."
	mkdir -p $(MODEL_DIR)
	curl -L -o $(MODEL_FILE) "$(MODEL_URL)"
	@echo "Model downloaded: $(MODEL_FILE)"

$(VAD_MODEL_FILE):
	@echo "Downloading Silero VAD model (864KB)..."
	mkdir -p $(MODEL_DIR)
	curl -L -o $(VAD_MODEL_FILE) "$(VAD_MODEL_URL)"
	@echo "VAD model downloaded: $(VAD_MODEL_FILE)"

# Install model to the default config location
install-model: $(MODEL_FILE) $(VAD_MODEL_FILE)
	@echo "Installing models to ~/.config/mike/models/..."
	mkdir -p ~/.config/mike/models
	cp $(MODEL_FILE) ~/.config/mike/models/ggml-tiny.bin
	cp $(VAD_MODEL_FILE) ~/.config/mike/models/ggml-silero-vad.bin
	@echo "Models installed."

model: $(MODEL_FILE) $(VAD_MODEL_FILE) install-model

# ---- Build Go app ----

build: whisper-build model
	@echo "Building mike Go app..."
	go build -o $(BINARY) -ldflags="-s -w" .
	@echo "Binary: ./$(BINARY)"

# ---- Run ----

run: build
	./$(BINARY)

# ---- Clean ----

clean:
	rm -rf $(BINARY)
	go clean
	@echo "Cleaned."

clean-all: clean
	rm -rf $(WHISPER_DIR) $(MODEL_DIR)
	@echo "Full clean done."

# ---- Install system dependencies (Debian/Ubuntu) ----

install-deps:
	@echo "Installing system dependencies..."
	sudo apt-get install -y \
		portaudio19-dev \
		libx11-dev \
		libxtst-dev \
		libxfixes-dev \
		libxkbcommon-dev \
		cmake \
		g++ \
		curl \
		wtype \
		ydotool \
		wl-clipboard
	@echo "Dependencies installed."

# ---- Help ----

help:
	@echo "mike - Voice-to-text CLI tool"
	@echo ""
	@echo "Targets:"
	@echo "  all             Build everything (default)"
	@echo "  build           Build the Go app (requires whisper-build + model)"
	@echo "  run             Build and run"
	@echo "  model           Download the whisper model"
	@echo "  whisper-build   Build the whisper C library"
	@echo "  install-deps    Install system dependencies (Debian/Ubuntu)"
	@echo "  clean           Remove build artifacts"
	@echo "  clean-all       Remove everything, including whisper.cpp and models"
	@echo ""
	@echo "Config file: ~/.config/mike/config.json"
	@echo "Default hotkey: Ctrl+Space"
	@echo "Help:  ./mike -h"
	@echo ""
