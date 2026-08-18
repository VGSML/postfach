# Must match the ORT API version expected by yalue/onnxruntime_go (v1.33 -> ORT 1.29).
ORT_VERSION        := 1.29.0
TOKENIZERS_VERSION := 1.27.0
THIRD_PARTY        := third_party

# Prompt Guard 2 variant: 86m (multilingual, default) or 22m (English-only,
# 4x smaller and ~2x faster, but blind to non-English injections — measured
# DE injection score 0.003 vs 0.991 on 86m). Usage: make fetch-model PG2_VARIANT=22m
PG2_VARIANT        ?= 86m
MODEL_DIR          := models/pg2-$(PG2_VARIANT)
HF_REPO            := https://huggingface.co/gravitee-io/Llama-Prompt-Guard-2-$(shell echo $(PG2_VARIANT) | tr a-z A-Z)-onnx/resolve/main

GUARD_ENV := CGO_LDFLAGS="-L$(CURDIR)/$(THIRD_PARTY)/lib"

.PHONY: build test vet build-guard test-guard deps-guard fetch-model clean

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

# --- Prompt Guard 2 (build tag: promptguard) --------------------------------

# Static lib for github.com/daulet/tokenizers (HF tokenizers Rust bindings).
$(THIRD_PARTY)/lib/libtokenizers.a:
	mkdir -p $(THIRD_PARTY)/lib
	curl -fsSL https://github.com/daulet/tokenizers/releases/download/v$(TOKENIZERS_VERSION)/libtokenizers.darwin-arm64.tar.gz \
		| tar -xz -C $(THIRD_PARTY)/lib

# ONNX Runtime shared library (loaded at runtime via dlopen).
# Archive layout varies between releases, so extract and move the inner dir.
$(THIRD_PARTY)/onnxruntime/lib:
	rm -rf $(THIRD_PARTY)/ort-tmp $(THIRD_PARTY)/onnxruntime
	mkdir -p $(THIRD_PARTY)/ort-tmp
	curl -fsSL https://github.com/microsoft/onnxruntime/releases/download/v$(ORT_VERSION)/onnxruntime-osx-arm64-$(ORT_VERSION).tgz \
		| tar -xz -C $(THIRD_PARTY)/ort-tmp
	mv $$(find $(THIRD_PARTY)/ort-tmp -maxdepth 2 -type d -name 'onnxruntime-osx-*' | head -1) $(THIRD_PARTY)/onnxruntime
	rm -rf $(THIRD_PARTY)/ort-tmp

deps-guard: $(THIRD_PARTY)/lib/libtokenizers.a $(THIRD_PARTY)/onnxruntime/lib

fetch-model:
	mkdir -p $(MODEL_DIR)
	test -f $(MODEL_DIR)/model.quant.onnx || curl -fSL -o $(MODEL_DIR)/model.quant.onnx $(HF_REPO)/model.quant.onnx
	test -f $(MODEL_DIR)/tokenizer.json  || curl -fsSL -o $(MODEL_DIR)/tokenizer.json  $(HF_REPO)/tokenizer.json

build-guard: deps-guard
	$(GUARD_ENV) go build -tags promptguard -o postfach-mcp ./cmd/postfach-mcp

test-guard: deps-guard
	$(GUARD_ENV) POSTFACH_PG2_MODEL=$(CURDIR)/$(MODEL_DIR)/model.quant.onnx \
		POSTFACH_ORT_LIB=$$(ls $(CURDIR)/$(THIRD_PARTY)/onnxruntime/lib/libonnxruntime.*.dylib | head -1) \
		go test -tags promptguard ./...

clean:
	rm -rf $(THIRD_PARTY) postfach-mcp
