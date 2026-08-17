//go:build promptguard

package screen

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/daulet/tokenizers"
	ort "github.com/yalue/onnxruntime_go"
)

// PromptGuard runs Meta's Llama Prompt Guard 2 86M classifier locally via
// ONNX Runtime (CoreML execution provider on macOS, CPU fallback). It is the
// second screening layer after the heuristics.
//
// Configuration (all via env):
//
//	POSTFACH_PG2_MODEL     path to model.onnx / model.quant.onnx (enables the screener)
//	POSTFACH_PG2_TOKENIZER path to tokenizer.json (default: next to the model)
//	POSTFACH_PG2_THRESHOLD malicious-score threshold, default 0.5
//	POSTFACH_ORT_LIB       path to libonnxruntime.dylib (default: third_party/, brew paths)
type PromptGuard struct {
	tk        *tokenizers.Tokenizer
	session   *ort.DynamicAdvancedSession
	inputs    []string
	clsID     uint32
	sepID     uint32
	threshold float32
}

// Two-scale scanning. The classifier scores a whole window: a short
// injection embedded in benign text dilutes below any usable threshold once
// the window grows past ~100 tokens (measured on model.quant.onnx: a
// 16-token injection scores 0.99 alone, 0.87 with ~50 benign tokens around
// it, 0.04 with ~100). Coarse windows catch wholesale jailbreak texts;
// fine windows catch injections hidden inside long benign emails.
const (
	pgMaxSeq      = 512 // model inspection window, incl. CLS/SEP
	pgWindow      = pgMaxSeq - 2
	pgOverlap     = 128
	pgFineWindow  = 64
	pgFineOverlap = 32
)

var ortInitOnce sync.Once
var ortInitErr error

func initORT() error {
	ortInitOnce.Do(func() {
		if lib := findORTLib(); lib != "" {
			ort.SetSharedLibraryPath(lib)
		}
		ortInitErr = ort.InitializeEnvironment()
	})
	return ortInitErr
}

func findORTLib() string {
	if p := os.Getenv("POSTFACH_ORT_LIB"); p != "" {
		return p
	}
	candidates := []string{
		"third_party/onnxruntime/lib/libonnxruntime.dylib",
		"/opt/homebrew/lib/libonnxruntime.dylib",
		"/usr/local/lib/libonnxruntime.dylib",
	}
	for _, c := range candidates {
		if matches, _ := filepath.Glob(strings.TrimSuffix(c, ".dylib") + "*.dylib"); len(matches) > 0 {
			return matches[0]
		}
	}
	return ""
}

// NewPromptGuardFromEnv builds the classifier screener if POSTFACH_PG2_MODEL
// is set; returns (nil, nil) when it is not configured.
func NewPromptGuardFromEnv() (Screener, error) {
	modelPath := os.Getenv("POSTFACH_PG2_MODEL")
	if modelPath == "" {
		return nil, nil
	}
	tokPath := os.Getenv("POSTFACH_PG2_TOKENIZER")
	if tokPath == "" {
		tokPath = filepath.Join(filepath.Dir(modelPath), "tokenizer.json")
	}
	threshold := 0.5
	if s := os.Getenv("POSTFACH_PG2_THRESHOLD"); s != "" {
		v, err := strconv.ParseFloat(s, 32)
		if err != nil || v <= 0 || v >= 1 {
			return nil, fmt.Errorf("POSTFACH_PG2_THRESHOLD: invalid value %q", s)
		}
		threshold = v
	}

	tk, err := loadTokenizerNoTruncation(tokPath)
	if err != nil {
		return nil, fmt.Errorf("load tokenizer %s: %w", tokPath, err)
	}

	if err := initORT(); err != nil {
		return nil, fmt.Errorf("initialize onnxruntime (set POSTFACH_ORT_LIB to libonnxruntime.dylib): %w", err)
	}

	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("onnxruntime session options: %w", err)
	}
	defer opts.Destroy()
	// CoreML is opt-in: the current ONNX export has unbounded dimensions,
	// which the CoreML compiler rejects node by node (and re-attempts per
	// input shape), so CPU is both quieter and faster. Re-evaluate once a
	// fixed-shape export is used.
	if v := os.Getenv("POSTFACH_PG2_COREML"); v == "1" || v == "true" {
		if err := opts.AppendExecutionProviderCoreMLV2(map[string]string{
			"ModelFormat":    "MLProgram",
			"MLComputeUnits": "ALL",
		}); err != nil {
			log.Printf("promptguard: CoreML unavailable (%v), falling back to CPU", err)
		}
	}

	inputInfo, outputInfo, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return nil, fmt.Errorf("inspect model %s: %w", modelPath, err)
	}
	inputs := make([]string, len(inputInfo))
	for i, in := range inputInfo {
		switch in.Name {
		case "input_ids", "attention_mask", "token_type_ids":
			inputs[i] = in.Name
		default:
			return nil, fmt.Errorf("model has unsupported input %q", in.Name)
		}
	}
	if len(outputInfo) == 0 {
		return nil, fmt.Errorf("model %s has no outputs", modelPath)
	}

	session, err := ort.NewDynamicAdvancedSession(modelPath, inputs, []string{outputInfo[0].Name}, opts)
	if err != nil {
		return nil, fmt.Errorf("create onnx session: %w", err)
	}

	pg := &PromptGuard{
		tk:        tk,
		session:   session,
		inputs:    inputs,
		threshold: float32(threshold),
	}
	// Learn CLS/SEP ids from the tokenizer itself: encoding an empty string
	// with special tokens yields exactly [CLS, SEP].
	if ids, _ := tk.Encode("", true); len(ids) >= 2 {
		pg.clsID, pg.sepID = ids[0], ids[len(ids)-1]
	} else {
		pg.clsID, pg.sepID = 1, 2 // DeBERTa-v2 defaults
	}
	return pg, nil
}

