package config

// VoiceConfig is config.json "voice": whisper.cpp transcription (DESIGN.md "Voice"),
// with explicitly enabled fallback to configured local daemon hosts.
// Every field is optional; internal/transcribe fills the defaults.
type VoiceConfig struct {
	// ViaPeer routes all audio and reports to this paired fleet machine. No cloud fallback.
	ViaPeer string `json:"via_peer,omitempty"`
	// RemoteURL is Fae's speech service, an OpenAI-style POST <url>/v1/audio/transcriptions.
	// When set, it is tried first (no local worker loads in normal operation); on a connect
	// failure, 5xx or timeout, that one job runs on the local worker instead.
	RemoteURL string `json:"remote_url,omitempty"`
	// RemoteModel is the model field sent with RemoteURL's requests.
	RemoteModel string `json:"remote_model,omitempty"`
	// FallbackHosts permits dictation on the explicitly configured local hosts when
	// this engine is busy or unavailable. Off by default; Overview access alone is read-only.
	FallbackHosts bool `json:"fallback_hosts,omitempty"`
	// Model is the catalog name of the model jobs use ("" = base-q5_1).
	Model string `json:"model,omitempty"`
	// Language: a whisper language code ("de", "en", ...); "" or "auto" = detected per file.
	Language string `json:"language,omitempty"`
	// DictationLanguages bounds automatic dictation detection; first is the retry language.
	// Empty defaults to English/French. Explicit Language overrides automatic detection.
	DictationLanguages []string `json:"dictation_languages,omitempty"`
	// Threads the engine uses; 0 = min(4, CPUs/2).
	Threads int `json:"threads,omitempty"`
	// VAD skips silence with the Silero model (fewer invented words); nil = on when downloaded.
	VAD *bool `json:"vad,omitempty"`
	// AutoTranscribe transcribes finished recordings by themselves; nil = on (setting voice up
	// first when it is missing).
	AutoTranscribe *bool `json:"auto_transcribe,omitempty"`

	// Engine: "" or "whisper.cpp" = whisper-cli, once per job; "faster-whisper" = one resident
	// Python worker (DESIGN.md "Voice", the faster-whisper worker), whisper.cpp as its fallback.
	Engine string `json:"engine,omitempty"`
	// Python is the interpreter the worker runs on; "" = the venv `tmx voice setup` makes
	// under <state>/voice/fw.
	Python string `json:"python,omitempty"`
	// ToolsDir is a pip --target folder with faster-whisper, put first on the worker's
	// sys.path; its cuda and ctranslate2 folders are DLL folders, checked against its
	// manifest.json. "" = Python has faster-whisper itself.
	ToolsDir string `json:"tools_dir,omitempty"`
	// ModelDir is a CTranslate2 model folder; "" = <state>/voice/fw/models/faster-whisper-large-v3-turbo
	// (faster-whisper-small when only that one is there).
	ModelDir string `json:"model_dir,omitempty"`
	// Prompt is Whisper's initial prompt: the names it should spell right.
	Prompt string `json:"prompt,omitempty"`
	// Hotwords are passed to faster-whisper as hotwords.
	Hotwords string `json:"hotwords,omitempty"`
	// Cleanup is the pass over every dictated text (transcribe.Tidy, as in Python tmx):
	// "" or "local" = names spelled as in Vocabulary, Replacements applied, spacing tidied, the
	// first letter a capital; "off" = the text as heard.
	Cleanup string `json:"cleanup,omitempty"`
	// Vocabulary: names to spell exactly so, whatever case or spacing was heard.
	Vocabulary []string `json:"vocabulary,omitempty"`
	// Replacements: known mishearings, heard -> right (whole words, any case; literal text).
	Replacements map[string]string `json:"replacements,omitempty"`
	// Device ("auto", "cuda", "cpu") and ComputeType ("auto", "float16", "int8", ...): "" = auto,
	// cpu/int8 without a GPU; on cuda, auto picks the best of float16/int8_float16/int8 that
	// the device actually supports (voice_worker.py device_and_compute), never a float16 a
	// GPU like Forge's Pascal Quadro P4000 cannot run well.
	Device      string `json:"device,omitempty"`
	ComputeType string `json:"compute_type,omitempty"`
	// IdleUnloadS: the worker ends this many seconds after its last job, which frees the
	// GPU memory; 0 = 300.
	IdleUnloadS int `json:"idle_unload_s,omitempty"`
}

// EngineFasterWhisper is voice.engine for the resident faster-whisper worker.
const EngineFasterWhisper = "faster-whisper"

// AutoTranscribeOn reports config voice.auto_transcribe (default on).
func (v VoiceConfig) AutoTranscribeOn() bool { return v.AutoTranscribe == nil || *v.AutoTranscribe }
