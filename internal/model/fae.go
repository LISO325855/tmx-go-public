package model

import "strings"

const (
	FaeProvider                 = "fae"
	FreeWorkerProfile           = "free-worker"
	FaeDefaultModel             = "fae/qwen3-coder-next:latest"
	FaeBaseURL                  = "http://fae:11434/v1"
	FaeDefaultRuntimeContextCap = 32768
)

// FaeModelName returns the Ollama name of a model in the Fae provider.
func FaeModelName(qualified string) (string, bool) {
	name, ok := strings.CutPrefix(qualified, FaeProvider+"/")
	return name, ok && name != ""
}

// Model-specific context caps for runtime exceptions.
var faeContextOverrides = map[string]int{}

// FaeContextOverride returns a model-specific cap, or zero when there is none.
func FaeContextOverride(name string) int { return faeContextOverrides[name] }