// loadTokenizerNoTruncation loads tokenizer.json with any baked-in
// truncation/padding removed, so long emails tokenize fully and chunking
// decides what the model sees.
func loadTokenizerNoTruncation(path string) (*tokenizers.Tokenizer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse tokenizer.json: %w", err)
	}
	null := json.RawMessage("null")
	m["truncation"] = null
	m["padding"] = null
	patched, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return tokenizers.FromBytes(patched)
}

func (p *PromptGuard) Name() string { return "promptguard" }

func (p *PromptGuard) Screen(ctx context.Context, text string) (Verdict, error) {
	var v Verdict
	if strings.TrimSpace(text) == "" {
		return v, nil
	}
	ids, _, err := p.tk.EncodeErr(text, false)
	if err != nil {
		return v, fmt.Errorf("tokenize: %w", err)
	}
	if len(ids) == 0 {
		return v, nil
	}

	chunks := chunkIDs(ids, pgWindow, pgOverlap)
	if len(ids) > pgFineWindow {
		chunks = append(chunks, chunkIDs(ids, pgFineWindow, pgFineOverlap)...)
	}
	var maxScore float32
	worst := -1
	for i, chunk := range chunks {
		if err := ctx.Err(); err != nil {
			return v, err
		}
		score, err := p.scoreChunk(chunk)
		if err != nil {
			return v, fmt.Errorf("promptguard inference (window %d/%d): %w", i+1, len(chunks), err)
		}
		if score > maxScore {
			maxScore, worst = score, i
		}
	}
	if maxScore >= p.threshold {
		v.Flagged = true
		v.Reasons = append(v.Reasons, fmt.Sprintf(
			"promptguard:malicious score %.3f in window %d/%d (threshold %.2f)",
			maxScore, worst+1, len(chunks), p.threshold))
	}
	return v, nil
}

// scoreChunk runs one <=512-token window and returns P(malicious).
func (p *PromptGuard) scoreChunk(chunk []uint32) (float32, error) {
	n := len(chunk) + 2
	inputIDs := make([]int64, 0, n)
	inputIDs = append(inputIDs, int64(p.clsID))
	for _, id := range chunk {
		inputIDs = append(inputIDs, int64(id))
	}
	inputIDs = append(inputIDs, int64(p.sepID))

	shape := ort.NewShape(1, int64(n))
	values := make([]ort.Value, len(p.inputs))
	defer func() {
		for _, v := range values {
			if v != nil {
				v.Destroy()
			}
		}
	}()
	for i, name := range p.inputs {
		var data []int64
		switch name {
		case "input_ids":
			data = inputIDs
		case "attention_mask":
			data = make([]int64, n)
			for j := range data {
				data[j] = 1
			}
		case "token_type_ids":
			data = make([]int64, n)
		}
		t, err := ort.NewTensor(shape, data)
		if err != nil {
			return 0, fmt.Errorf("create tensor %s: %w", name, err)
		}
		values[i] = t
	}

	outputs := []ort.Value{nil}
	if err := p.session.Run(values, outputs); err != nil {
		return 0, err
	}
	defer outputs[0].Destroy()
	logits, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return 0, fmt.Errorf("unexpected output type %T", outputs[0])
	}
	data := logits.GetData()
	if len(data) < 2 {
		return 0, fmt.Errorf("unexpected logits length %d", len(data))
	}
	// Binary head: index 0 = benign, index 1 = malicious. Stable softmax.
	l0, l1 := float64(data[0]), float64(data[1])
	m := math.Max(l0, l1)
	e0, e1 := math.Exp(l0-m), math.Exp(l1-m)
	return float32(e1 / (e0 + e1)), nil
}
